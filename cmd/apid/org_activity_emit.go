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

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// recordAppActivity projects one successful app mutation into the global
// organization timeline. Projection failures are observable but do not turn a
// committed primary mutation into an ambiguous HTTP failure.
func (s *server) recordAppActivity(ctx context.Context, r *http.Request, acct state.Account, app state.App, entry state.OrgActivity) {
	activityStore, ok := s.store.(state.OrgActivityStore)
	if !ok {
		return
	}
	orgID, err := s.resolveActivityOrg(ctx, app)
	if err != nil {
		if s.log != nil {
			s.log.Warn("activity: resolve organization failed", "app", app.ID, "kind", entry.Kind, "err", err)
		}
		return
	}
	appID, err := uuid.Parse(app.ID)
	if err != nil {
		if s.log != nil {
			s.log.Warn("activity: invalid app id", "app", app.ID, "kind", entry.Kind)
		}
		return
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
	if _, err := activityStore.AppendOrgActivity(ctx, entry); err != nil {
		if s.log != nil {
			s.log.Warn("activity: append failed", "org", orgID.String(), "app", app.ID, "kind", entry.Kind, "err", err)
		}
	}
}

// Apps are currently owned by an account, not an organization. A caller's
// active org or org-bound API key cannot determine where app details belong:
// the same account may be a member of an unrelated shared organization.
func (s *server) resolveActivityOrg(ctx context.Context, app state.App) (uuid.UUID, error) {
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
	entry := state.OrgActivity{
		Kind: "app.deployed", SourceType: "deployment", SourceID: deployment.ID,
		Data: activityData(data),
	}
	if deploymentID, err := uuid.Parse(deployment.ID); err == nil {
		entry.DeploymentID = &deploymentID
	}
	if deployment.DeployedVia == "github" || deployment.PusherLogin != "" {
		entry.ActorType = state.OrgActivityActorGitHub
		entry.ActorLabel = "GitHub Actions"
	}
	s.recordAppActivity(ctx, r, acct, app, entry)
}
