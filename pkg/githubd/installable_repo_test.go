// adr: 20261101160300
package githubd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// repoHTTP answers GET /repos/{owner}/{name} with one status and body.
type repoHTTP struct {
	status int
	body   string
}

func (h repoHTTP) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: h.status, Body: io.NopCloser(strings.NewReader(h.body)), Header: http.Header{}}, nil
}

// A repository the installation cannot see, or a name that now leads to
// another repository, is ErrRepositoryNotVisible; other failures are not
// (ADR-20261101160300, amendment 2).
func TestGetInstallableRepoNotVisible(t *testing.T) {
	ctx := context.Background()
	found := `{"id":4242,"full_name":"acme/shop","owner":{"id":7,"login":"acme"}}`
	for _, tc := range []struct {
		name       string
		http       repoHTTP
		notVisible bool
		wantErr    bool
	}{
		{name: "visible", http: repoHTTP{status: http.StatusOK, body: found}},
		{name: "not visible", http: repoHTTP{status: http.StatusNotFound, body: `{}`}, notVisible: true, wantErr: true},
		{name: "renamed elsewhere", http: repoHTTP{status: http.StatusOK, body: `{"id":9,"full_name":"acme/other","owner":{"id":7}}`}, notVisible: true, wantErr: true},
		{name: "provider failure", http: repoHTTP{status: http.StatusBadGateway, body: `{}`}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, err := (&AppAuth{HTTPClient: tc.http}).GetInstallableRepo(ctx, "token", "acme/shop")
			if (err != nil) != tc.wantErr || errors.Is(err, ErrRepositoryNotVisible) != tc.notVisible {
				t.Fatalf("GetInstallableRepo = %+v, %v; want error %v, not visible %v", repo, err, tc.wantErr, tc.notVisible)
			}
			if !tc.wantErr && (repo.ID != 4242 || repo.Owner.ID != 7) {
				t.Fatalf("repository = %+v, want IDs 4242 and 7", repo)
			}
		})
	}
}
