package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// recordAppActivity projects one successful app mutation into the global
// organization timeline. Projection failures are observable but do not turn a
// committed primary mutation into an ambiguous HTTP failure.
func (s *server) recordAppActivity(ctx context.Context, r *http.Request, acct state.Account, app state.App, entry state.OrgActivity) {
	activityStore, ok := s.store.(state.OrgActivityStore)
	outbox, hasOutbox := s.store.(state.OrgActivityOutboxStore)
	if !ok && !hasOutbox {
		return
	}
	entry, err := s.prepareAppActivity(ctx, r, acct, app, entry)
	if err != nil {
		if s.log != nil {
			s.log.Warn("activity: prepare failed", "app", app.ID, "kind", entry.Kind, "err", err)
		}
		return
	}
	if hasOutbox {
		id, err := outbox.EnqueueOrgActivityOutbox(ctx, entry)
		if err != nil {
			if s.log != nil {
				s.log.Warn("activity: enqueue failed", "org", entry.OrgID.String(), "app", app.ID, "kind", entry.Kind, "err", err)
			}
			return
		}
		s.deliverOrgActivityOutbox(ctx, id)
		return
	}
	if _, err := activityStore.AppendOrgActivity(ctx, entry); err != nil {
		if s.log != nil {
			s.log.Warn("activity: append failed", "org", entry.OrgID.String(), "app", app.ID, "kind", entry.Kind, "err", err)
		}
	}
}

func (s *server) prepareAppActivity(ctx context.Context, r *http.Request, acct state.Account, app state.App, entry state.OrgActivity) (state.OrgActivity, error) {
	orgID, err := s.resolveActivityOrg(ctx, app)
	if err != nil {
		return state.OrgActivity{}, err
	}
	appID, err := uuid.Parse(app.ID)
	if err != nil {
		return state.OrgActivity{}, err
	}
	entry.OrgID = orgID
	entry.AppID = &appID
	if entry.ResourceType == "" {
		entry.ResourceType = "app"
	}
	if entry.ResourceID == "" {
		entry.ResourceID = app.ID
	}
	if entry.ResourceLabel == "" {
		entry.ResourceLabel = app.Slug
	}
	if entry.ActorType == "" {
		entry.ActorType, entry.ActorLabel, entry.ActorAccountID = activityActor(r, acct)
	}
	return entry, nil
}

func newAppLifecycleActivity(r *http.Request, acct state.Account, kind, sourceType string, data map[string]any) state.OrgActivity {
	actorType, actorLabel, actorID := activityActor(r, acct)
	return state.OrgActivity{
		Kind: kind, ActorType: actorType, ActorAccountID: actorID, ActorLabel: actorLabel,
		ResourceType: "app", SourceType: sourceType,
		SourceID: activitySourceID(r, sourceType), Data: activityData(data),
	}
}

func (s *server) createAppIfUnderQuotaWithActivity(ctx context.Context, r *http.Request, acct state.Account, app state.App, limits api.Limits) (state.App, error) {
	entry := newAppLifecycleActivity(r, acct, "app.created", "app.created", map[string]any{
		"phase": "created", "app_type": string(app.Type),
	})
	if mutationStore, ok := s.store.(state.OrgActivityAppLifecycleMutationStore); ok {
		created, outboxID, err := mutationStore.CreateAppIfUnderQuotaWithActivity(ctx, app, limits, entry)
		if err == nil && outboxID > 0 {
			s.deliverOrgActivityOutbox(ctx, outboxID)
		}
		return created, err
	}
	created, err := s.store.CreateAppIfUnderQuota(ctx, app, limits)
	if err == nil {
		s.recordAppActivity(ctx, r, acct, created, entry)
	}
	return created, err
}

