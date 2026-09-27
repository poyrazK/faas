package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProjectReleaseReadClientPaths(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		paths = append(paths, r.URL.RequestURI())
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "token")
	ctx := context.Background()
	if _, err := client.GetActiveProjectReleaseSet(ctx, "my project", "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetProjectReleaseSet(ctx, "my project", "staging", "release/id"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListProjectReleaseSets(ctx, "my project", "staging", "a+b=", 2); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/v1/projects/my%20project/environments/staging/release-sets/active",
		"/v1/projects/my%20project/environments/staging/release-sets/release%2Fid",
		"/v1/projects/my%20project/environments/staging/release-sets?before=a%2Bb%3D&limit=2",
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}
