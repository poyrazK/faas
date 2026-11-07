package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/onebox-faas/faas/guest/runners/local"
)

// gregale dev --local (ADR-20261107210000).
var MatchFunctionRoute = matchFunctionRoute

const (
	DevLocalTokenHeader = devLocalTokenHeader
	DevLocalTriggerPath = devLocalTriggerPath
)

// DevLocalServerForTest plans and prepares the project's functions and
// returns the local server's handler and its trigger token.
func DevLocalServerForTest(t *testing.T, projectDir string) (http.Handler, string, error) {
	t.Helper()
	fns, err := planLocalFunctions(projectDir)
	if err != nil {
		return nil, "", err
	}
	cache := t.TempDir()
	for _, fn := range fns {
		if err := fn.prepare(context.Background(), cache, os.Environ()); err != nil {
			return nil, "", err
		}
	}
	srv, err := newDevLocalServer(fns, projectDir)
	if err != nil {
		return nil, "", err
	}
	srv.out = io.Discard
	srv.port = devLocalDefaultPort
	t.Cleanup(func() {
		for _, fn := range fns {
			local.Reload(fn.proc)
		}
	})
	return srv, srv.token, nil
}
