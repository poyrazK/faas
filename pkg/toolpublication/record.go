// Package toolpublication validates credential-free publication evidence shared
// by apid and the management MCP bridge. It does not execute or replay work.
package toolpublication

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var hexID = regexp.MustCompile(`^[0-9a-f]{64}$`)

func ValidID(id string) bool { return hexID.MatchString(id) }

func ValidateIntent(in api.ToolPublicationIntent) error {
	if in.EndpointPath != "" && !ValidEndpointPath(in.EndpointPath) {
		return errors.New("invalid publication endpoint path")
	}
	data, err := json.Marshal(in)
	if err != nil || len(data) > api.ToolPublicationIntentMaxBytes || !api.ValidAppSlug(in.App) || !ValidID(in.SourceSHA256) || !ValidID(in.FixturesSHA256) ||
		(in.AccessMode != "open" && in.AccessMode != "external-oauth") || len(in.ToolScopes) < 1 || len(in.ToolScopes) > api.ToolPublicationMaxTools || len(in.TestCases) < 1 || len(in.TestCases) > api.ToolPublicationMaxCases {
		return errors.New("invalid publication intent")
	}
	seen := map[string]bool{}
	for _, name := range in.TestCases {
		if name == "" || len(name) > 256 || seen[name] {
			return errors.New("invalid or duplicate test case")
		}
		seen[name] = true
	}
	for name, scopes := range in.ToolScopes {
		if name == "" || len(name) > 128 || len(scopes) > 64 {
			return errors.New("invalid tool access requirements")
		}
		for _, scope := range scopes {
			if scope == "" || len(scope) > 256 {
				return errors.New("invalid tool scope")
			}
		}
	}
	return nil
}

func ValidEndpointPath(value string) bool {
	u, err := url.Parse(value)
	return err == nil && len(value) <= api.ToolInvocationEndpointMaxBytes && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.ContainsAny(value, "%\\\r\n\x00") &&
		u.Scheme == "" && u.Host == "" && u.RawQuery == "" && u.Fragment == "" && u.Path == value && (path.Clean(value) == value || path.Clean(value)+"/" == value)
}

func ValidateOutcome(intent api.ToolPublicationIntent, out api.ToolPublicationOutcome) error {
	data, err := json.Marshal(out)
	if err != nil || len(data) > api.ToolPublicationOutcomeMaxBytes {
		return errors.New("publication evidence exceeds bound")
	}
	if out.Status == "failed" || out.Status == "outcome_unknown" {
		if out.DeploymentID != "" || out.CatalogSHA256 != "" || len(out.Catalog) != 0 || out.ProtocolVerified || len(out.Checks) != 0 {
			return errors.New("unsuccessful outcome must not register evidence")
		}
		return nil
	}
	if out.Status != "completed" || out.DeploymentID == "" || !ValidID(out.CatalogSHA256) || !out.ProtocolVerified || len(out.Checks) != len(intent.TestCases) || len(out.Catalog) > api.ToolPublicationCatalogMaxBytes {
		return errors.New("incomplete publication evidence")
	}
	for i, check := range out.Checks {
		if check.Name != intent.TestCases[i] || check.Status != "passed" {
			return errors.New("publication tests do not match review")
		}
	}
	// Domain handlers additionally validate the complete MCP contract. Keep
	// this storage-level check independent of runtime transports and state.
	var catalog struct {
		Version         int    `json:"version"`
		ProtocolVersion string `json:"protocol_version"`
		Tools           []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	err = json.Unmarshal(out.Catalog, &catalog)
	if err != nil || catalog.Version != 2 || catalog.ProtocolVersion != api.MCPProtocolVersion || len(catalog.Tools) != len(intent.ToolScopes) {
		return errors.New("invalid runtime catalog")
	}
	seen := map[string]bool{}
	for _, tool := range catalog.Tools {
		if _, ok := intent.ToolScopes[tool.Name]; !ok || seen[tool.Name] {
			return errors.New("runtime tool was not reviewed")
		}
		seen[tool.Name] = true
	}
	return nil
}

// View turns an abandoned attempt into an uncertain result, never a resumable
// plan. No observer can turn a serving deployment into proof of lost tests.
func View(record api.ToolPublicationRecord, now time.Time) api.ToolPublicationRecord {
	if record.Status == "running" && !now.Before(record.Deadline) {
		record.Status = "outcome_unknown"
	}
	return record
}

func Digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func Matches(record api.ToolPublicationRecord, query string) bool {
	query = strings.ToLower(query)
	if strings.Contains(strings.ToLower(record.Intent.App), query) {
		return true
	}
	return slices.ContainsFunc(toolNames(record), func(name string) bool { return strings.Contains(strings.ToLower(name), query) })
}

func toolNames(record api.ToolPublicationRecord) []string {
	names := make([]string, 0, len(record.Intent.ToolScopes))
	for name := range record.Intent.ToolScopes {
		names = append(names, name)
	}
	return names
}

func Version(record api.ToolPublicationRecord) api.ToolRegistryVersion {
	names := toolNames(record)
	sort.Strings(names)
	return api.ToolRegistryVersion{AppID: record.Intent.AppID, Version: record.ID, AccountID: record.AccountID, App: record.Intent.App, Tools: names,
		DeploymentID: record.Outcome.DeploymentID, SourceSHA256: record.Intent.SourceSHA256, DeploymentSourceSHA256: record.DeploymentSourceSHA256,
		CatalogSHA256: record.Outcome.CatalogSHA256, EvidenceOrigin: record.EvidenceOrigin, AccessMode: record.Intent.AccessMode, Availability: "not_checked"}
}
