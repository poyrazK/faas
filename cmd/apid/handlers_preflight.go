package main

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/preflight"
)

// preflightGitHubTokenEnv optionally supplies a read-only GitHub token. Without
// one, upstream commit lookups use the anonymous budget of 60 calls per hour
// per source IP, which a public endpoint can exhaust. The value is never
// logged.
const preflightGitHubTokenEnv = "FAAS_PREFLIGHT_GITHUB_TOKEN"

// servePreflight answers the public "would my app run here" check. The handler
// itself lives in pkg/preflight so it is testable without standing up apid;
// this method owns only the lazy wiring.
func (s *server) servePreflight(w http.ResponseWriter, r *http.Request) {
	s.preflightOnce.Do(func() {
		// gitfetch creates and removes a temp dir per fetch beneath this
		// root, so a scratch directory is the correct home for it.
		workDir := filepath.Join(os.TempDir(), "faas-preflight")
		if err := os.MkdirAll(workDir, 0o700); err != nil {
			workDir = os.TempDir()
		}
		checker := preflight.NewChecker(workDir).WithToken(os.Getenv(preflightGitHubTokenEnv))
		s.preflightHandler = preflight.NewHandler(checker)
	})
	s.preflightHandler.ServeHTTP(w, r)
}
