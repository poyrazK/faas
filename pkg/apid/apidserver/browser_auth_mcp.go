package apidserver

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The purpose prefix is part of the stored hash. Changing read to write or CLI
// changes the lookup identity, so neither browser nor exchange can widen a code.
var errBrowserAuthInput = errors.New("invalid browser authorization request")

func mintBrowserAuthCode(r *http.Request) (string, []byte, error) {
	var input api.CliAuthCodeRequest
	if r.ContentLength != 0 && r.Body != http.NoBody {
		if err := decodeJSON(r, &input); err != nil {
			return "", nil, errBrowserAuthInput
		}
	}
	if input.Purpose == "" {
		return mintCliAuthCodeRaw()
	}
	prefix := "MCPR"
	switch input.Purpose {
	case api.CliAuthPurposeMCPRead:
	case api.CliAuthPurposeMCPWrite:
		prefix = "MCPW"
	default:
		return "", nil, errBrowserAuthInput
	}
	entropy := make([]byte, 32)
	if _, err := rand.Read(entropy); err != nil {
		return "", nil, err
	}
	return prefix + "-" + hex.EncodeToString(entropy), api.HashToken(append([]byte(prefix+":"), entropy...)), nil
}

func isMCPAuthCode(raw string) bool {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(raw), "-", ""))
	return len(normalized) == 68 && (strings.HasPrefix(normalized, "MCPR") || strings.HasPrefix(normalized, "MCPW"))
}

func browserAuthCredential(raw string) (string, []string, time.Duration) {
	if !isMCPAuthCode(raw) {
		return "cli-login", api.ScopesAdminOnly, cliAuthKeyTTL
	}
	scopes := []string{api.ScopeAppsRead}
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(raw)), "MCPW") {
		scopes = append(scopes, api.ScopeDeployWrite)
	}
	return "mcp-login", scopes, 8 * time.Hour
}

// Preserve the existing CLI audit contract while distinguishing MCP consent.
func browserAuthAuditMethod(raw string) string {
	if isMCPAuthCode(raw) {
		return "mcp-login"
	}
	return "cli_code"
}
