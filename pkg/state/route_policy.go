package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrRoutePolicyStale      = errors.New("route policy plan is stale")
	ErrRoutePolicyUnresolved = errors.New("route policy requirements remain unresolved")
	ErrRoutePolicyKeyReused  = errors.New("route policy idempotency key reused for another request")
)

type RoutePolicySnapshot struct {
	SavedRequirements *api.SavedRouteRequirements
	Account           Account
	App               App
	Rules             []api.EdgeRuleResponse
	Contract          *RoutePolicyContract
}

// Captured bytes are loaded through the same snapshot/transaction as policy.
type RoutePolicyContract struct {
	DeploymentID string
	Doc          []byte
	SHA256       string
	Truncated    bool
}

// The planner must be pure: it runs while store transaction locks are held.
type RoutePolicyPlanner func(RoutePolicySnapshot) (api.RoutePolicyPlan, error)

type RoutePolicyStore interface {
	PlanRoutePolicy(context.Context, string, string, api.RoutePolicyPlanRequest, RoutePolicyPlanner) (api.RoutePolicyPlan, error)
	ApplyRoutePolicy(context.Context, string, string, string, api.RoutePolicyApplyRequest, RoutePolicyPlanner) (api.RoutePolicyReceipt, bool, error)
	GetRoutePolicyReceipt(context.Context, string, string, string) (api.RoutePolicyReceipt, error)
	FindRoutePolicyReceipt(context.Context, string, string, string, api.RoutePolicyApplyRequest) (api.RoutePolicyReceipt, error)
}

var (
	_ RoutePolicyStore = (*MemStore)(nil)
	_ RoutePolicyStore = (*PgStore)(nil)
)