func (s *server) scheduleAppDeletionWithActivity(ctx context.Context, r *http.Request, acct state.Account, app state.App, graceUntil time.Time) (state.App, error) {
	entry := newAppLifecycleActivity(r, acct, "app.deleted", "app.deleted", map[string]any{
		"phase": "deleted", "delete_grace_until": graceUntil.UTC().Format(time.RFC3339),
	})
	prepared, prepareErr := s.prepareAppActivity(ctx, r, acct, app, entry)
	if prepareErr != nil && s.log != nil {
		s.log.Warn("activity: prepare app deletion", "app", app.ID, "err", prepareErr)
	}
	if mutationStore, ok := s.store.(state.OrgActivityAppLifecycleMutationStore); ok && prepareErr == nil {
		parked, outboxID, err := mutationStore.ScheduleAppDeletionWithActivity(ctx, app.ID, graceUntil, prepared)
		if err == nil && outboxID > 0 {
			s.deliverOrgActivityOutbox(ctx, outboxID)
		}
		return parked, err
	}
	parked, err := s.store.ScheduleAppDeletion(ctx, app.ID, graceUntil)
	if err == nil && prepareErr == nil {
		s.recordAppActivity(ctx, r, acct, app, prepared)
	}
	return parked, err
}

func (s *server) restoreAppWithActivity(ctx context.Context, r *http.Request, acct state.Account, app state.App) (state.App, error) {
	entry := newAppLifecycleActivity(r, acct, "app.restored", "app.restored", map[string]any{"phase": "restored"})
	prepared, prepareErr := s.prepareAppActivity(ctx, r, acct, app, entry)
	if prepareErr != nil && s.log != nil {
		s.log.Warn("activity: prepare app restore", "app", app.ID, "err", prepareErr)
	}
	if mutationStore, ok := s.store.(state.OrgActivityAppLifecycleMutationStore); ok && prepareErr == nil {
		restored, outboxID, err := mutationStore.RestoreAppWithActivity(ctx, app.ID, prepared)
		if err == nil && outboxID > 0 {
			s.deliverOrgActivityOutbox(ctx, outboxID)
		}
		return restored, err
	}
	restored, err := s.store.RestoreApp(ctx, app.ID)
	if err == nil && prepareErr == nil {
		s.recordAppActivity(ctx, r, acct, restored, prepared)
	}
	return restored, err
}

// App ownership is persisted on the resource row. A caller's active org or
// org-bound API key cannot determine where app details belong: the same
// account may be a member of an unrelated shared organization. The personal
// org lookup is retained only for legacy rows written before org_id existed.
func (s *server) resolveActivityOrg(ctx context.Context, app state.App) (uuid.UUID, error) {
	if app.OrgID != "" {
		return uuid.Parse(app.OrgID)
	}
	org, err := s.store.OrgByPersonalAccount(ctx, app.AccountID)
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(org.ID)
}

func activityActor(r *http.Request, acct state.Account) (state.OrgActivityActorType, string, *uuid.UUID) {
	if r != nil {
		if _, key, ok := authmw.AccountFromContext(r); ok && key != nil {
			label := strings.TrimSpace(key.Label)
			if label == "" {
				label = "API key"
			}
			return state.OrgActivityActorAPIKey, label, nil
		}
	}
	accountID, err := uuid.Parse(acct.ID)
	if err != nil {
		return state.OrgActivityActorUser, acct.Email, nil
	}
	return state.OrgActivityActorUser, acct.Email, &accountID
}

// activitySourceID uses a digest of Idempotency-Key when one exists so an
// HTTP retry cannot duplicate the projection without storing the caller's raw
// key. Calls without an idempotency key receive a fresh UUID and therefore
// remain honest separate mutations.
func activitySourceID(r *http.Request, prefix string) string {
	if r != nil {
		if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
			sum := sha256.Sum256([]byte(key))
			return prefix + ":idem:" + hex.EncodeToString(sum[:])
		}
	}
	return prefix + ":" + uuid.NewString()
}

func activityData(values map[string]any) json.RawMessage {
	raw, err := json.Marshal(values)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func (s *server) recordDomainTLSIssuedActivity(ctx context.Context, domain state.CustomDomain, notAfter time.Time) {
	app, err := s.store.AppByID(ctx, domain.AppID)
	if err != nil {
		if s.log != nil {
			s.log.Warn("activity: resolve certificate app failed", "domain", domain.Domain, "err", err)
		}
		return
	}
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		if s.log != nil {
			s.log.Warn("activity: resolve certificate account failed", "domain", domain.Domain, "err", err)
		}
		return
	}
	s.recordAppActivity(ctx, nil, acct, app, state.OrgActivity{
		Kind: "domain.tls_issued", ActorType: state.OrgActivityActorSystem, ActorLabel: "Gregale",
		ResourceType: "domain", ResourceID: domain.Domain, ResourceLabel: domain.Domain,
		SourceType: "certificate", SourceID: domain.Domain + ":" + notAfter.UTC().Format(time.RFC3339Nano),
		Data: activityData(map[string]any{"not_after": notAfter.UTC().Format(time.RFC3339)}),
	})
}

