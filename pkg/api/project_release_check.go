package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	CodeProjectReleaseCheckFailed  = "project_release_check_failed"
	CodeProjectReleaseCheckChanged = "project_release_check_changed"
)

// This report is an observation, never a reusable activation grant. Publication
// reevaluates every member and compares the expected active graph under locks.
type ProjectReleaseCheckResponse struct {
	ProjectID               string                            `json:"project_id"`
	Environment             string                            `json:"environment"`
	ExpectedActiveReleaseID string                            `json:"expected_active_release_id"`
	GraphDigest             string                            `json:"graph_digest"`
	TTLSeconds              int                               `json:"ttl_seconds"`
	Members                 []ProjectReleaseSetMemberResponse `json:"members"`
	Passed                  bool                              `json:"passed"`
	CheckedAt               time.Time                         `json:"checked_at"`
	Checks                  []BindingCheckReport              `json:"checks"`
	Blockers                []BindingCheckFinding             `json:"blockers,omitempty"`
}

func ProjectReleaseGraphDigest(report ProjectReleaseCheckResponse) string {
	members := append([]ProjectReleaseSetMemberResponse(nil), report.Members...)
	for i, m := range members {
		members[i].AppID = canonicalReleaseUUID(m.AppID)
		members[i].DeploymentID = canonicalReleaseUUID(m.DeploymentID)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].AppID < members[j].AppID })
	raw, _ := json.Marshal(struct {
		Project, Environment, Expected string
		TTL                            int
		Members                        []ProjectReleaseSetMemberResponse
	}{canonicalReleaseUUID(report.ProjectID), report.Environment, report.ExpectedActiveReleaseID, report.TTLSeconds, members})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (c *Client) CheckProjectReleaseSet(ctx context.Context, project, environment string, req PublishProjectReleaseSetRequest) (ProjectReleaseCheckResponse, error) {
	var out ProjectReleaseCheckResponse
	return out, c.do(ctx, http.MethodPost, projectReleaseSetsPath(project, environment)+"/check", req, &out)
}

func canonicalReleaseUUID(raw string) string {
	// Stored PostgreSQL UUIDs and historical memory UUIDs differ only in
	// hyphenation. Keep the wire digest identical without SDK dependencies.
	if len(raw) != 32 && len(raw) != 36 {
		return raw
	}
	compact := strings.ReplaceAll(strings.ToLower(raw), "-", "")
	if len(compact) != 32 {
		return raw
	}
	if _, err := hex.DecodeString(compact); err != nil {
		return raw
	}
	return compact[:8] + "-" + compact[8:12] + "-" + compact[12:16] + "-" + compact[16:20] + "-" + compact[20:]
}
