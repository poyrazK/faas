package main_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A PR preview slug is pr-<N>- plus a parent slug of up to 40 characters.
func TestValidCLISlugAcceptsLongPreviewSlugs(t *testing.T) {
	parent := strings.Repeat("a", 40)
	for slug, want := range map[string]bool{
		"pr-12345-" + parent:              true,
		parent:                            true,
		parent + "b":                      false,
		"pr-x-" + parent:                  false,
		"pr-1-" + strings.Repeat("a", 60): false,
	} {
		if got := main.ValidCLISlug(slug); got != want {
			t.Errorf("ValidCLISlug(%q) = %v, want %v", slug, got, want)
		}
	}
}

func previewReportServer(t *testing.T, preview api.PreviewResourceResponse, environment *api.PreviewEnvironmentStatusResponse) *api.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/environment"):
			if environment == nil {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"preview_environment_not_found","status":404}`))
				return
			}
			_ = json.NewEncoder(w).Encode(environment)
		case strings.HasPrefix(r.URL.Path, "/v1/preview/"):
			_ = json.NewEncoder(w).Encode(preview)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404}`))
		}
	}))
	t.Cleanup(srv.Close)
	return api.NewClient(srv.URL, "token")
}

func TestPreviewReportExplainsAServiceWithoutProduction(t *testing.T) {
	client := previewReportServer(t, api.PreviewResourceResponse{
		App: api.AppResponse{Slug: "pr-42-ledger", PreviewOfSlug: "ledger", PreviewPRNumber: 42},
	}, nil)
	if _, err := main.CollectPreviewRouteReport(t.Context(), client, "pr-42-ledger", "", "24h"); !errors.Is(err, main.ErrPreviewNoProductionParent) {
		t.Fatalf("report on a new service = %v, want the no-production-parent explanation", err)
	}
}

func TestPreviewReportNamesTheWholeEnvironment(t *testing.T) {
	client := previewReportServer(t, api.PreviewResourceResponse{}, &api.PreviewEnvironmentStatusResponse{
		RootSlug: "pr-42-web", PRNumber: 42, CommitSHA: strings.Repeat("a", 40),
		Members: []api.PreviewEnvironmentMemberResponse{{Slug: "pr-42-web"}, {Slug: "pr-42-ledger"}},
	})
	note, ok := main.PreviewEnvironmentReviewNote(t.Context(), client, "pr-42-web")
	if !ok || !strings.Contains(note, "gregale preview review pr-42-web pr-42-ledger") {
		t.Fatalf("note = (%q, %v), want the review command for every workload", note, ok)
	}
	if _, ok := main.PreviewEnvironmentReviewNote(t.Context(), client, "pr-42-ledger"); ok {
		t.Fatal("a non-root slug produced an environment note")
	}
}
