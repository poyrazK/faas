package api

// adr: 202610171905

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOrgProjectClientKeepsTenantInPath(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer team-token" {
			t.Error("organization credential missing")
		}
		paths = append(paths, r.URL.RequestURI())
		if r.Method == http.MethodPost {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			defer func() {
				if r.MultipartForm != nil {
					_ = r.MultipartForm.RemoveAll()
				}
			}()
			if r.FormValue("project_slug") != "shop" {
				t.Error("project upload changed its addressed slug")
			}
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "team-token")
	ctx := t.Context()
	if _, err := client.GetOrgProject(ctx, "my company", "shop"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetActiveOrgProjectReleaseSet(ctx, "my company", "shop", "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetOrgProjectReleaseSet(ctx, "my company", "shop", "staging", "release/id"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListOrgProjectReleaseSets(ctx, "my company", "shop", "staging", "a+b=", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ScanOrgProjectDeployment(ctx, "my company", strings.NewReader("source"), "source.tar.gz", "shop", "", "", 0, nil, nil, false, false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeployOrgProject(ctx, "my company", "plan+token", strings.NewReader("source"), "source.tar.gz", "shop", "", "", 0, nil, nil, false, false, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetProject(ctx, "personal-shop"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/v1/orgs/my%20company/projects/shop",
		"/v1/orgs/my%20company/projects/shop/environments/staging/release-sets/active",
		"/v1/orgs/my%20company/projects/shop/environments/staging/release-sets/release%2Fid",
		"/v1/orgs/my%20company/projects/shop/environments/staging/release-sets?before=a%2Bb%3D&limit=2",
		"/v1/orgs/my%20company/projects/shop/scan",
		"/v1/orgs/my%20company/projects/shop/deploy?plan_token=plan%2Btoken",
		"/v1/projects/personal-shop",
	}
	if len(paths) != len(want) {
		t.Fatalf("requests=%v", paths)
	}
	for i, path := range paths {
		if path != want[i] {
			t.Errorf("request %d path=%q want=%q", i, path, want[i])
		}
	}
}
