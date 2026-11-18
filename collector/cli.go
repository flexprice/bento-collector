// Package collector wires the `bento-flexprice collector` commands (serve, run, render).
package collector

import (
	"github.com/flexprice/bento-collector/collector/engine"
	"github.com/flexprice/bento-collector/collector/render"
	"github.com/flexprice/bento-collector/collector/server"
	"github.com/flexprice/bento-collector/collector/spec"
	"github.com/flexprice/bento-collector/collector/store"

	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const usage = `Usage: bento-flexprice collector <command> [flags]

Commands:
  serve    Run the collector UI and API
  run      Run collectors for one closed UTC day and send events to Flexprice
  render   Print the Bento config a collector renders to (secrets as env placeholders)

Run "bento-flexprice collector <command> -h" for flags.
`

// Main runs the collector subcommands and returns the process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	code := 0
	switch args[0] {
	case "serve":
		err = serve(args[1:], stderr)
	case "run":
		code, err = run(args[1:], stdout, stderr)
	case "render":
		err = renderCmd(args[1:], stdout)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return code
}

type commonFlags struct {
	data        string
	secrets     string
	git         bool
	connections string
	lock        bool
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.data, "data", envOr("COLLECTOR_DATA_DIR", "./collector-data"), "directory holding connections and collector specs [COLLECTOR_DATA_DIR]")
	fs.StringVar(&c.secrets, "secrets", os.Getenv("COLLECTOR_SECRETS_DIR"), "directory with one file per secret, e.g. a mounted Kubernetes Secret (default <data>/../collector-secrets) [COLLECTOR_SECRETS_DIR]")
	fs.BoolVar(&c.git, "git", os.Getenv("COLLECTOR_GIT") == "true", "commit every save to a git repository in the data directory [COLLECTOR_GIT]")
	fs.StringVar(&c.connections, "connections", os.Getenv("COLLECTOR_CONNECTIONS_FILE"), "YAML file of connections owned by the deployment; values may use ${ENV}, secrets come from secret_env [COLLECTOR_CONNECTIONS_FILE]")
	fs.BoolVar(&c.lock, "lock-connections", os.Getenv("COLLECTOR_LOCK_CONNECTIONS") == "true", "only allow connections from --connections; the UI cannot add or edit any [COLLECTOR_LOCK_CONNECTIONS]")
}

func (c *commonFlags) open() (store.Store, store.Secrets, string, error) {
	secrets := c.secrets
	if secrets == "" {
		secrets = filepath.Join(filepath.Dir(filepath.Clean(c.data)), "collector-secrets")
	}
	absData, _ := filepath.Abs(c.data)
	absSecrets, _ := filepath.Abs(secrets)
	if rel, err := filepath.Rel(absData, absSecrets); err == nil && !strings.HasPrefix(rel, "..") {
		return nil, nil, "", errors.New("the secrets directory must not be inside the data directory, which may be committed to git")
	}
	if c.lock && c.connections == "" {
		return nil, nil, "", errors.New("--lock-connections needs --connections (COLLECTOR_CONNECTIONS_FILE)")
	}
	files, err := store.NewFileStore(c.data, c.git)
	if err != nil {
		return nil, nil, "", err
	}
	var st store.Store = files
	if c.connections != "" {
		managed, err := store.LoadConnections(c.connections, os.Getenv)
		if err != nil {
			return nil, nil, "", err
		}
		st = store.NewManagedStore(files, managed, c.lock)
	}
	return st, store.EnvSecrets{Next: store.FileSecrets{Dir: secrets}, Getenv: os.Getenv}, secrets, nil
}

