package apidserver

import (
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/legal"
	"github.com/onebox-faas/faas/pkg/state"
)

// loadLegal reads the embedded pkg/legal config once per process.
var loadLegal = sync.OnceValues(legal.Load)

// publishedLegal returns the legal config and whether its documents are
// published. The pages, the sign-in notice and the sign-up acceptance record
// all follow this one answer.
func publishedLegal() (legal.Config, bool) {
	cfg, err := loadLegal()
	return cfg, err == nil && cfg.Published()
}

// termsPublished reports whether sign-in pages show the Terms notice.
func termsPublished() bool {
	_, ok := publishedLegal()
	return ok
}

// signupTermsAcceptance is the Terms acceptance a new self-service account
// records, or nil while the Terms are unpublished.
func signupTermsAcceptance(method string) *state.TermsAcceptance {
	cfg, ok := publishedLegal()
	if !ok {
		return nil
	}
	return &state.TermsAcceptance{Version: cfg.Version(), Method: method}
}

// registerLegalPages mounts GET /terms, /privacy and /acceptable-use.
// They are public: prospects read them before signing up. Until
// pkg/legal/legal.json is complete the routes answer 404 exactly like an
// unknown path, so an unfinished document is never served.
func (s *server) registerLegalPages(mux *http.ServeMux) {
	cfg, err := loadLegal()
	if err != nil {
		// legal.json is embedded and checked by tests; a parse error is a
		// build defect, not a runtime condition. Serve nothing.
		s.log.Error("legal: documents disabled", "err", err)
	} else if missing := cfg.Missing(); len(missing) > 0 {
		s.log.Info("legal: documents unpublished", "missing", missing)
	}
	_, published := publishedLegal()
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
