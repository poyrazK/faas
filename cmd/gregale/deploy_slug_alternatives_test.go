package main_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// slugOwnersServer fakes apid app ownership: mine are the caller's apps,
// others belong to another account (GET answers 404, POST 409).
func slugOwnersServer(t *testing.T, mine, others map[string]bool) (*httptest.Server, *[]string) {
	t.Helper()
	return slugOwnersServerWithDeleted(t, mine, others, nil)
}

// slugOwnersServerWithDeleted is slugOwnersServer where deleted are slugs the
// caller's own deleted apps still hold (GET 404, POST 409, listed in
// /v1/deleted-apps).
func slugOwnersServerWithDeleted(t *testing.T, mine, others map[string]bool, deleted []string) (*httptest.Server, *[]string) {
	t.Helper()
	deletedApps := []api.DeletedAppSummary{}
	for _, slug := range deleted {
		others[slug] = true
		deletedApps = append(deletedApps, api.DeletedAppSummary{ID: "del-" + slug, Slug: slug, RestoreEligible: true})
	}
	var mu sync.Mutex
	var created []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		writeApp := func(slug string) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "id-" + slug, Slug: slug})
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"acct-1","email":"x@example.com","plan":"free","status":"active"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/deleted-apps":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"apps": deletedApps})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps":
			var req api.CreateAppRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if mine[req.Slug] || others[req.Slug] {
				api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Slug taken", "taken"))
				return
			}
			mine[req.Slug] = true
			created = append(created, req.Slug)
			writeApp(req.Slug)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/apps/"):
			slug := strings.TrimPrefix(r.URL.Path, "/v1/apps/")
			if !mine[slug] {
				api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", ""))
				return
			}
			writeApp(slug)
		case strings.HasPrefix(r.URL.Path, "/v1/apps/"):
			writeApp(strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/apps/"), "/")[0])
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", ""))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &created
}

func TestDeployTakesSuffixedNameWhenAnotherOwnerHoldsIt(t *testing.T) {
	ctx := context.Background()
	mine, others := map[string]bool{}, map[string]bool{"shop": true}
	srv, created := slugOwnersServer(t, mine, others)
	client := main.NewClient(srv.URL, "fp_live_x")
	want := api.SuffixedAppSlugs("acct-1", "shop", 3)[0]

	got, err := main.DeployAppWithAlternatives(ctx, client, "shop", "")
	if err != nil || got != want {
		t.Fatalf("first deploy = %q, %v; want %q", got, err, want)
	}
	// A fresh checkout (no link) deploys again: same app, nothing new created.
	got, err = main.DeployAppWithAlternatives(ctx, client, "shop", "")
	if err != nil || got != want || len(*created) != 1 {
		t.Fatalf("redeploy = %q, %v, created %v; want %q and one create", got, err, *created, want)
	}
}

// A deploy to an app the caller already has under the name makes no extra
// calls: no account lookup, no alternative probes.
func TestDeployOwnAppMakesNoAlternativeCalls(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "shop"})
	}))
	defer srv.Close()
	got, err := main.DeployAppWithAlternatives(context.Background(), main.NewClient(srv.URL, "fp_live_x"), "shop", "")
	if err != nil || got != "shop" {
		t.Fatalf("got %q, %v; want shop", got, err)
	}
	if len(paths) == 0 || paths[0] != "GET /v1/apps/shop" {
		t.Fatalf("calls %v; want the lookup of shop first", paths)
	}
	for _, path := range paths {
		if path == "GET /v1/account" {
			t.Fatalf("calls %v; want no account lookup", paths)
		}
	}
}

// A new name the caller holds no suffixed app for is created under the name.
func TestDeployFreeNameCreatesIt(t *testing.T) {
	srv, created := slugOwnersServer(t, map[string]bool{}, map[string]bool{})
	got, err := main.DeployAppWithAlternatives(context.Background(), main.NewClient(srv.URL, "fp_live_x"), "shop", "")
	if err != nil || got != "shop" || strings.Join(*created, ",") != "shop" {
		t.Fatalf("got %q, %v, created %v; want shop created", got, err, *created)
	}
}

