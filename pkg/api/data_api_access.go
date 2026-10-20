package api

import (
	"encoding/hex"
	"errors"
	"regexp"
)

// DataAPIAccessConfig is what a Data API runtime reports about access
// (ADR-20261016151947, ADR-20261019102108): its exposed schemas, the issuer
// whose user tokens it accepts, its public-read allowlist and server-key
// hashes with their permissions. apid hands it to the realtime endpoints of
// the same app, so live reads follow the runtime's rules. It holds no secret:
// only SHA-256 hashes of keys.
type DataAPIAccessConfig struct {
	Version    int                `json:"version"`
	Schemas    []string           `json:"schemas"`
	Auth       *DataAPIUserAuth   `json:"auth,omitempty"`
	PublicRead []string           `json:"public_read,omitempty"`
	ServerKeys []DataAPIServerKey `json:"server_keys,omitempty"`
}

// DataAPIUserAuth is the issuer, JWKS and audience a Data API verifies user
// tokens against. realtimed verifies live reads' user tokens the same way.
type DataAPIUserAuth struct {
	Issuer   string `json:"issuer"`
	JWKSURL  string `json:"jwks_url"`
	Audience string `json:"audience"`
}

// DataAPITokenAlgorithms are the signing algorithms the Data API runtime
// accepts for user tokens.
var DataAPITokenAlgorithms = []string{"RS256", "ES256"}

// Policy returns the OIDC settings for verifying a user token like the Data
// API does.
func (a DataAPIUserAuth) Policy() (issuer, jwksURL string, audience, algorithms []string) {
	return a.Issuer, a.JWKSURL, []string{a.Audience}, append([]string(nil), DataAPITokenAlgorithms...)
}

// DataAPIServerKey is one server-key slot: the key's SHA-256 and, when the
// key is restricted, its permissions.
type DataAPIServerKey struct {
	Slot        string                         `json:"slot"`
	SHA256      string                         `json:"sha256"`
	Permissions *DataAPIServerKeyPermissionSet `json:"permissions,omitempty"`
}

// DataAPIServerKeyPermissionSet narrows a server key. Absent fields keep full
// access; live reads use Relations and Subjects.
type DataAPIServerKeyPermissionSet struct {
	Access    string   `json:"access,omitempty"`
	Relations []string `json:"relations,omitempty"`
	RPCs      []string `json:"rpcs,omitempty"`
	Subjects  *bool    `json:"subjects,omitempty"`
}

var (
	dataAPIAccessSchema   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	dataAPIAccessRelation = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}\.[A-Za-z_][A-Za-z0-9_]{0,62}$`)
	dataAPIAccessFunction = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}\.[a-z][a-z0-9_]{0,62}$`)
)

// Validate bounds a reported access config.
func (c DataAPIAccessConfig) Validate() error {
	if c.Version != 1 || len(c.Schemas) == 0 || len(c.Schemas) > DataAPIMaxSchemas || len(c.PublicRead) > DataAPIPublicReadMaxRelations || len(c.ServerKeys) > 2 {
		return errors.New("data api access: version, schema, allowlist or key count out of bounds")
	}
	for _, s := range c.Schemas {
		if !dataAPIAccessSchema.MatchString(s) {
			return errors.New("data api access: invalid schema")
		}
	}
	if a := c.Auth; a != nil {
		issuer, jwks, audience, algorithms := a.Policy()
		if a.Audience == "" || len(a.Audience) > DataAPIAudienceMaxBytes || ValidateRealtimeAuth(RealtimeAuthModeOIDCJWT, false, issuer, jwks, audience, algorithms, nil) != nil {
			return errors.New("data api access: invalid user token settings")
		}
	}
	if !uniqueMatching(c.PublicRead, dataAPIAccessRelation) {
		return errors.New("data api access: invalid public-read entry")
	}
	slots := map[string]bool{}
	for _, k := range c.ServerKeys {
		raw, err := hex.DecodeString(k.SHA256)
		if (k.Slot != "a" && k.Slot != "b") || slots[k.Slot] || err != nil || len(raw) != 32 || hex.EncodeToString(raw) != k.SHA256 {
			return errors.New("data api access: invalid server key slot or hash")
		}
		slots[k.Slot] = true
		if p := k.Permissions; p != nil && ((p.Access != "" && p.Access != "read" && p.Access != "write") ||
			len(p.Relations) > DataAPIServerKeyPermissionsMaxEntries || len(p.RPCs) > DataAPIServerKeyPermissionsMaxEntries ||
			!uniqueMatching(p.Relations, dataAPIAccessRelation) || !uniqueMatching(p.RPCs, dataAPIAccessFunction)) {
			return errors.New("data api access: invalid server key permissions")
		}
	}
	return nil
}

func uniqueMatching(values []string, pattern *regexp.Regexp) bool {
	seen := map[string]bool{}
	for _, v := range values {
		if !pattern.MatchString(v) || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
