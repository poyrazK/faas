package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/legal"
)

// registerLegalPages mounts GET /terms, /privacy and /acceptable-use.
// They are public: prospects read them before signing up. Until
// pkg/legal/legal.json names the operator, governing law and effective date
// the routes answer 404 exactly like an unknown path, so an unfinished
// document is never served.
func (s *server) registerLegalPages(mux *http.ServeMux) {
	cfg, err := legal.Load()
	if err != nil {
		// legal.json is embedded and checked by tests; a parse error is a
		// build defect, not a runtime condition. Serve nothing.
		s.log.Error("legal: documents disabled", "err", err)
	} else if missing := cfg.Missing(); len(missing) > 0 {
		s.log.Info("legal: documents unpublished", "missing", missing)
	}
	published := err == nil && cfg.Published()
	// Literal patterns: capgate's route-coverage test reads them statically.
	mux.HandleFunc("GET /terms", s.legalPage(cfg, published, legal.Terms))
	mux.HandleFunc("GET /privacy", s.legalPage(cfg, published, legal.Privacy))
	mux.HandleFunc("GET /acceptable-use", s.legalPage(cfg, published, legal.AcceptableUse))
}

func (s *server) legalPage(cfg legal.Config, published bool, doc string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !published {
			writeMissingRoute(w, r)
			return
		}
		body, err := legal.Render(cfg, doc, httpsec.NonceFromContext(r.Context()))
		if err != nil {
			s.log.Error("legal: render failed", "doc", doc, "err", err)
			writeMissingRoute(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(body)
	}
}