// An earlier deploy took a suffixed name while another account held shop;
// that account has since let it go. A fresh checkout (no link) keeps
// deploying to the suffixed app instead of creating a second app as shop.
func TestDeployKeepsSuffixedAppAfterNameFrees(t *testing.T) {
	alt := api.SuffixedAppSlugs("acct-1", "shop", 3)[0]
	srv, created := slugOwnersServer(t, map[string]bool{alt: true}, map[string]bool{})
	got, err := main.DeployAppWithAlternatives(context.Background(), main.NewClient(srv.URL, "fp_live_x"), "shop", "")
	if err != nil || got != alt || len(*created) != 0 {
		t.Fatalf("got %q, %v, created %v; want %q and no create", got, err, *created, alt)
	}
}

// The caller deleted their suffixed app; it holds its slug for the restore
// window. The next deploy of the name takes the next alternative instead of
// failing on the deleted one.
func TestDeploySkipsOwnDeletedAlternative(t *testing.T) {
	alts := api.SuffixedAppSlugs("acct-1", "shop", 3)
	srv, created := slugOwnersServerWithDeleted(t, map[string]bool{}, map[string]bool{"shop": true}, []string{alts[0]})
	got, err := main.DeployAppWithAlternatives(context.Background(), main.NewClient(srv.URL, "fp_live_x"), "shop", "")
	if err != nil || got != alts[1] || len(*created) != 1 {
		t.Fatalf("got %q, %v, created %v; want %q", got, err, *created, alts[1])
	}
}

// The name asked for itself held by the caller's own deleted app keeps the
// restore hint: no suffix.
func TestDeployOwnDeletedNameKeepsRestoreHint(t *testing.T) {
	srv, created := slugOwnersServerWithDeleted(t, map[string]bool{}, map[string]bool{}, []string{"shop"})
	_, err := main.DeployAppWithAlternatives(context.Background(), main.NewClient(srv.URL, "fp_live_x"), "shop", "")
	if err == nil || !strings.Contains(err.Error(), "app restore shop") || len(*created) != 0 {
		t.Fatalf("err %v, created %v; want the restore hint and no create", err, *created)
	}
}

func TestDeploySkipsSquattedAlternative(t *testing.T) {
	alts := api.SuffixedAppSlugs("acct-1", "shop", 3)
	srv, _ := slugOwnersServer(t, map[string]bool{}, map[string]bool{"shop": true, alts[0]: true})
	got, err := main.DeployAppWithAlternatives(context.Background(), main.NewClient(srv.URL, "fp_live_x"), "shop", "")
	if err != nil || got != alts[1] {
		t.Fatalf("got %q, %v; want %q", got, err, alts[1])
	}
}

func TestDeployOwnNameIsNotSuffixed(t *testing.T) {
	srv, created := slugOwnersServer(t, map[string]bool{"shop": true}, map[string]bool{})
	got, err := main.DeployAppWithAlternatives(context.Background(), main.NewClient(srv.URL, "fp_live_x"), "shop", "")
	if err != nil || got != "shop" || len(*created) != 0 {
		t.Fatalf("got %q, %v, created %v; want shop, no create", got, err, *created)
	}
}

// gregale.yaml that names the app keeps the old error: a suffixed app would
// no longer match its triggers and routes.
func TestDeployManifestNamingAppKeepsConflictError(t *testing.T) {
	dir := t.TempDir()
	manifest := "triggers:\n  - kind: cron\n    app: shop\n    schedule: \"*/5 * * * *\"\n    path: /tick\n"
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, created := slugOwnersServer(t, map[string]bool{}, map[string]bool{"shop": true})
	_, err := main.DeployAppWithAlternatives(context.Background(), main.NewClient(srv.URL, "fp_live_x"), "shop", dir)
	if err == nil || !strings.Contains(err.Error(), `slug "shop" is already in use`) || len(*created) != 0 {
		t.Fatalf("err %v, created %v; want the in-use error and no create", err, *created)
	}
}
