// adr: 20261122191423
package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// destroyFake serves a project with a web app (owns bucket "assets"), a
// worker app, a database bound to both and a shared database also bound to
// an app outside the project.
type destroyFake struct {
	mu       sync.Mutex
	requests []string
	bucket   int  // status for DELETE bucket
	full     bool // bucket "assets" holds an object
}

func (f *destroyFake) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	switch r.Method + " " + r.URL.Path {
	case "GET /v1/projects/assetforge/delete-preview", "GET /v1/projects/assetforge":
		main.WriteJSONTest(w, api.ProjectDeletePreviewResponse{Project: api.ProjectSummaryResponse{Slug: "assetforge"},
			Workloads: []api.ProjectWorkloadResponse{{Slug: "assetforge-web"}, {Slug: "assetforge-worker"}}})
	case "GET /v1/apps/assetforge-web":
		main.WriteJSONTest(w, api.AppResponse{ID: "app-web", Slug: "assetforge-web"})
	case "GET /v1/apps/assetforge-worker":
		main.WriteJSONTest(w, api.AppResponse{ID: "app-worker", Slug: "assetforge-worker"})
	case "GET /v1/apps/assetforge-web/buckets":
		main.WriteJSONTest(w, api.ObjectBucketList{Items: []api.ObjectBucket{{ID: "bkt-1", Name: "assets"}}})
	case "GET /v1/apps/assetforge-worker/buckets":
		main.WriteJSONTest(w, api.ObjectBucketList{})
	case "GET /v1/apps/assetforge-web/buckets/bkt-1/objects/versions":
		list := api.ObjectVersionList{Items: []api.ObjectVersion{}}
		if f.full {
			list.Items = append(list.Items, api.ObjectVersion{Key: "rooms/1.png", VersionID: "v1", IsLatest: true})
		}
		main.WriteJSONTest(w, list)
	case "GET /v1/apps/assetforge-web/postgres-bindings", "GET /v1/apps/assetforge-worker/postgres-bindings":
		main.WriteJSONTest(w, api.AppPostgresBindingList{Items: []api.AppPostgresBinding{{Database: "main"}, {Database: "shared"}}})
	case "GET /v1/postgres/databases":
		main.WriteJSONTest(w, api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: "db-main", Name: "main"}, {ID: "db-shared", Name: "shared"}, {ID: "db-other", Name: "other"}}})
	case "GET /v1/postgres/databases/db-main/bindings":
		main.WriteJSONTest(w, api.ManagedPostgresBindingList{Items: []api.ManagedPostgresBinding{{AppID: "app-web"}, {AppID: "app-worker"}}})
	case "GET /v1/postgres/databases/db-shared/bindings":
		main.WriteJSONTest(w, api.ManagedPostgresBindingList{Items: []api.ManagedPostgresBinding{{AppID: "app-web"}, {AppID: "app-elsewhere"}}})
	case "DELETE /v1/apps/assetforge-web/buckets/bkt-1":
		if f.bucket != 0 {
			w.WriteHeader(f.bucket)
			main.WriteJSONTest(w, api.Problem{Status: f.bucket, Code: "bucket_not_empty", Title: "Bucket not empty"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "DELETE /v1/apps/assetforge-web", "DELETE /v1/apps/assetforge-worker", "DELETE /v1/projects/assetforge":
		w.WriteHeader(http.StatusNoContent)
	case "DELETE /v1/postgres/databases/db-main":
		main.WriteJSONTest(w, api.ManagedPostgresDatabase{ID: "db-main", Name: "main"})
	default:
		w.WriteHeader(http.StatusNotFound)
		main.WriteJSONTest(w, api.Problem{Status: 404, Code: "not_found", Title: "no route " + r.Method + " " + r.URL.Path})
	}
}

func (f *destroyFake) deletes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		if strings.HasPrefix(r, "DELETE ") {
			out = append(out, r)
		}
	}
	return out
}

func runDestroy(t *testing.T, f *destroyFake, args ...string) int {
	t.Helper()
	main.ResetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	return main.CmdProjectsDestroy(args)
}

func TestProjectDestroyKeepsDataByDefault(t *testing.T) {
	f := &destroyFake{}
	if code := runDestroy(t, f, "assetforge", "--yes"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	// The web app owns a bucket, so it and the project stay; no database is
	// touched.
	if got, want := f.deletes(), []string{"DELETE /v1/apps/assetforge-worker"}; !slices.Equal(got, want) {
		t.Fatalf("deletes %v, want %v", got, want)
	}
}

func TestProjectDestroyIncludeDataDeletesOwnedDataOnly(t *testing.T) {
	f := &destroyFake{}
	if code := runDestroy(t, f, "assetforge", "--yes", "--include-data"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := []string{
		"DELETE /v1/apps/assetforge-web/buckets/bkt-1",
		"DELETE /v1/apps/assetforge-web",
		"DELETE /v1/apps/assetforge-worker",
		"DELETE /v1/postgres/databases/db-main", // db-shared is bound outside the project
		"DELETE /v1/projects/assetforge",
	}
	if got := f.deletes(); !slices.Equal(got, want) {
		t.Fatalf("deletes %v, want %v", got, want)
	}
}

func TestProjectDestroyRefusedBucketKeepsItsAppAndTheProject(t *testing.T) {
	f := &destroyFake{bucket: http.StatusConflict}
	if code := runDestroy(t, f, "assetforge", "--yes", "--include-data"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	for _, kept := range []string{"DELETE /v1/apps/assetforge-web", "DELETE /v1/projects/assetforge"} {
		if slices.Contains(f.deletes(), kept) {
			t.Fatalf("%s ran after the bucket was refused: %v", kept, f.deletes())
		}
	}
}

// Gregale deletes only empty buckets, so a bucket holding objects is kept by
// the plan, with how to empty it, instead of failing every run.
func TestProjectDestroyPlansToKeepABucketWithObjects(t *testing.T) {
	f := &destroyFake{full: true}
	var stdout bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &stdout
	t.Cleanup(func() { *main.OsStdout = old })
	if code := runDestroy(t, f, "assetforge", "--yes", "--include-data"); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if got, want := f.deletes(), []string{"DELETE /v1/apps/assetforge-worker"}; !slices.Equal(got, want) {
		t.Fatalf("deletes %v, want %v", got, want)
	}
	if out := stdout.String(); !strings.Contains(out, "assets still hold objects") || !strings.Contains(out, "gregale bucket deletion create") {
		t.Fatalf("plan does not say how to empty the bucket:\n%s", out)
	}
}

// A refused bucket keeps the web app, so the database it is bound to stays
// too, though the plan had chosen to delete it.
func TestProjectDestroyKeepsDatabaseOfAKeptApp(t *testing.T) {
	f := &destroyFake{bucket: http.StatusConflict}
	runDestroy(t, f, "assetforge", "--yes", "--include-data")
	if slices.Contains(f.deletes(), "DELETE /v1/postgres/databases/db-main") {
		t.Fatalf("database of a kept app deleted: %v", f.deletes())
	}
}

func TestProjectDestroyDryRunChangesNothing(t *testing.T) {
	f := &destroyFake{}
	if code := runDestroy(t, f, "assetforge", "--dry-run", "--include-data"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := f.deletes(); len(got) != 0 {
		t.Fatalf("dry run deleted %v", got)
	}
	if code := runDestroy(t, f, "assetforge"); code != 1 {
		t.Fatalf("no mode: exit %d, want 1", code)
	}
}