func (s *server) recordDeploymentActivity(ctx context.Context, r *http.Request, acct state.Account, app state.App, deployment state.Deployment, data map[string]any) {
	entry := s.newDeploymentActivity(ctx, r, acct, app, data)
	if entry == nil {
		return
	}
	entry.SourceID = deployment.ID
	if deploymentID, err := uuid.Parse(deployment.ID); err == nil {
		entry.DeploymentID = &deploymentID
	}
	if deployment.DeployedVia == "github" || deployment.PusherLogin != "" {
		entry.ActorType = state.OrgActivityActorGitHub
		entry.ActorLabel = "GitHub Actions"
	}
	s.recordAppActivity(ctx, r, acct, app, *entry)
}

func (s *server) cancelDeploymentWithActivity(ctx context.Context, r *http.Request, acct state.Account, app state.App, prior state.Deployment, reason state.CancelReason, operator bool) (state.Deployment, []string, int64, error) {
	principal := acct.ID
	if operator {
		principal = "operator:" + acct.ID
	}
	activity := state.OrgActivity{
		Kind: "deploy.cancelled", ResourceType: "app", ResourceID: app.ID, ResourceLabel: app.Slug,
		SourceType: "deployment.cancelled",
		SourceID:   activitySourceID(r, "deployment.cancelled:"+prior.ID),
		Data: activityData(map[string]any{
			"phase": "cancelled", "reason": string(reason), "previous_status": string(prior.Status),
		}),
	}
	deploymentID, deploymentErr := uuid.Parse(prior.ID)
	if deploymentErr != nil {
		if s.log != nil {
			s.log.Warn("activity: parse cancelled deployment id", "deployment", prior.ID, "err", deploymentErr)
		}
	} else {
		activity.DeploymentID = &deploymentID
	}
	prepared, prepareErr := s.prepareAppActivity(ctx, r, acct, app, activity)
	if prepareErr != nil {
		if s.log != nil {
			s.log.Warn("activity: prepare deployment cancellation", "deployment", prior.ID, "err", prepareErr)
		}
	}
	if prepareErr == nil && operator {
		prepared.ActorType = state.OrgActivityActorOperator
		prepared.ActorLabel = strings.TrimSpace(acct.Email)
		if prepared.ActorLabel == "" {
			prepared.ActorLabel = "Gregale operator"
		}
		if actorID, err := uuid.Parse(acct.ID); err == nil {
			prepared.ActorAccountID = &actorID
		}
	}
	if mutationStore, ok := s.store.(state.OrgActivityCancellationMutationStore); ok && prepareErr == nil {
		return mutationStore.CancelDeploymentTxWithActivity(ctx, prior.ID, principal, reason, prepared)
	}
	deployment, cancelledBuilds, err := s.store.CancelDeploymentTx(ctx, prior.ID, principal, reason)
	if err == nil && prepareErr == nil {
		s.recordAppActivity(ctx, nil, acct, app, prepared)
	}
	return deployment, cancelledBuilds, 0, err
}

func (s *server) newDeploymentActivity(ctx context.Context, r *http.Request, acct state.Account, app state.App, data map[string]any) *state.OrgActivity {
	requestData := make(map[string]any, len(data)+1)
	for key, value := range data {
		requestData[key] = value
	}
	requestData["phase"] = "requested"
	entry, err := s.prepareAppActivity(ctx, r, acct, app, state.OrgActivity{
		Kind: "deploy.requested", SourceType: "deployment.requested", Data: activityData(requestData),
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("activity: prepare deployment failed", "app", app.ID, "err", err)
		}
		return nil
	}
	return &entry
}
