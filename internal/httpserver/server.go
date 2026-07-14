// Package httpserver assembles the top-level HTTP router (assets + REST + UI)
// with middleware and provides graceful startup/shutdown.
package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/assets"
	"github.com/apexion/apexion/internal/api"
	"github.com/apexion/apexion/internal/config"
	"github.com/apexion/apexion/internal/ui"
)

// Server wraps the http.Server plus its router.
type Server struct {
	http *http.Server
	log  zerolog.Logger
}

// New builds the server, mounting assets, the REST API, and the UI.
func New(cfg config.ServerConfig, apiH *api.API, uiH *ui.Handler, log zerolog.Logger) *Server {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(requestLogger(log))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))

	// Static assets (embedded).
	r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(assets.FS()))))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// REST API.
	r.Mount("/api", apiH.Routes())
	// Server-rendered UI (catch-all, mounted last).
	r.Mount("/", uiH.Routes())

	return &Server{
		http: &http.Server{
			Addr:         cfg.Addr(),
			Handler:      r,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
		},
		log: log,
	}
}

// Start begins serving (blocking) until the server is closed.
func (s *Server) Start() error {
	s.log.Info().Str("addr", s.http.Addr).Msg("http server listening")
	if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully drains connections.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func requestLogger(log zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			// Skip noisy poll/asset logs at info level.
			ev := log.Debug()
			ev.Str("method", r.Method).Str("path", r.URL.Path).
				Int("status", ww.Status()).Dur("took", time.Since(start)).Msg("request")
		})
	}
}
