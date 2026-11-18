// Package collector wires the `bento-flexprice collector` commands (serve, render).
package collector

import (
	"github.com/flexprice/bento-collector/collector/engine"
	"github.com/flexprice/bento-collector/collector/server"
	"github.com/flexprice/bento-collector/collector/source"
	"github.com/flexprice/bento-collector/collector/source/prometheus"
	"github.com/flexprice/bento-collector/collector/spec"
	"github.com/flexprice/bento-collector/collector/store"

	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const usage = `Usage: bento-flexprice collector <command> [flags]

Commands:
  serve    Run the collector UI and API; publishing writes stream configs for
           bento-flexprice -w streams <streams dir>
  render   Print a collector's stream config (secrets as env placeholders); with
           --day, a one-off config for plain bento-flexprice -c (backfill)

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
	streams     string
	git         bool
	connections string
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.data, "data", envOr("COLLECTOR_DATA_DIR", "./collector-data"), "directory holding the versioned collector specs [COLLECTOR_DATA_DIR]")
	fs.StringVar(&c.streams, "streams", os.Getenv("COLLECTOR_STREAMS_DIR"), "directory published stream configs are written to, watched by bento-flexprice -w streams (default <data>/streams) [COLLECTOR_STREAMS_DIR]")
	fs.BoolVar(&c.git, "git", os.Getenv("COLLECTOR_GIT") == "true", "commit every save to a git repository in the data directory [COLLECTOR_GIT]")
	fs.StringVar(&c.connections, "connections", os.Getenv("COLLECTOR_CONNECTIONS_FILE"), "YAML file of connections owned by the deployment; values may use ${ENV}, secrets come from secret_env [COLLECTOR_CONNECTIONS_FILE]")
}

// sources are the input providers this build supports.
var sources = source.NewRegistry(prometheus.Provider{})

func (c *commonFlags) open() (store.Store, *engine.Engine, error) {
	if c.connections == "" {
		return nil, nil, errors.New("--connections (COLLECTOR_CONNECTIONS_FILE) is required: the YAML file listing source and Flexprice connections")
	}
	conns, err := store.LoadConnections(c.connections, os.Getenv)
	if err != nil {
		return nil, nil, err
	}
	streams := c.streams
	if streams == "" {
		streams = filepath.Join(c.data, "streams")
	}
	st, err := store.NewFileStore(c.data, streams, c.git)
	if err != nil {
		return nil, nil, err
	}
	eng, err := engine.New(st, conns, sources)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", c.connections, err)
	}
	return st, eng, nil
}

func serve(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var c commonFlags
	c.register(fs)
	addr := fs.String("addr", envOr("COLLECTOR_ADDR", ":8090"), "listen address [COLLECTOR_ADDR]")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, eng, err := c.open()
	if err != nil {
		return err
	}
	srv := &server.Server{Store: st, Engine: eng}
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdown)
	}()
	fmt.Fprintf(stderr, "collector UI listening on %s (data %s, connections %s)\n", *addr, c.data, c.connections)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func renderCmd(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	var c commonFlags
	c.register(fs)
	id := fs.String("collector", "", "collector id")
	version := fs.Int("version", 0, "version (default latest)")
	day := fs.String("day", "", "UTC day YYYY-MM-DD: a one-off config for that day, for backfills (default: the scheduled stream config publish writes)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, eng, err := c.open()
	if err != nil {
		return err
	}
	col, err := st.GetCollector(*id, *version)
	if err != nil {
		return err
	}
	var w *spec.Window
	if *day != "" {
		d, err := spec.ParseDay(*day, time.Now())
		if err != nil {
			return err
		}
		w = &d
	}
	y, err := eng.Render(col, w)
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
