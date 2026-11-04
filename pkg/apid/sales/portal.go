package sales

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

//go:embed web/*
var assets embed.FS

var portal = template.Must(template.ParseFS(assets, "web/index.html"))

func (s *Service) HideDisabled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !s.Enabled() && (path == "/v1/sales" || strings.HasPrefix(path, "/v1/sales/") || path == "/v1/referrals" || strings.HasPrefix(path, "/v1/referrals/") || path == "/dashboard/sales" || path == "/dashboard/referrals" || strings.HasPrefix(path, "/dashboard/referral-assets/")) {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) SalesPage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.page(w, r, acct, true)
}
func (s *Service) CustomerPage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.page(w, r, acct, false)
}

func (s *Service) page(w http.ResponseWriter, r *http.Request, acct state.Account, staff bool) {
	scope, ok := s.scope(w, r, acct, staff)
	if !ok {
		return
	}
	var buf bytes.Buffer
	if err := portal.Execute(&buf, struct {
		Staff   bool
		Manager bool
		Nonce   string
	}{staff, scope.All, httpsec.NonceFromContext(r.Context())}); err != nil {
		unavailable(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (s *Service) Asset(w http.ResponseWriter, r *http.Request) {
	if !s.Enabled() {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("file")
	if name != "portal.css" && name != "portal.js" && name != "pipeline.js" && name != "reports.js" && name != "inquiries.js" && name != "activity.js" && name != "qualification.js" && name != "templates.js" {
		http.NotFound(w, r)
		return
	}
	content, err := assets.ReadFile("web/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if name == "portal.css" {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	_, _ = w.Write(content)
}
