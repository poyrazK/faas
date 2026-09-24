package gateway

import (
	"encoding/base32"
	"strings"

	"github.com/google/uuid"
)

// An environment host names both durable identities, not mutable slugs. Two
// 128-bit UUIDs fit in one DNS label when encoded as unpadded base32 (57 bytes
// including separators), so the existing platform wildcard certificate covers
// the URL without a hostname allocation table or per-environment DNS records.
const environmentHostPrefix = "env-"

var environmentHostEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// BuildEnvironmentHost returns the stable platform hostname for one workload
// in one registered project environment. A deleted and recreated environment
// gets a new URL, so an old link can never silently point to a new scope.
func BuildEnvironmentHost(suffix, environmentID, appID string) string {
	if suffix == "" || !strings.HasPrefix(suffix, ".") {
		return ""
	}
	environment, err := uuid.Parse(environmentID)
	if err != nil {
		return ""
	}
	app, err := uuid.Parse(appID)
	if err != nil {
		return ""
	}
	return environmentHostPrefix + strings.ToLower(environmentHostEncoding.EncodeToString(environment[:])) + "-" +
		strings.ToLower(environmentHostEncoding.EncodeToString(app[:])) + suffix
}

// EnvironmentIDsFromHost is the inverse of BuildEnvironmentHost. It accepts
// only the canonical lowercase one-label shape, not lookalike app/custom
// hosts or a differently encoded form of the same UUIDs.
func EnvironmentIDsFromHost(suffix, host string) (environmentID, appID string, ok bool) {
	if suffix == "" || !strings.HasPrefix(suffix, ".") {
		return "", "", false
	}
	label, matched := strings.CutSuffix(host, suffix)
	if !matched || len(label) != 57 || !strings.HasPrefix(label, environmentHostPrefix) || label[30] != '-' {
		return "", "", false
	}
	decode := func(encoded string) (string, bool) {
		if len(encoded) != 26 || strings.ToLower(encoded) != encoded {
			return "", false
		}
		decoded, err := environmentHostEncoding.DecodeString(strings.ToUpper(encoded))
		if err != nil || len(decoded) != 16 {
			return "", false
		}
		id, err := uuid.FromBytes(decoded)
		if err != nil || strings.ToLower(environmentHostEncoding.EncodeToString(id[:])) != encoded {
			return "", false
		}
		return id.String(), true
	}
	environmentID, ok = decode(label[4:30])
	if !ok {
		return "", "", false
	}
	appID, ok = decode(label[31:])
	if !ok {
		return "", "", false
	}
	return environmentID, appID, true
}
