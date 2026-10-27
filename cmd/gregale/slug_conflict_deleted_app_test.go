package main_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Hunt 14 H14-93: a deploy under the slug of the caller's own deleted app
// failed with "already in use; pick a different --name", with no word that
// the app could be restored.
func TestSlugConflictNamesOwnDeletedApp(t *testing.T) {
	grace := time.Date(2026, 11, 24, 8, 10, 0, 0, time.UTC)
	cases := []struct {
		name      string
		deleted   string // GET /v1/deleted-apps body; empty answers 404
		want      []string
		forbidden string
	}{
		{name: "restorable",
			deleted: `{"apps":[{"id":"a1","slug":"other"},{"id":"a2","slug":"taken","restore_eligible":true,"delete_grace_until":"` + grace.Format(time.RFC3339) + `"}]}`,
			want:    []string{`"taken"`, "an app you deleted", "gregale apps restore taken", "2026-11-24 08:10 UTC"}},
		{name: "not restorable",
			deleted:   `{"apps":[{"id":"a2","slug":"taken","restore_eligible":false,"restore_blocker":"plan_quota"}]}`,
			want:      []string{"held by an app you deleted until it is purged", "pick a different --name"},
			forbidden: "apps restore"},
		{name: "another account", deleted: `{"apps":[]}`,
			want: []string{`slug "taken" is already in use`}, forbidden: "deleted"},
		{name: "lookup fails", want: []string{`slug "taken" is already in use`}, forbidden: "deleted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps":
					api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Slug taken", `app slug "taken" is already in use`))
				case r.Method == http.MethodGet && r.URL.Path == "/v1/deleted-apps" && tc.deleted != "":
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, tc.deleted)
				default:
					api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", ""))
				}
			}))
			defer srv.Close()
			client := main.NewClient(srv.URL, "fp_live_x")
			for site, err := range map[string]error{
				"createOrFetchApp":   main.CreateOrFetchApp(context.Background(), client, api.CreateAppRequest{Slug: "taken"}, nil, nil, nil),
				"ensureSourceRefApp": func() error { _, err := main.EnsureSourceRefApp(context.Background(), client, "taken"); return err }(),
			} {
				if err == nil {
					t.Fatalf("%s: want an error", site)
				}
				for _, want := range tc.want {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("%s: error %q lacks %q", site, err, want)
					}
				}
				if tc.forbidden != "" && strings.Contains(err.Error(), tc.forbidden) {
					t.Errorf("%s: error %q must not mention %q", site, err, tc.forbidden)
				}
			}
		})
	}
}

// Hunt 14 H14-94: `app scale --min 1` on a parked app printed a cost echo
// and nothing else, while the parked app kept no instance (the floor holds
// only after a real wake, H5-54).
func TestAppScaleMinOnParkedAppSaysWakeStartsFloor(t *testing.T) {
	for _, status := range []string{"evicted_cold", "active"} {
		t.Run(status, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/v1/apps/demo") {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "demo", Status: status, MinInstances: 1, RAMMB: 512})
					return
				}
				api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", ""))
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			stdout, readStderr, restore := main.SwapIO(t)
			code := main.Run([]string{"app", "demo", "scale", "--min", "1"})
			restore()
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, readStderr())
			}
			hasNote := strings.Contains(stdout.String(), "Run gregale wake demo to start it now")
			if hasNote != (status == "evicted_cold") {
				t.Fatalf("status %s: wake note present = %v, stdout = %s", status, hasNote, stdout)
			}
		})
	}
}
