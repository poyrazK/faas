package api

import (
	"encoding/hex"
	"errors"
	"regexp"
)

// DataAPIAccessConfig is what a Data API runtime reports about access without
// a user token (ADR-20261016151947, ADR-20261019102108): its exposed schemas,
// public-read allowlist and server-key hashes with their permissions. apid
// hands it to the realtime endpoints of the same app, so live reads follow
// the runtime's rules. It holds no secret: only SHA-256 hashes of keys.
type DataAPIAccessConfig struct {
	Version    int                `json:"version"`
	Schemas    []string           `json:"schemas"`
	PublicRead []string           `json:"public_read,omitempty"`
	ServerKeys []DataAPIServerKey `json:"server_keys,omitempty"`
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
