package collector

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed ui
var uiFiles embed.FS

// Server is the control-plane API plus the embedded UI.
type Server struct {
	Store   Store
	Secrets Secrets
	Engine  *Engine
	// Token, when set, is required as "Authorization: Bearer <token>" on /api.
	Token string
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	api := http.NewServeMux()
	api.HandleFunc("GET /api/connections", s.listConnections)
	api.HandleFunc("PUT /api/connections/{id}", s.saveConnection)
	api.HandleFunc("POST /api/connections/{id}/test", s.testConnection)
	api.HandleFunc("GET /api/collectors", s.listCollectors)
	api.HandleFunc("GET /api/collectors/{id}", s.getCollector)
	api.HandleFunc("PUT /api/collectors/{id}", s.saveCollector)
	api.HandleFunc("GET /api/collectors/{id}/versions", s.listVersions)
	api.HandleFunc("GET /api/collectors/{id}/versions/{version}", s.getVersion)
	api.HandleFunc("POST /api/preview", s.preview)
	api.HandleFunc("POST /api/render", s.render)
	mux.Handle("/api/", s.auth(api))
	sub, _ := fs.Sub(uiFiles, "ui")
	mux.Handle("/", uiCache(http.FileServerFS(sub)))
	return securityHeaders(mux)
}

// Hashed assets never change; index.html must be revalidated so a new build is picked up.
func uiCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
				writeErr(w, http.StatusUnauthorized, errors.New("a valid collector token is required"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// connectionView never exposes the secret value, only whether it is set.
type connectionView struct {
	Connection
	HasSecret bool `json:"has_secret"`
}

func (s *Server) view(c Connection) connectionView {
	return connectionView{Connection: c, HasSecret: c.SecretRef != "" && s.Secrets.Has(c.SecretRef)}
}

func (s *Server) listConnections(w http.ResponseWriter, _ *http.Request) {
	cs, err := s.Store.ListConnections()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]connectionView, len(cs))
	for i, c := range cs {
		out[i] = s.view(c)
	}
	writeJSONResp(w, http.StatusOK, out)
}

func (s *Server) saveConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Connection
		Secret string `json:"secret"`
	}
	if !decode(w, r, &body) {
		return
	}
	c := body.Connection
	c.ID = r.PathValue("id")
	if c.SecretRef == "" && (body.Secret != "" || c.Type == ConnectionFlexprice || (c.AuthType != "" && c.AuthType != "none")) {
		c.SecretRef = c.ID
	}
	if err := c.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if body.Secret != "" {
		if err := s.Secrets.Put(c.SecretRef, body.Secret); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	saved, err := s.Store.SaveConnection(c)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSONResp(w, http.StatusOK, s.view(saved))
}

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.GetConnection(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	secret := ""
	if c.SecretRef != "" {
		if secret, err = s.Secrets.Get(c.SecretRef); err != nil {
			writeJSONResp(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var msg string
	switch c.Type {
	case ConnectionPrometheus:
		var v string
		v, err = PrometheusClient{HTTP: s.Engine.HTTP, Conn: c, Secret: secret}.Test(ctx)
		msg = "Connected to Prometheus " + v
	case ConnectionFlexprice:
		err = FlexpriceClient{HTTP: s.Engine.HTTP, Conn: c, APIKey: secret}.Test(ctx)
		msg = "Connected to Flexprice; the API key works"
	}
	if err != nil {
		writeJSONResp(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSONResp(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) listCollectors(w http.ResponseWriter, _ *http.Request) {
	cs, err := s.Store.ListCollectors()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if cs == nil {
		cs = []Collector{}
	}
	writeJSONResp(w, http.StatusOK, cs)
}

func (s *Server) getCollector(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.GetCollector(r.PathValue("id"), 0)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSONResp(w, http.StatusOK, c)
}

func (s *Server) getVersion(w http.ResponseWriter, r *http.Request) {
	v, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || v < 1 {
		writeErr(w, http.StatusBadRequest, errors.New("version must be a positive number"))
		return
	}
	c, err := s.Store.GetCollector(r.PathValue("id"), v)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSONResp(w, http.StatusOK, c)
}

func (s *Server) listVersions(w http.ResponseWriter, r *http.Request) {
	vs, err := s.Store.ListVersions(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSONResp(w, http.StatusOK, vs)
}

func (s *Server) saveCollector(w http.ResponseWriter, r *http.Request) {
	var c Collector
	if !decode(w, r, &c) {
		return
	}
	c.ID = r.PathValue("id")
	c.SavedBy = r.Header.Get("X-Collector-User")
	c.Normalize()
	if _, err := s.Engine.Resolve(c, DayWindow(time.Now()), false); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	saved, err := s.Store.SaveCollector(c)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSONResp(w, http.StatusOK, saved)
}

type previewRequest struct {
	Collector Collector `json:"collector"`
	Day       string    `json:"day"`
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	var req previewRequest
	if !decode(w, r, &req) {
		return
	}
	win, err := ParseDay(req.Day, time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Collector.Normalize()
	if err := req.Collector.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	res, err := s.Engine.Preview(r.Context(), req.Collector, win)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSONResp(w, http.StatusOK, res)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request) {
	var req previewRequest
	if !decode(w, r, &req) {
		return
	}
	win, err := ParseDay(req.Day, time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Collector.Normalize()
	in, err := s.Engine.Resolve(req.Collector, win, false)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	y, err := RenderConfig(in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSONResp(w, http.StatusOK, map[string]string{"yaml": y})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func statusFor(err error) int {
	if errors.Is(err, ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSONResp(w, code, map[string]string{"error": err.Error()})
}

func writeJSONResp(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