func serve(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var c commonFlags
	c.register(fs)
	addr := fs.String("addr", envOr("COLLECTOR_ADDR", ":8090"), "listen address [COLLECTOR_ADDR]")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, secrets, secretsDir, err := c.open()
	if err != nil {
		return err
	}
	token := os.Getenv("COLLECTOR_UI_TOKEN")
	srv := &server.Server{Store: st, Secrets: secrets, Engine: engine.NewEngine(st, secrets), Token: token, ConnectionsLocked: c.lock}
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdown)
	}()
	if token == "" {
		host, _, _ := net.SplitHostPort(*addr)
		if ip := net.ParseIP(host); (ip == nil || !ip.IsLoopback()) && host != "localhost" && os.Getenv("COLLECTOR_ALLOW_NO_AUTH") != "true" {
			return fmt.Errorf("set COLLECTOR_UI_TOKEN before listening on %s (or bind to 127.0.0.1, or set COLLECTOR_ALLOW_NO_AUTH=true behind your own auth proxy)", *addr)
		}
		fmt.Fprintln(stderr, "warning: COLLECTOR_UI_TOKEN is not set; the UI and API are open on", *addr)
	}
	fmt.Fprintf(stderr, "collector UI listening on %s (data %s, secrets %s)\n", *addr, c.data, secretsDir)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// run exits 0 when every collector succeeded, 1 when any failed or rejected
// rows, so a CronJob or ECS task retries and alerts on it.
func run(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var c commonFlags
	c.register(fs)
	var ids stringList
	fs.Var(&ids, "collector", "collector id to run; repeat for several")
	all := fs.Bool("all", false, "run every saved collector")
	day := fs.String("day", "", "UTC day YYYY-MM-DD (default yesterday)")
	from := fs.String("from", "", "inclusive start day for a backfill; runs every day from --from to --day")
	dryRun := fs.Bool("dry-run", false, "query and validate, but do not send events")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	st, secrets, _, err := c.open()
	if err != nil {
		return 1, err
	}
	now := time.Now()
	last, err := spec.ParseDay(*day, now)
	if err != nil {
		return 2, err
	}
	windows := []spec.Window{last}
	if *from != "" {
		first, err := spec.ParseDay(*from, now)
		if err != nil {
			return 2, err
		}
		if first.Start.After(last.Start) {
			return 2, errors.New("--from must not be after --day")
		}
		windows = nil
		for w := first; !w.Start.After(last.Start); w = spec.DayWindow(w.End) {
			windows = append(windows, w)
		}
	}
	var collectors []spec.Collector
	if *all {
		if collectors, err = st.ListCollectors(); err != nil {
			return 1, err
		}
	}
	for _, id := range ids {
		col, err := st.GetCollector(id, 0)
		if err != nil {
			return 1, err
		}
		collectors = append(collectors, col)
	}
	if len(collectors) == 0 {
		return 2, errors.New("nothing to run: pass --collector <id> or --all")
	}
	eng := engine.NewEngine(st, secrets)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	enc := json.NewEncoder(stdout)
	code := 0
	for _, col := range collectors {
		for _, w := range windows {
			res, err := eng.Run(ctx, col, w, *dryRun)
			if err != nil {
				fmt.Fprintf(stderr, "%s %s: %v\n", col.ID, w.Start.Format("2006-01-02"), err)
				code = 1
				continue
			}
			res.Rows = nil
			_ = enc.Encode(res)
			if !res.OK {
				code = 1
			}
		}
	}
	return code, nil
}

func renderCmd(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	var c commonFlags
	c.register(fs)
	id := fs.String("collector", "", "collector id")
	version := fs.Int("version", 0, "version (default latest)")
	day := fs.String("day", "", "UTC day YYYY-MM-DD (default yesterday)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, secrets, _, err := c.open()
	if err != nil {
		return err
	}
	col, err := st.GetCollector(*id, *version)
	if err != nil {
		return err
	}
	w, err := spec.ParseDay(*day, time.Now())
	if err != nil {
		return err
	}
	in, err := engine.NewEngine(st, secrets).Resolve(col, w, false)
	if err != nil {
		return err
	}
	y, err := render.RenderConfig(in)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, y)
	return err
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
