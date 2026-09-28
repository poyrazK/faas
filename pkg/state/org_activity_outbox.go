package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// OrgActivityOutboxItem is one claimed durable activity fact. Claim metadata
// is intentionally omitted so stale workers cannot act as claim authority.
type OrgActivityOutboxItem struct {
	ID       int64
	Activity OrgActivity
	Attempts int
}

// OrgActivityOutboxStore is the apid delivery capability for the global
// organization activity projection.
type OrgActivityOutboxStore interface {
	EnqueueOrgActivityOutbox(context.Context, OrgActivity) (int64, error)
	ClaimOrgActivityOutbox(context.Context, string, time.Duration) (OrgActivityOutboxItem, error)
	DeliverOrgActivityOutbox(context.Context, int64) (delivered bool, err error)
	FailOrgActivityOutbox(context.Context, int64, error) error
	PruneOrgActivityOutbox(context.Context, time.Time) (int64, error)
}

// OrgActivityEnvMutationStore provides the first transactionally-outboxed
// producers. Other activity-producing mutations can adopt this capability
// incrementally without widening Store or coupling resource APIs to the
// projection table.
type OrgActivityEnvMutationStore interface {
	OrgActivityOutboxStore
	UpsertAppEnvInScopeWithActivity(context.Context, string, string, string, string, string, OrgActivity) (int64, error)
	DeleteAppEnvInScopeWithActivity(context.Context, string, string, string, string, OrgActivity) (int64, error)
}

// OrgActivityDeploymentMutationStore atomically couples a deployment write
// with its timeline handoff. Source builds enqueue at the build-queue
// transaction boundary; image deployments enqueue with the deployment row.
type OrgActivityDeploymentMutationStore interface {
	OrgActivityOutboxStore
	CreateDeploymentWithActivity(context.Context, Deployment, OrgActivity) (Deployment, int64, error)
	CreateBuildWithIDAndActivity(context.Context, string, string, DeploymentKind, int64, string, OrgActivity) (Build, int64, error)
}

// OrgActivityDomainMutationStore atomically couples custom-domain ownership
// changes with their timeline handoff.
type OrgActivityDomainMutationStore interface {
	OrgActivityOutboxStore
	CreateCustomDomainIfUnderQuotaWithActivity(context.Context, string, string, string, int, int, OrgActivity) (CustomDomain, int64, error)
	CreateCustomDomainInEnvironmentIfUnderQuotaWithActivity(context.Context, string, string, string, string, int, int, OrgActivity) (CustomDomain, int64, error)
	DeleteCustomDomainWithActivity(context.Context, string, OrgActivity) (int64, error)
}

// OrgActivityCancellationMutationStore atomically couples deployment
// cancellation and its timeline handoff, including any cascading build
// cancellations.
type OrgActivityCancellationMutationStore interface {
	OrgActivityOutboxStore
	CancelDeploymentTxWithActivity(context.Context, string, string, CancelReason, OrgActivity) (Deployment, []string, int64, error)
}

// OrgActivityAppLifecycleMutationStore atomically couples app lifecycle
// changes with their timeline handoff. The activity's app and organization
// references are filled from the app row returned by the mutation.
type OrgActivityAppLifecycleMutationStore interface {
	OrgActivityOutboxStore
	CreateAppIfUnderQuotaWithActivity(context.Context, App, api.Limits, OrgActivity) (App, int64, error)
	ScheduleAppDeletionWithActivity(context.Context, string, time.Time, OrgActivity) (App, int64, error)
	RestoreAppWithActivity(context.Context, string, api.Limits, OrgActivity) (App, int64, error)
}

// OrgActivityAppConfigBuilder constructs the activity from the exact app row
// immediately before and after a config update. Implementations call it while
// holding the mutation lock/transaction; it must be pure and must not perform
// I/O. Returning record=false suppresses no-op timeline events.
type OrgActivityAppConfigBuilder func(before, after App) (data json.RawMessage, record bool, err error)

// OrgActivityAppConfigMutationStore atomically couples a partial app config
// update with its workspace activity outbox handoff.
type OrgActivityAppConfigMutationStore interface {
	OrgActivityOutboxStore
	UpdateAppWithActivity(context.Context, string, UpdateAppParams, OrgActivity, OrgActivityAppConfigBuilder) (App, int64, error)
}

// OrgActivityAPIKeyMutationStore atomically couples organization-bound
// API-key creation, rotation, and revocation with the workspace activity
// timeline. Legacy keys without an organization binding remain outside this
// organization-level history.
type OrgActivityAPIKeyMutationStore interface {
	OrgActivityOutboxStore
	CreateOrgAPIKeyWithActivity(context.Context, string, string, []byte, string, []string, *time.Time, string, string, *string, OrgActivity) (APIKey, int64, error)
	RevokeOrgAPIKeyWithActivity(context.Context, string, string, OrgActivity) (APIKey, int64, error)
	RotateOrgAPIKeyWithActivity(context.Context, string, string, []byte, string, time.Duration, string, string, *string, OrgActivity) (newKey, oldKey APIKey, outboxID int64, err error)
}

