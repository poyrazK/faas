// Package tasktoolrelease defines credential-free durable Task release reviews.
package tasktoolrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

var ErrInvalid = errors.New("invalid Task release intent")
var ErrChanged = errors.New("Task release intent or approval changed")

// RequiredSetup lists unverified owner setup, not resolved binding identities.
func RequiredSetup() []string {
	return []string{"runtime_postgres_binding", "separate_schema_owner_migration_binding", "shared_namespace_and_retained_payload_keys",
		"read_only_observer_binding", "worker_scoped_metrics_tokens", "live_worker_and_observer_readiness", "candidate_catalog_and_release_policy_verification"}
}

func Validate(in api.TaskToolReleaseIntent) error {
	body, err := json.Marshal(in)
	if err != nil || len(body) > api.TaskToolReleaseIntentMaxBytes || !toolpublication.ValidID(in.ReleaseFingerprint) ||
		in.WorkerRuntime != "node" || in.Protocol != api.MCPProtocolVersion || !api.ValidAppSlug(in.ObserverMetricApp) ||
		!toolpublication.ValidEndpointPath(in.Endpoint) || !slices.Equal(in.RequiredSetup, RequiredSetup()) ||
		strings.TrimSpace(in.Reason) == "" || utf8.RuneCountInString(in.Reason) > 280 || !utf8.ValidString(in.Reason) ||
		strings.IndexFunc(in.Reason, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 || len(in.Apps) < 3 || len(in.Apps) > api.TaskToolReleaseAppsMax {
		return ErrInvalid
	}
	// Reuse the established permission and endpoint bounds without claiming a
	// catalog or fixture was observed on a deployed Task stack.
	permissions := api.ToolPublicationIntent{App: "review", SourceSHA256: in.ReleaseFingerprint, FixturesSHA256: in.ReleaseFingerprint,
		EndpointPath: in.Endpoint, AccessMode: in.AccessMode, ToolScopes: in.ToolScopes, TestCases: []string{"review"}}
	if toolpublication.ValidateIntent(permissions) != nil {
		return ErrInvalid
	}
	roles, slugs, ids := map[string]int{}, map[string]bool{}, map[string]bool{}
	for _, app := range in.Apps {
		r := app.Review
		if !api.ValidAppSlug(app.App) || slugs[app.App] || ids[r.AppID] || uuid.Validate(r.AppID) != nil || !toolpublication.ValidID(r.ConfigurationHash) ||
			r.Scope != api.DefaultEnvScope || r.EnvironmentID != "" || r.ProjectID != "" || r.Protected || r.WorkloadRevision != 0 || r.WorkloadConfigHash != "" || r.ServingDeploymentIDs == nil || len(r.ServingDeploymentIDs) > 1 {
			return ErrInvalid
		}
		if r.LatestDeploymentID != "" && uuid.Validate(r.LatestDeploymentID) != nil {
			return ErrInvalid
		}
		for _, id := range r.ServingDeploymentIDs {
			if uuid.Validate(id) != nil {
				return ErrInvalid
			}
		}
		switch app.Role {
		case "web", "worker", "observer", "previous_worker":
		default:
			return ErrInvalid
		}
		roles[app.Role]++
		slugs[app.App], ids[r.AppID] = true, true
	}
	if roles["web"] != 1 || roles["worker"] != 1 || roles["observer"] != 1 || !slugs[in.ObserverMetricApp] {
		return ErrInvalid
	}
	return nil
}

// New binds the immutable intent to its native account and requesting key.
func New(account, key, id string, in api.TaskToolReleaseIntent, now time.Time) (api.TaskToolReleaseIntentRecord, error) {
	var out api.TaskToolReleaseIntentRecord
	if uuid.Validate(account) != nil || uuid.Validate(key) != nil || !toolpublication.ValidID(id) || Validate(in) != nil {
		return out, ErrInvalid
	}
	body, _ := json.Marshal(struct {
		Account, Key, ID string
		Intent           api.TaskToolReleaseIntent
	}{account, key, id, in})
	sum := sha256.Sum256(body)
	return api.TaskToolReleaseIntentRecord{Version: 1, ID: id, AccountID: account, RequesterKeyID: key, IntentSHA256: hex.EncodeToString(sum[:]),
		Intent: in, Status: "pending", CreatedAt: now.UTC(), ExpiresAt: now.UTC().Add(api.TaskToolReleaseApprovalTTL)}, nil
}

func View(in api.TaskToolReleaseIntentRecord, now time.Time) api.TaskToolReleaseIntentRecord {
	in.BindingsVerified, in.ReadyToApply = false, false
	if (in.Status == "pending" || in.Status == "approved") && !now.Before(in.ExpiresAt) {
		in.Status = "expired"
	}
	return in
}

// Check permanently invalidates an observed mismatch; restoring the old source
// later does not revive approval. An empty fingerprint checks native pins only.
func Check(in api.TaskToolReleaseIntentRecord, fingerprint string, nativeFresh bool, now time.Time) api.TaskToolReleaseIntentRecord {
	in = View(in, now)
	if (in.Status == "pending" || in.Status == "approved") && (!nativeFresh || fingerprint != "" && fingerprint != in.Intent.ReleaseFingerprint) {
		in.Status = "stale"
	}
	return in
}

// Decide never grants executable release authority and never extends a deadline.
func Decide(in api.TaskToolReleaseIntentRecord, decision api.TaskToolReleaseIntentDecision, now time.Time) (api.TaskToolReleaseIntentRecord, error) {
	in = View(in, now)
	if decision.IntentSHA256 != in.IntentSHA256 || decision.ReleaseFingerprint != in.Intent.ReleaseFingerprint {
		return in, ErrChanged
	}
	target := map[string]string{"approve": "approved", "reject": "rejected", "revoke": "revoked"}[decision.Decision]
	if target == "" {
		return in, ErrInvalid
	}
	if in.Status == target {
		return in, nil
	}
	if in.Status != "pending" && !(in.Status == "approved" && target == "revoked") {
		return in, ErrChanged
	}
	now = now.UTC()
	in.Status, in.DecidedAt = target, &now
	return in, nil
}
