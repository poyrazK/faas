// adr: 20261112160000 — neond admits only the Neon calls this backend makes:
// every request the package's tests send must match neond's allow-list.
package neon

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/neond"
)

// recordedRequests is every Neon request the package's tests sent, as
// "METHOD /path" relative to the API base.
var recordedRequests sync.Map

func recordRequests(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedRequests.Store(r.Method+" "+strings.TrimPrefix(r.URL.EscapedPath(), "/api/v2"), true)
		handler.ServeHTTP(w, r)
	})
}

func TestMain(m *testing.M) {
	code := m.Run()
	var unmatched []string
	recordedRequests.Range(func(key, _ any) bool {
		method, path, _ := strings.Cut(key.(string), " ")
		// Requests the tests send to prove the client refuses them never
		// reach neond's allow-list in production either.
		if strings.Contains(path, "..") || strings.Contains(path, "/private") {
			return true
		}
		if _, _, ok := neond.Match(method, path); !ok {
			unmatched = append(unmatched, key.(string))
		}
		return true
	})
	if len(unmatched) > 0 {
		sort.Strings(unmatched)
		fmt.Fprintf(os.Stderr, "neond allow-list (pkg/neond.Routes) misses Neon calls this backend makes:\n  %s\n", strings.Join(unmatched, "\n  "))
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
