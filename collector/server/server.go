// Package server is the collector's HTTP API plus the embedded UI.
package server

import (
	"github.com/flexprice/bento-collector/collector/engine"
	"github.com/flexprice/bento-collector/collector/spec"
	"github.com/flexprice/bento-collector/collector/store"
	"github.com/flexprice/bento-collector/collector/web"

	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Server is the control-plane API plus the embedded UI.
type Server struct {
	Store  store.Store
	Engine *engine.Engine
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	api := http.NewServeMux()
	api.HandleFunc("GET /api/connections", s.listConnections)
	api.HandleFunc("POST /api/connections/{id}/test", s.testConnection)
	api.HandleFunc("GET /api/collectors", s.listCollectors)
	api.HandleFunc("GET /api/collectors/{id}", s.getCollector)
	api.HandleFunc("PUT /api/collectors/{id}", s.saveCollector)
	api.HandleFunc("GET /api/collectors/{id}/versions", s.listVersions)
	api.HandleFunc("GET /api/collectors/{id}/versions/{version}", s.getVersion)
	api.HandleFunc("POST /api/collectors/{id}/publish", s.publish)
	api.HandleFunc("DELETE /api/collectors/{id}/publish", s.unpublish)
	api.HandleFunc("POST /api/preview", s.preview)
	api.HandleFunc("POST /api/render", s.render)
	mux.Handle("/api/", api)
	sub := web.FS()
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

func (s *Server) listConnections(w http.ResponseWriter, _ *http.Request) {
	writeJSONResp(w, http.StatusOK, s.Engine.Connections.List())
}

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Engine.Connections.Get(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	msg, err := s.Engine.TestConnection(ctx, r.PathValue("id"))
	if err != nil {
		writeJSONResp(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSONResp(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

// collectorView is the latest version plus which version Bento runs.
type collectorView struct {
	spec.Collector
	Published *store.Published `json:"published,omitempty"`
}

func (s *Server) view(c spec.Collector) (collectorView, error) {
	v := collectorView{Collector: c}
	p, err := s.Store.Published(c.ID)
	if errors.Is(err, store.ErrNotFound) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	v.Published = &p
	return v, nil
}

func (s *Server) listCollectors(w http.ResponseWriter, _ *http.Request) {
	cs, err := s.Store.ListCollectors()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]collectorView, 0, len(cs))
	for _, c := range cs {
		v, err := s.view(c)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, v)
	}
	writeJSONResp(w, http.StatusOK, out)
}

func (s *Server) getCollector(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.GetCollector(r.PathValue("id"), 0)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	v, err := s.view(c)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSONResp(w, http.StatusOK, v)
}

type publishRequest struct {
	// Version to publish; 0 means the latest.
	Version int `json:"version"`
}

// publish checks Flexprice and writes the version's stream config; Bento
// starts running it on its schedule. Publishing an older version rolls back.
func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := s.Store.GetCollector(r.PathValue("id"), req.Version)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	config, checks, err := s.Engine.Publish(ctx, c)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if engine.Blocked(checks) {
		var msgs []string
		for _, ch := range checks {
			if ch.Level == "error" {
				msgs = append(msgs, ch.Message)
			}
		}
		writeJSONResp(w, http.StatusConflict, map[string]any{"error": "Not published: " + strings.Join(msgs, " "), "checks": checks})
		return
	}
	p, err := s.Store.Publish(c.ID, c.Version, config, r.Header.Get("X-Collector-User"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSONResp(w, http.StatusOK, map[string]any{"published": p, "checks": checks})
}

func (s *Server) unpublish(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Unpublish(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	var c spec.Collector
	if !decode(w, r, &c) {
		return
	}
	c.ID = r.PathValue("id")
	c.SavedBy = r.Header.Get("X-Collector-User")
	c.Normalize()
	if err := s.Engine.Validate(c); err != nil {
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
	Collector spec.Collector `json:"collector"`
	Day       string         `json:"day"`
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	var req previewRequest
	if !decode(w, r, &req) {
		return
	}
	win, err := spec.ParseDay(req.Day, time.Now())
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
	var day *spec.Window
	if req.Day != "" {
		win, err := spec.ParseDay(req.Day, time.Now())
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		day = &win
	}
	req.Collector.Normalize()
	y, err := s.Engine.Render(req.Collector, day)
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
	if errors.Is(err, store.ErrNotFound) {
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
