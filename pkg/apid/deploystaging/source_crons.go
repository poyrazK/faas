package deploystaging

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// PrepareSourceCrons preserves the source API's additive semantics. Existing
// schedules (including operator-managed options) remain untouched. The returned
// intent belongs to this request and can be canceled on definite rejection.
// Saved CLI intents are immutable and remain owned by the CLI recovery flow.
func PrepareSourceCrons(ctx context.Context, store state.DeploymentCronStagingStore, acct state.Account, app state.App, m *gregalemanifest.Manifest) (context.Context, *api.DeploymentCronStagingRequest, error) {
	if m == nil {
		return ctx, nil, nil
	}
	hasCron := false
	for _, t := range m.Triggers {
		hasCron = hasCron || t.App == app.Slug && t.Kind == gregalemanifest.TriggerKindCron
	}
	if !hasCron {
		return ctx, nil, nil
	}
	if prepared, ok := store.(state.PreparedDeploymentCronStore); ok {
		owned, err := prepared.PreparedDeploymentCrons(ctx, app.ID)
		if err != nil || owned {
			return ctx, nil, err
		}
	}
	if legacy, ok := store.(state.LegacyDeploymentCronStore); ok {
		owned, err := legacy.LegacyDeploymentCrons(ctx, app.ID)
		if err != nil || owned {
			return ctx, nil, err
		}
	}
	snapshot, err := store.DeploymentCronStagingSnapshot(ctx, acct.ID, app.ID)
	if err != nil {
		return ctx, nil, err
	}
	id := state.DeploymentSubmissionAttempt(ctx)
	if id == "" {
		id = strings.ReplaceAll(uuid.NewString(), "-", "")
		ctx = state.ContextWithDeploymentSubmission(ctx, id)
	}
	r := api.DeploymentCronStagingRequest{Operation: "prepare", SessionID: id, ExpectedAppID: app.ID, ExpectedRevision: snapshot.Revision, ExpectedDeploymentID: snapshot.LatestDeploymentID, Before: snapshot.Crons, Desired: append([]api.DeploymentCronConfig{}, snapshot.Crons...)}
	keys := make(map[string]bool)
	for _, c := range r.Desired {
		keys[c.Schedule+"\x00"+c.Path] = true
	}
	for _, t := range m.Triggers {
		key := t.Schedule + "\x00" + t.Path
		if t.App != app.Slug || t.Kind != gregalemanifest.TriggerKindCron || keys[key] {
			continue
		}
		r.Desired = append(r.Desired, api.DeploymentCronConfig{ID: uuid.NewString(), Schedule: t.Schedule, Path: t.Path, Enabled: t.IsEnabled(), Timezone: "UTC"})
		keys[key] = true
	}
	limits, _ := api.LimitsFor(acct.Plan)
	_, err = store.ApplyDeploymentCronStaging(ctx, app.ID, state.DeploymentCronStagingParams{AccountID: acct.ID, Request: r, Limits: limits})
	if err != nil {
		return ctx, nil, err
	}
	return ctx, &r, nil
}
