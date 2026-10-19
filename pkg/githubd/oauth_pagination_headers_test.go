// adr: 012
package githubd

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestListInstallableReposReadsEveryLinkField(t *testing.T) {
	for _, secondPageFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("second-page-fails=%v", secondPageFails), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Query().Get("page") == "2" {
					if secondPageFails {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					_, _ = io.WriteString(w, `{"repositories":[{"id":2,"full_name":"acme/second"}]}`)
					return
				}
				w.Header().Add("Link", `<https://api.github.com/installation/repositories?page=1>; rel="first"`)
				w.Header().Add("Link", `<https://api.github.com/installation/repositories?page=2>; rel="next"`)
				_, _ = io.WriteString(w, `{"repositories":[{"id":1,"full_name":"acme/first"}]}`)
			}))
			t.Cleanup(server.Close)
			auth := &AppAuth{HTTPClient: &singleHostClient{base: server.Client(), api: server.URL}}
			repos, err := auth.ListInstallableRepos(t.Context(), "test-token", 5)
			if secondPageFails {
				// Installation sync uses a successful list to detach absent
				// repositories, so an incomplete list must never look complete.
				if err == nil || repos != nil {
					t.Errorf("incomplete list returned repos=%v, err=%v", repos, err)
				}
			} else if err != nil || len(repos) != 2 || repos[1].FullName != "acme/second" {
				t.Errorf("paginated list returned repos=%v, err=%v; want both repositories", repos, err)
			}
			if calls.Load() != 2 {
				t.Errorf("page requests = %d, want 2", calls.Load())
			}
		})
	}
}