func RoutePolicyRequestSHA256(request api.RoutePolicyApplyRequest) string {
	body, _ := json.Marshal(request)
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

func ValidateRoutePolicyApply(key string, request api.RoutePolicyApplyRequest) error {
	if err := ValidateRoutePolicySource(request.RoutePolicyPlanRequest); err != nil {
		return err
	}
	if request.Saved && request.ExpectedRevision == nil {
		return errors.New("saved apply requires the reviewed expected_revision")
	}
	if key == "" || len(key) > api.RoutePolicyIdempotencyKeyMaxBytes || strings.IndexFunc(key, unicode.IsControl) >= 0 {
		return errors.New("a bounded Idempotency-Key without control characters is required")
	}
	if !request.Confirm || len(request.ExpectedPlanSHA256) != 64 ||
		strings.IndexFunc(request.ExpectedPlanSHA256, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') }) >= 0 {
		return errors.New("confirm and expected_plan_sha256 are required")
	}
	return nil
}

// Source selection is checked by the stores as well as the HTTP boundary.
func ValidateRoutePolicySource(request api.RoutePolicyPlanRequest) error {
	if !request.Saved {
		if request.ExpectedRevision != nil {
			return errors.New("expected_revision requires saved requirements")
		}
		return nil
	}
	config := request.Requirements
	if config.Version != 0 || config.Routes != nil || config.Groups != nil || config.Public != nil {
		return errors.New("saved and inline requirements are mutually exclusive")
	}
	if request.ThrottleBurst < 0 {
		return errors.New("throttle_burst must be nonnegative")
	}
	return ValidateCheckRouteRequirements(api.CheckRouteRequirementsRequest{DeploymentID: request.DeploymentID, ExpectedRevision: request.ExpectedRevision})
}

func bindRoutePolicySavedRequirements(snapshot *RoutePolicySnapshot, request api.RoutePolicyPlanRequest, saved api.SavedRouteRequirements) error {
	if err := validateSavedRouteRequirements(saved, snapshot.App.ID); err != nil {
		return err
	}
	if err := checkSavedRevision(saved, request.ExpectedRevision); err != nil {
		return err
	}
	snapshot.SavedRequirements = &saved
	return nil
}

func routePolicyPlanForApply(snapshot RoutePolicySnapshot, request api.RoutePolicyApplyRequest, planner RoutePolicyPlanner) (api.RoutePolicyPlan, error) {
	if !snapshot.Account.MayDeploy() {
		return api.RoutePolicyPlan{}, ErrRoutePolicyUnresolved
	}
	plan, err := planner(snapshot)
	if err != nil {
		return plan, err
	}
	if plan.AppID != snapshot.App.ID || plan.SHA256 != request.ExpectedPlanSHA256 {
		return plan, ErrRoutePolicyStale
	}
	if (plan.Status != "ready" && plan.Status != "no_changes") || len(plan.Unresolved) != 0 {
		return plan, ErrRoutePolicyUnresolved
	}
	return plan, nil
}

func storedRoutePolicyAction(kind string, body json.RawMessage) json.RawMessage {
	wrapped, _ := json.Marshal(map[string]any{"kind": kind, kind: body})
	return wrapped
}

// Stage the entire batch before writing. Rule IDs and updates are generated
// from the locked server plan, never accepted from a submitted artifact.
func stageRoutePolicy(snapshot RoutePolicySnapshot, plan api.RoutePolicyPlan, now time.Time) (RoutePolicySnapshot, []api.RoutePolicyAppliedChange, error) {
	snapshot.Rules = append([]api.EdgeRuleResponse(nil), snapshot.Rules...)
	applied := make([]api.RoutePolicyAppliedChange, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		if change.Kind != "throttle" && change.Kind != "budget" {
			return snapshot, nil, ErrRoutePolicyUnresolved
		}
		switch change.Operation {
		case "create":
			if change.Create == nil || change.Create.Priority == nil || change.Create.Kind != change.Kind ||
				*change.Create.Priority < 0 || *change.Create.Priority > api.RoutePlanPriorityMax {
				return snapshot, nil, ErrRoutePolicyUnresolved
			}
			request := change.Create
			rule := api.EdgeRuleResponse{ID: uuid.NewString(), AccountID: snapshot.Account.ID, AppID: snapshot.App.ID,
				MatchHost: request.MatchHost, MatchPath: request.MatchPath, MatchMethods: request.MatchMethods,
				MatchHeaders: request.MatchHeaders, Priority: *request.Priority, Enabled: true, Kind: change.Kind,
				Action: storedRoutePolicyAction(change.Kind, request.Action), ValidateMode: api.ValidateModeBlock, CreatedAt: now, UpdatedAt: now}
			snapshot.Rules = append(snapshot.Rules, rule)
			applied = append(applied, api.RoutePolicyAppliedChange{Operation: change.Operation, Kind: change.Kind, RuleID: rule.ID})
		case "update":
			if change.Update == nil || change.Update.Action == nil {
				return snapshot, nil, ErrRoutePolicyUnresolved
			}
			found := false
			for i := range snapshot.Rules {
				rule := &snapshot.Rules[i]
				if rule.ID != change.RuleID {
					continue
				}
				if rule.AppID != snapshot.App.ID || rule.AccountID != snapshot.Account.ID || rule.Kind != change.Kind {
					return snapshot, nil, ErrNotFound
				}
				rule.Action, rule.UpdatedAt = storedRoutePolicyAction(change.Kind, *change.Update.Action), now
				found = true
			}
			if !found {
				return snapshot, nil, ErrNotFound
			}
			applied = append(applied, api.RoutePolicyAppliedChange{Operation: change.Operation, Kind: change.Kind, RuleID: change.RuleID})
		default:
			return snapshot, nil, ErrRoutePolicyUnresolved
		}
	}
	return snapshot, applied, nil
}

func verifyRoutePolicy(snapshot RoutePolicySnapshot, planner RoutePolicyPlanner, plan api.RoutePolicyPlan, changes []api.RoutePolicyAppliedChange, now time.Time) (api.RoutePolicyReceipt, error) {
	verified, err := planner(snapshot)
	if err != nil {
		return api.RoutePolicyReceipt{}, fmt.Errorf("verify committed route policy: %w", err)
	}
	if verified.Status != "no_changes" || verified.Before.Status != "satisfied" {
		return api.RoutePolicyReceipt{}, ErrRoutePolicyUnresolved
	}
	verified.Before.PolicyScope = "committed_app"
	return api.RoutePolicyReceipt{ID: uuid.NewString(), AppID: snapshot.App.ID, PlanSHA256: plan.SHA256,
		AppliedAt: now, Changes: changes, Verification: verified.Before}, nil
}

type routePolicyStoredReceipt struct {
	RequestSHA256 string
	Receipt       api.RoutePolicyReceipt
}

func copyRoutePolicyReceipt(receipt api.RoutePolicyReceipt) api.RoutePolicyReceipt {
	body, _ := json.Marshal(receipt)
	var copy api.RoutePolicyReceipt
	_ = json.Unmarshal(body, &copy)
	return copy
}
