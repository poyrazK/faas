package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Only APID passes internally observed binding fences. This route cannot be
// used by promotion/rollback workers until they gain graph qualification.
type CheckedProjectReleaseSetStore interface {
	CheckProjectReleaseEligibility(context.Context, string, string, string, int, []ProjectReleaseMember) error
	PublishProjectReleaseSetWithBindings(context.Context, string, string, string, string, int, []ProjectReleaseMember) (ProjectReleaseSet, error)
}

type checkedProjectReleaseKey struct{}
type projectReleaseEligibilityKey struct{}

func (m *MemStore) CheckProjectReleaseEligibility(ctx context.Context, account, project, environment string, ttl int, members []ProjectReleaseMember) error {
	_, err := m.publishProjectReleaseSet(context.WithValue(ctx, projectReleaseEligibilityKey{}, true), account, project, environment, nil, nil, ttl, members)
	return err
}
func (s *PgStore) CheckProjectReleaseEligibility(ctx context.Context, account, project, environment string, ttl int, members []ProjectReleaseMember) error {
	_, err := s.publishProjectReleaseSet(context.WithValue(ctx, projectReleaseEligibilityKey{}, true), account, project, environment, nil, nil, ttl, members)
	return err
}
func projectReleaseEligibilityOnly(ctx context.Context) bool {
	check, _ := ctx.Value(projectReleaseEligibilityKey{}).(bool)
	return check
}

func projectReleaseBindingAudit(ctx context.Context, release ProjectReleaseSet, previous string) ([]byte, error) {
	report := api.ProjectReleaseCheckResponse{ProjectID: release.ProjectID, Environment: release.EnvironmentSlug, ExpectedActiveReleaseID: previous, TTLSeconds: release.TTLSeconds}
	for _, m := range release.Members {
		report.Members = append(report.Members, api.ProjectReleaseSetMemberResponse{AppID: m.AppID, DeploymentID: m.DeploymentID})
	}
	return json.Marshal(map[string]any{"release_id": release.ID, "previous_release_id": previous, "project_id": release.ProjectID, "environment": release.EnvironmentSlug, "graph_digest": api.ProjectReleaseGraphDigest(report), "members": release.Members, "binding_fences": bindingReleaseFences(ctx)})
}

func (m *MemStore) PublishProjectReleaseSetWithBindings(ctx context.Context, account, project, environment, expected string, ttl int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	return m.publishProjectReleaseSet(context.WithValue(ctx, checkedProjectReleaseKey{}, true), account, project, environment, &expected, nil, ttl, members)
}

func (s *PgStore) PublishProjectReleaseSetWithBindings(ctx context.Context, account, project, environment, expected string, ttl int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	return s.publishProjectReleaseSet(context.WithValue(ctx, checkedProjectReleaseKey{}, true), account, project, environment, &expected, nil, ttl, members)
}

func checkedProjectRelease(ctx context.Context) bool {
	checked, _ := ctx.Value(checkedProjectReleaseKey{}).(bool)
	return checked
}

func pgAuthorizeProjectRelease(ctx context.Context, tx pgx.Tx, candidate, previous string) error {
	body, err := bindingReleaseGrantJSON(bindingReleaseFences(ctx))
	if err != nil {
		return err
	}
	old := pgtype.UUID{}
	if previous != "" {
		old = mustPgUUID(previous)
	}
	_, err = sqlc.New().AuthorizeBindingReleaseGraph(ctx, tx, sqlc.AuthorizeBindingReleaseGraphParams{Candidate: mustPgUUID(candidate), Previous: old, Fences: body})
	return mapErr(err)
}

func (m *MemStore) checkProjectReleaseBindingsLocked(ctx context.Context, project, environment string, members []ProjectReleaseMember) error {
	if !checkedProjectRelease(ctx) {
		return m.rejectUncheckedBindingReleaseGraphLocked(project, environment)
	}
	fences := bindingReleaseFences(ctx)
	if len(fences) != len(members) {
		return ErrBindingReleaseRequired
	}
	for _, member := range members {
		d := m.deployments[member.DeploymentID]
		found := false
		for _, f := range fences {
			if f.AppID != member.AppID || !sameDeploymentID(f.DeploymentID, member.DeploymentID) {
				continue
			}
			p := m.bindingReleasePolicyLocked(member.AppID, environment)
			if f.AllowUnsupported || f.PolicyRevision != p.Revision || p.Mode == "enforce" && !releaseFenceMatchesPolicy(f, p) {
				return ErrBindingPromotionChanged
			}
			if err := m.checkBindingTrafficGuardLocked(d, &bindingTrafficGuard{fence: f}); err != nil {
				return err
			}
			found = true
			break
		}
		if !found {
			return ErrBindingReleaseRequired
		}
	}
	// Check every deadline after all revision comparisons, just before mutation.
	for _, f := range fences {
		if !f.ValidUntil.IsZero() && !f.ValidUntil.After(time.Now()) {
			return ErrBindingPromotionExpired
		}
	}
	return nil
}
