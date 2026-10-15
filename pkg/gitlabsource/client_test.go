package gitlabsource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPinnedGitLabArchiveRejectsIdentityAndRedirectChanges(t *testing.T) {
	sha := strings.Repeat("a", 40)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer private-token" {
			t.Errorf("missing bearer authorization")
		}
		switch r.URL.Path {
		case "/api/v4/projects/team%2Fservice":
			// URL.Path is decoded by net/http; this branch is not expected.
			t.Fatal("unexpected encoded path")
		case "/api/v4/projects/team/service":
			fmt.Fprint(w, `{"id":41,"path_with_namespace":"team/service","visibility":"private"}`)
		case "/api/v4/projects/41/repository/branches/main":
			fmt.Fprintf(w, `{"commit":{"id":%q}}`, sha)
		case "/api/v4/projects/41/repository/archive.tar.gz":
			if r.URL.Query().Get("sha") != sha || r.URL.Query().Get("include_lfs_blobs") != "false" {
				t.Errorf("mutable or incomplete archive request: %s", r.URL.String())
			}
			fmt.Fprint(w, "pinned archive")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := Client{APIURL: server.URL + "/api/v4", HTTP: server.Client()}
	repository, err := client.VerifyRepository(context.Background(), "private-token", "team/service", 41)
	if err != nil || repository.ID != 41 {
		t.Fatalf("verified repository = %+v, %v", repository, err)
	}
	resolved, err := client.ResolveCommit(context.Background(), "private-token", 41, "refs/heads/main")
	if err != nil || resolved != sha {
		t.Fatalf("resolved commit = %q, %v", resolved, err)
	}
	archive, err := client.Archive(context.Background(), "private-token", 41, resolved, 1024)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(archive)
	archive.Close()
	if err != nil || string(body) != "pinned archive" {
		t.Fatalf("archive = %q, %v", body, err)
	}
	for _, path := range []string{"team/../service", "team%2Fservice", "https://gitlab.com/team/service", "team/service?x=1"} {
		if _, err := client.VerifyRepository(context.Background(), "private-token", path, 0); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("unsafe path %q: %v", path, err)
		}
	}
	if _, err := client.VerifyRepository(context.Background(), "private-token", "team/service", 99); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("repository replacement accepted: %v", err)
	}
	if _, err := client.Archive(context.Background(), "private-token", 41, "main", 1024); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mutable archive accepted: %v", err)
	}
	if requests != 4 {
		t.Fatalf("requests = %d, want 4", requests)
	}
}

func TestGitLabClientDoesNotForwardBearerOnRedirect(t *testing.T) {
	received := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { received = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	client := Client{APIURL: source.URL + "/api/v4"}
	if _, err := client.VerifyRepository(context.Background(), "private-token", "team/service", 0); !errors.Is(err, ErrUnavailable) || received {
		t.Fatalf("redirect followed: err=%v received=%t", err, received)
	}
}
