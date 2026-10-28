package apidserver

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
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
	if input.Purpose == "" && len(input.Scopes) == 0 {
		return mintCliAuthCodeRaw()
	}
	prefix := "MCPR"
	switch input.Purpose {
	case api.CliAuthPurposeMCPRead:
	case api.CliAuthPurposeMCPWrite:
		prefix = "MCPW"
	case api.CliAuthPurposeMCP:
		bits, ok := mcpScopeBits(input.Scopes)
		if !ok {
			return "", nil, errBrowserAuthInput
		}
		// One hex digit names the scope set (ADR-20261026195952).
		prefix = "MCP" + strings.ToUpper(strconv.FormatUint(uint64(bits), 16))
	default:
		return "", nil, errBrowserAuthInput
	}
	if input.Purpose != api.CliAuthPurposeMCP && len(input.Scopes) != 0 {
		return "", nil, errBrowserAuthInput
	}
	entropy := make([]byte, 32)
	if _, err := rand.Read(entropy); err != nil {
		return "", nil, err
	}
	return prefix + "-" + hex.EncodeToString(entropy), api.HashToken(append([]byte(prefix+":"), entropy...)), nil
}

// mcpScopeBits maps a requested scope set to the bits of the code's scope
// digit; every scope must be grantable and appear once.
func mcpScopeBits(scopes []string) (uint8, bool) {
	var bits uint8
	for _, scope := range scopes {
		i := slices.Index(api.MCPGrantableScopes, scope)
		if i < 0 || bits&(1<<i) != 0 {
			return 0, false
		}
		bits |= 1 << i
	}
	return bits, true
}

func isMCPAuthCode(raw string) bool {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(raw), "-", ""))
	return len(normalized) == 68 && strings.HasPrefix(normalized, "MCP") && strings.ContainsRune("RW0123456789ABCDEF", rune(normalized[3]))
}

// mcpCodeScopes are the scopes a valid MCP code's purpose digit grants.
func mcpCodeScopes(raw string) []string {
	scopes := []string{api.ScopeAppsRead}
	switch digit := strings.ToUpper(strings.TrimSpace(raw))[3]; digit {
	case 'R':
	case 'W':
		scopes = append(scopes, api.ScopeDeployWrite)
	default:
		bits, _ := strconv.ParseUint(string(digit), 16, 8)
		for i, scope := range api.MCPGrantableScopes {
			if bits&(1<<i) == 0 {
				continue
			}
			scopes = append(scopes, scope)
			if scope == api.ScopeManagedPostgresManage {
				scopes = append(scopes, api.ScopeManagedPostgresRead)
			}
		}
	}
	return scopes
}

func browserAuthCredential(raw string) (string, []string, time.Duration) {
	if !isMCPAuthCode(raw) {
		return "cli-login", api.ScopesAdminOnly, cliAuthKeyTTL
	}
	// The key slides while used (ADR-20261028205920; TouchKeyLastUsed).
	return api.MCPLoginKeyLabel, mcpCodeScopes(raw), api.MCPLoginKeyIdle
}

// mcpCapabilities describes, for the consent page, what an MCP code grants
// beyond inspection.
func mcpCapabilities(raw string) []string {
	described := map[string]string{
		api.ScopeDeployWrite:           "deploy, promote and roll back apps, and add custom domains and crons",
		api.ScopeSecretsRequest:        "ask you to enter secrets on a Gregale page; it never sees their values",
		api.ScopeStorageManage:         "create object storage buckets and bind them to apps",
		api.ScopeManagedPostgresManage: "create managed PostgreSQL databases and bind them to apps (billed)",
	}
	var out []string
	for _, scope := range mcpCodeScopes(raw) {
		if text, ok := described[scope]; ok {
			out = append(out, text)
		}
	}
	return out
}

// Preserve the existing CLI audit contract while distinguishing MCP consent.
func browserAuthAuditMethod(raw string) string {
	if isMCPAuthCode(raw) {
		return "mcp-login"
	}
	return "cli_code"
}
