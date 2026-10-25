package api

import (
	"context"
	"net/http"
	"net/url"
)

// Release diff change kinds (ADR-20261021101117).
const (
	ReleaseDiffAdded   = "added"
	ReleaseDiffRemoved = "removed"
	ReleaseDiffChanged = "changed"
)

// ReleaseDiff is what changed between two releases of one app. It never
// carries variable values, secret fingerprints or SBOM files.
type ReleaseDiff struct {
	App  string           `json:"app"`
	From *ReleaseDiffSide `json:"from,omitempty"`
	To   ReleaseDiffSide  `json:"to"`
	// Summary is one line, such as "new code, 2 config keys, 5 packages".
	Summary      string                  `json:"summary"`
	Code         ReleaseDiffCode         `json:"code"`
	Config       ReleaseDiffConfig       `json:"config"`
	Dependencies ReleaseDiffDependencies `json:"dependencies"`
	Runtime      []ReleaseDiffField      `json:"runtime"`
	Database     ReleaseDiffDatabase     `json:"database"`
}

// ReleaseDiffSide identifies one of the two releases.
type ReleaseDiffSide struct {
	DeploymentID string `json:"deployment_id"`
	Revision     int    `json:"revision,omitempty"`
	CreatedAt    string `json:"created_at"`
	DeployedBy   string `json:"deployed_by,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Tag          string `json:"tag,omitempty"`
	PRNumber     int    `json:"pr_number,omitempty"`
	Commit       string `json:"commit,omitempty"`
}

// ReleaseDiffCode compares the releases' source and image.
type ReleaseDiffCode struct {
	Changed      bool   `json:"changed"`
	FromCommit   string `json:"from_commit,omitempty"`
	ToCommit     string `json:"to_commit,omitempty"`
	CompareURL   string `json:"compare_url,omitempty"`
	ImageChanged bool   `json:"image_changed"`
}

// ReleaseDiffKey is one configuration key that changed.
type ReleaseDiffKey struct {
	Key    string `json:"key"`
	Change string `json:"change"`
}

// ReleaseDiffConfig compares the configuration each release started with.
// Known is false when the retained config history no longer reaches a
// release; Note says why.
type ReleaseDiffConfig struct {
	Known     bool             `json:"known"`
	Note      string           `json:"note,omitempty"`
	Variables []ReleaseDiffKey `json:"variables"`
	Secrets   []ReleaseDiffKey `json:"secrets"`
}

// ReleaseDiffPackage is one package that changed between the builds.
type ReleaseDiffPackage struct {
	Name string `json:"name"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// ReleaseDiffDependencies compares the builds' SBOMs. Each list holds at
// most ReleaseDiffPackagesMax entries; Truncated reports a cut.
type ReleaseDiffDependencies struct {
	Known     bool                 `json:"known"`
	Note      string               `json:"note,omitempty"`
	Added     []ReleaseDiffPackage `json:"added"`
	Removed   []ReleaseDiffPackage `json:"removed"`
	Changed   []ReleaseDiffPackage `json:"changed"`
	Truncated bool                 `json:"truncated,omitempty"`
}

// ReleaseDiffField is one runtime setting that changed.
type ReleaseDiffField struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// ReleaseDiffDatabase compares the releases' release commands, which run
// database migrations (ADR-1448).
type ReleaseDiffDatabase struct {
	Changed            bool   `json:"changed"`
	FromReleaseCommand string `json:"from_release_command,omitempty"`
	ToReleaseCommand   string `json:"to_release_command,omitempty"`
}

// GetReleaseDiff compares a release with the one before it, or with
// against when set.
func (c *Client) GetReleaseDiff(ctx context.Context, deploymentID, against string) (ReleaseDiff, error) {
	var out ReleaseDiff
	path := "/v1/deployments/" + url.PathEscape(deploymentID) + "/diff"
	if against != "" {
		path += "?against=" + url.QueryEscape(against)
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}