// OrgActivityAccessMutationStore atomically couples invitation and membership
// changes with workspace activity. Invitation acceptance produces two facts:
// the invitation was accepted and the member joined.
type OrgActivityAccessMutationStore interface {
	OrgActivityOutboxStore
	CreateOrgInvitationWithActivity(context.Context, OrgInvitation, OrgActivity) (OrgInvitation, int64, error)
	ConsumeOrgInvitationWithActivity(context.Context, []byte, Account, OrgActivity, OrgActivity) (membership OrgMembership, invitation OrgInvitation, acceptedOutboxID, memberOutboxID int64, err error)
	RevokeOrgInvitationWithActivity(context.Context, string, string, string, OrgActivity) (OrgInvitation, int64, error)
	UpdateOrgMemberRoleWithActivity(context.Context, string, string, OrgRole, OrgActivity) (OrgMembership, int64, error)
	RemoveOrgMemberWithActivity(context.Context, string, string, OrgActivity) (OrgMembership, int64, error)
}

// OrgActivityOwnershipTransferMutationStore atomically records the owner
// swap and its workspace activity handoff.
type OrgActivityOwnershipTransferMutationStore interface {
	OrgActivityOutboxStore
	TransferOrgOwnershipWithActivity(context.Context, string, string, string, OrgActivity) (int64, error)
}

const (
	OrgActivityOutboxMaxAttempts = 12

	orgActivityOutboxInitialRetryDelay = 5 * time.Second
	orgActivityOutboxMaxRetryDelay     = 5 * time.Minute
	orgActivityOutboxDefaultLease      = 30 * time.Second

	orgActivityOutboxStatePending    = "pending"
	orgActivityOutboxStateProcessing = "processing"
	orgActivityOutboxStateDelivered  = "delivered"
	orgActivityOutboxStateDeadLetter = "dead_letter"
)

func orgActivityOutboxLease(lease time.Duration) time.Duration {
	if lease <= 0 {
		return orgActivityOutboxDefaultLease
	}
	return lease
}

func orgActivityOutboxRetryDelay(attempts int) time.Duration {
	delay := orgActivityOutboxInitialRetryDelay
	for i := 1; i < attempts; i++ {
		if delay >= orgActivityOutboxMaxRetryDelay/2 {
			return orgActivityOutboxMaxRetryDelay
		}
		delay *= 2
	}
	if delay > orgActivityOutboxMaxRetryDelay {
		return orgActivityOutboxMaxRetryDelay
	}
	return delay
}

func orgActivityOutboxFailureMessage(error) string { return "activity projection delivery failed" }

func bindOrgActivityToApp(entry OrgActivity, app App) (OrgActivity, error) {
	orgID := entry.OrgID
	if app.OrgID != "" {
		parsed, err := uuid.Parse(app.OrgID)
		if err != nil {
			return OrgActivity{}, errors.New("state: app activity requires an organization-owned app")
		}
		orgID = parsed
	}
	if orgID == uuid.Nil {
		return OrgActivity{}, errors.New("state: app activity requires an organization-owned app")
	}
	appID, err := uuid.Parse(app.ID)
	if err != nil {
		return OrgActivity{}, errors.New("state: app activity requires a valid app id")
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
	if entry.SourceID == "" {
		entry.SourceID = entry.SourceType + ":" + app.ID
	}
	return normalizeOrgActivity(entry, time.Now())
}

func deploymentOutcomeActivity(request OrgActivity, outcome, errorCode string, now time.Time) (OrgActivity, bool, error) {
	if request.DeploymentID == nil {
		return OrgActivity{}, false, errors.New("state: deployment request activity has no deployment id")
	}
	kind, sourceType, phase := "", "", ""
	switch request.Kind {
	case "deploy.requested":
		switch outcome {
		case "live":
			kind, sourceType, phase = "app.deployed", "deployment.live", "live"
		case "failed":
			kind, sourceType, phase = "deploy.failed", "deployment.failed", "failed"
		}
	case "deploy.rollback_requested":
		switch outcome {
		case "live":
			kind, sourceType, phase = "deploy.rolled_back", "rollback.completed", "completed"
		case "failed":
			kind, sourceType, phase = "deploy.rollback_failed", "rollback.failed", "failed"
		}
	}
	if kind == "" {
		return OrgActivity{}, false, nil
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(request.Data, &data); err != nil || data == nil {
		return OrgActivity{}, false, errors.New("state: invalid deployment request activity data")
	}
	phaseJSON, err := json.Marshal(phase)
	if err != nil {
		return OrgActivity{}, false, err
	}
	data["phase"] = phaseJSON
	if errorCode != "" {
		data["error_code"], err = json.Marshal(errorCode)
		if err != nil {
			return OrgActivity{}, false, err
		}
	}
	request.Data, err = json.Marshal(data)
	if err != nil {
		return OrgActivity{}, false, err
	}
	request.Kind = kind
	request.SourceType = sourceType
	request.OccurredAt = now.UTC()
	outcomeActivity, err := normalizeOrgActivity(request, now)
	return outcomeActivity, true, err
}
