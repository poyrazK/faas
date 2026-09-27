package githubd

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/reposcan"
)

type stubBranchHeads struct {
	sha string
	err error
}

func (s stubBranchHeads) BranchHead(context.Context, int64, string, string) (string, error) {
	return s.sha, s.err
}

func TestHTTPBranchHeads(t *testing.T) {
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/repos/octo/api/branches/release%2Fcanary" ||
			r.Header.Get("Authorization") != "Bearer installation-token" {
			t.Errorf("branch request = %s, auth = %q", r.URL.EscapedPath(), r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"commit":{"sha":"`+sha+`"}}`)
	}))
	defer srv.Close()
	tokens := NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) {
		return "installation-token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	client := NewHTTPBranchHeads(tokens, &singleHostClient{base: srv.Client(), api: srv.URL})
	got, err := client.BranchHead(context.Background(), 42, "octo/api", "release/canary")
	if err != nil || got != sha {
		t.Fatalf("branch head = (%q, %v), want %q", got, err, sha)
	}
}

func TestHTTPBranchHeadsFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"upstream failure", http.StatusServiceUnavailable, `{}`},
		{"missing commit", http.StatusOK, `{}`},
		{"invalid response", http.StatusOK, `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			tokens := NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) {
				return "installation-token", time.Now().Add(time.Hour), nil
			}), time.Minute)
			client := NewHTTPBranchHeads(tokens, &singleHostClient{base: srv.Client(), api: srv.URL})
			if _, err := client.BranchHead(context.Background(), 42, "octo/api", "main"); !errors.Is(err, ErrBranchHeadUnavailable) {
				t.Fatalf("branch head error = %v, want unavailable", err)
			}
		})
	}
}

func TestHandlePushRequestChecksCurrentBranchBeforeFetch(t *testing.T) {
	rig := newRig(t, func(fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	const eventSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	body := []byte(`{"ref":"refs/heads/main","after":"` + eventSHA + `","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	for _, tc := range []struct {
		name string
		head stubBranchHeads
		want error
	}{
		{"superseded", stubBranchHeads{sha: strings.Repeat("b", 40)}, ErrIgnored},
		{"lookup failure", stubBranchHeads{err: ErrBranchHeadUnavailable}, ErrBranchHeadUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newServiceForRig(t, rig)
			svc.BranchHeads = tc.head
			svc.Source = &stubSource{err: errors.New("source fetch must not run")}
			_, err := svc.HandlePushRequest(context.Background(), body)
			if !errors.Is(err, tc.want) {
				t.Fatalf("push error = %v, want %v", err, tc.want)
			}
		})
	}
	svc := newServiceForRig(t, rig)
	svc.BranchHeads = stubBranchHeads{sha: eventSHA}
	svc.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := svc.HandlePushRequest(context.Background(), body); err != nil {
		t.Fatalf("current head push: %v", err)
	}
}
