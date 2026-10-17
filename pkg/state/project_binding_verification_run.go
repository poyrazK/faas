// adr:1356 — graph and member receipts share a fixed review and deadline.
package state

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"maps"
	"slices"
	"time"
)

type GraphVerificationCreate struct {
	Report  api.ProjectBindingVerificationRun
	Members map[string]VerificationRunCreate
}
type graphVerificationRecord struct {
	AccountID, ProjectID string
	Report               api.ProjectBindingVerificationRun
}
type ProjectBindingVerificationRunStore interface {
	CreateProjectBindingVerificationRun(context.Context, string, string, GraphVerificationCreate) (api.ProjectBindingVerificationRun, error)
	GetProjectBindingVerificationRun(context.Context, string, string, string, string) (api.ProjectBindingVerificationRun, error)
	CheckProjectBindingVerificationRun(context.Context, string, string, string, string, ...map[string][]VerificationRunAdmission) error
	SetProjectBindingVerificationRunStatus(context.Context, string, string, string, string, string) error
}

func cloneGraphVerification(r graphVerificationRecord) graphVerificationRecord {
	raw, _ := json.Marshal(r)
	var out graphVerificationRecord
	_ = json.Unmarshal(raw, &out)
	return out
}
func publicGraphVerification(r graphVerificationRecord) api.ProjectBindingVerificationRun {
	out := cloneGraphVerification(r).Report
	out.MaxConcurrency = api.BindingVerificationConcurrency(out.MaxConcurrency)
	for name, member := range out.Members {
		member.MaxConcurrency = api.BindingVerificationConcurrency(member.MaxConcurrency)
		out.Members[name] = member
	}
	if out.Status != "cancelled" && !out.ExpiresAt.After(time.Now()) {
		out.Status, out.Reason = "expired", "run_expired"
	}
	return out
}
func validateGraphVerificationCreate(account string, in GraphVerificationCreate) error {
	r := in.Report
	if api.BindingVerificationConcurrency(r.MaxConcurrency) < 1 || api.BindingVerificationConcurrency(r.MaxConcurrency) > api.BindingVerificationRunnerMaxConcurrency || !canonicalStateUUID(r.ID) || r.CreatedAt.IsZero() || r.CreatedAt.After(time.Now()) || !r.ExpiresAt.After(time.Now()) || r.ExpiresAt.Sub(r.CreatedAt) > time.Duration(api.BindingVerificationRunMaxTTLSeconds)*time.Second || r.Project != r.Manifest.Project || r.Environment != r.Manifest.Environment || len(in.Members) == 0 || len(in.Members) > api.ProjectReleaseSetMaxMembers || len(in.Members) != len(r.Manifest.Deployments) || len(r.Members) != len(in.Members) || !bindingRevisionPattern.MatchString(r.ReviewDigest) {
		return ErrInvalidArgument
	}
	ids := map[string]bool{}
	for slug, member := range in.Members {
		child := member.Report
		if api.BindingVerificationConcurrency(child.MaxConcurrency) != api.BindingVerificationConcurrency(r.MaxConcurrency) || child.App != slug || child.GraphRunID != r.ID || child.Scope != r.Environment || stageCanonicalID(child.DeploymentID) != r.Manifest.Deployments[slug] || child.Policy.Revision != r.Manifest.ExpectedPolicyRevisions[slug] || !maps.Equal(child.Inventory.ServiceTargets, r.Manifest.ServiceTargets[slug]) || child.QueuePublish != r.QueuePublish || child.ObjectStorageWrite != r.ObjectStorageWrite || !child.CreatedAt.Equal(r.CreatedAt) || !child.ExpiresAt.Equal(r.ExpiresAt) || ids[child.ID] || r.Members[slug].ID != child.ID {
			return ErrInvalidArgument
		}
		if err := validateVerificationRunCreate(account, child.Policy.AppID, member); err != nil {
			return err
		}
		ids[child.ID] = true
	}
	return nil
}
func validateGraphVerificationSnapshot(s releaseStageSnapshot, m api.CheckedProjectReleaseManifest) error {
	if m.Project != s.Project.Slug || m.ExpectedActiveReleaseID == nil || m.ExpectedFallbackDeployments == nil || len(s.Apps) != len(m.Deployments) || len(s.Apps) == 0 || len(s.Apps) > api.ProjectReleaseSetMaxMembers || len(m.ServiceTargets) != len(s.Apps) || len(m.ExpectedPolicyRevisions) != len(s.Apps) {
		return ErrConflict
	}
	if stageCanonicalID(s.Active) != *m.ExpectedActiveReleaseID {
		return ErrConflict
	}
	fallback := map[string]string{}
	members := map[string]string{}
	for _, app := range s.Apps {
		d, ok := s.Deployments[m.Deployments[app.Slug]]
		if !ok || d.AppID != app.ID || d.Scope != m.Environment || d.Status != DeployLive || d.RootfsKey == "" || d.ImageDigest == "" || d.EnvironmentWorkloadHeld() || app.AccountID != s.Project.AccountID || app.Status != AppActive || s.Managed[app.ID] || s.Mirrors[stageCanonicalID(d.ID)] {
			return ErrConflict
		}
		if !m.Deactivate && (m.TTLSeconds < 1 || m.TTLSeconds > api.RevisionPinMaxTTLSeconds || app.Manifest.RevisionPinTTLSeconds < m.TTLSeconds) {
			return ErrConflict
		}
		p := s.Policies[app.ID]
		if p.Mode != "enforce" || p.Revision != m.ExpectedPolicyRevisions[app.Slug] {
			return ErrConflict
		}
		targets := map[string]string{}
		for _, b := range app.Manifest.ServiceBindings {
			if id := m.Deployments[b.Service]; id != "" {
				targets[b.Service] = id
			}
		}
		if m.ServiceTargets[app.Slug] == nil || !maps.Equal(targets, m.ServiceTargets[app.Slug]) {
			return ErrConflict
		}
		members[app.ID] = d.ID
		if s.Active == "" || m.Deactivate {
			for _, live := range s.Deployments {
				if live.AppID == app.ID && live.Scope == m.Environment && live.Status == DeployLive && live.TrafficPercent > 0 {
					if live.TrafficPercent != 100 || fallback[app.Slug] != "" {
						return ErrConflict
					}
					fallback[app.Slug] = stageCanonicalID(live.ID)
				}
			}
		}
	}
	if (s.Active == "" || m.Deactivate) && !maps.Equal(fallback, m.ExpectedFallbackDeployments) {
		return ErrConflict
	}
	if s.Active != "" && !m.Deactivate && len(m.ExpectedFallbackDeployments) != 0 {
		return ErrConflict
	}
	if m.Deactivate && (m.TTLSeconds != 0 || m.RollbackReleaseID != "" || s.Active == "" || !maps.Equal(m.Deployments, m.ExpectedFallbackDeployments)) {
		return ErrConflict
	}
	if m.RollbackReleaseID != "" {
		if s.Source == nil {
			return ErrConflict
		}
		if err := validateCheckedRollbackSource(*s.Source, s.Project.AccountID, s.Project.ID, m.Environment, members, time.Now()); err != nil {
			return err
		}
	}
	return nil
}
func graphVerificationActive(r api.ProjectBindingVerificationRun) error {
	if r.Status != "pending" && r.Status != "passed" || !r.ExpiresAt.After(time.Now()) {
		return ErrConflict
	}
	return nil
}
func graphMemberReviewActive(r api.BindingVerificationRun) error {
	if r.Status != "pending" && r.Status != "passed" || !r.ExpiresAt.After(time.Now()) {
		return ErrConflict
	}
	age, err := time.ParseDuration(r.Policy.MaxVerificationAge)
	if err != nil {
		return err
	}
	for _, s := range r.Steps {
		if s.Plan.Action == "reuse" && s.Plan.CheckedAt != nil && time.Since(*s.Plan.CheckedAt) > age {
			return ErrConflict
		}
	}
	return nil
}

func graphVerificationMemberNames(r api.ProjectBindingVerificationRun) []string {
	return slices.Sorted(maps.Keys(r.Members))
}

func graphMemberAdmissionsMatch(r verificationRunRecord, name string, captures []map[string][]VerificationRunAdmission, memberCount int) bool {
	if len(captures) == 0 {
		return true
	}
	if len(captures) != 1 || len(captures[0]) != memberCount {
		return false
	}
	captured, ok := captures[0][name]
	if !ok {
		return false
	}
	if r.Report.Status == "passed" && captured == nil {
		return true
	}
	return verificationRunAdmissionsMatch(r, [][]VerificationRunAdmission{captured})
}
