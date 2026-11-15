package gateway

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// internalSvcTokenApp returns the app_id claim of a compact JWS that the
// verifier already accepted, or "" when it has none. The signature is not
// checked again here.
func internalSvcTokenApp(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		AppID string `json:"app_id"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.AppID
}

// internalSvcTokenForOtherApp reports a verified token minted for another
// app (ADR-20261110210000 slice 3k): a token schedd or secretd minted names
// the app it dispatches to, so it opens that app only. A token without an
// app_id (another minter) is unchanged.
func internalSvcTokenForOtherApp(token, appID string) bool {
	claim := internalSvcTokenApp(token)
	return claim != "" && claim != appID
}
