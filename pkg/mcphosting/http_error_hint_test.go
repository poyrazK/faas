package mcphosting

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestMCP401HintNamesTokenEnv pins Hunt 32 H32-28: a 401 from an MCP server
// got the CLI's generic "Run gregale login" hint, which does not help.
func TestMCP401HintNamesTokenEnv(t *testing.T) {
	err := httpResponseError(&http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{}})
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Problem.Hint, "--token-env") {
		t.Fatalf("401 problem = %+v, want a --token-env hint", err)
	}
}
