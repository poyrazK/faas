package deploystaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cronexpr"
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
		// The declared timezone and skip_if_running apply, as on a CLI deploy;
		// every source-declared cron used to run in UTC.
		timezone, err := cronexpr.NormalizeTimezone(t.Timezone)
		if err != nil {
			return ctx, nil, state.ErrInvalidArgument
		}
		r.Desired = append(r.Desired, api.DeploymentCronConfig{ID: uuid.NewString(), Schedule: t.Schedule, Path: t.Path, Enabled: t.IsEnabled(),
			Timezone: timezone, SkipIfRunning: t.SkipIfRunning != nil && *t.SkipIfRunning})
		keys[key] = true
	}
	limits, _ := api.LimitsFor(acct.Plan)
	_, err = store.ApplyDeploymentCronStaging(ctx, app.ID, state.DeploymentCronStagingParams{AccountID: acct.ID, Request: r, Limits: limits})
	if err != nil {
		return ctx, nil, err
	}
	return ctx, &r, nil
}

// PrepareDeliveryCrons is PrepareSourceCrons for a GitHub push. Its session is
// derived from the webhook delivery and the app, as the deployment's IDs are,
// so a delivery githubd retries prepares the same intent again instead of a
// second one.
func PrepareDeliveryCrons(ctx context.Context, store state.DeploymentCronStagingStore, acct state.Account, app state.App, m *gregalemanifest.Manifest, deliveryID string) (context.Context, *api.DeploymentCronStagingRequest, error) {
	if deliveryID != "" && state.DeploymentSubmissionAttempt(ctx) == "" {
		sum := sha256.Sum256([]byte("github-delivery-crons\x00" + deliveryID + "\x00" + app.ID))
		ctx = state.ContextWithDeploymentSubmission(ctx, hex.EncodeToString(sum[:api.DeploymentAttemptIDBytes/2]))
	}
	return PrepareSourceCrons(ctx, store, acct, app, m)
}

// CancelSourceCrons withdraws an intent PrepareSourceCrons returned when its
// deployment was definitely not created, so it does not hold the app's later
// source deploys back from staging their crons.
func CancelSourceCrons(ctx context.Context, store state.DeploymentCronStagingStore, accountID, appID string, intent *api.DeploymentCronStagingRequest) error {
	if intent == nil {
		return nil
	}
	r := *intent
	r.Operation = "cancel"
	_, err := store.ApplyDeploymentCronStaging(ctx, appID, state.DeploymentCronStagingParams{AccountID: accountID, Request: r})
	return err
}
