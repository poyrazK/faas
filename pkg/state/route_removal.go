package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRouteRemovalPolicyRevision = errors.New("route removal policy changed")

type RouteRemovalBlockedError struct{ Reason string }

func (e *RouteRemovalBlockedError) Error() string { return "route removal blocked: " + e.Reason }

type RouteRemovalContract struct {
	Deployment Deployment
	Document   []byte
	SHA256     string
	Truncated  bool
	CapturedAt time.Time
	UpdatedAt  time.Time
}
type RouteRemovalApprovalValidator func(RouteRemovalContract, RouteRemovalContract, []api.RouteRemovalMapping) error

// The validator checks compatibility; persistence rechecks its hash binding,
// policy revision, current baseline and observed traffic in one transaction.
type RouteRemovalStore interface {
	GetRouteRemovalPolicy(context.Context, string, string) (api.RouteRemovalPolicy, error)
	SetRouteRemovalPolicy(context.Context, string, string, api.SetRouteRemovalPolicyRequest) (api.RouteRemovalPolicy, error)
	ApproveRouteRemoval(context.Context, string, string, string, api.ApproveRouteRemovalRequest, RouteRemovalApprovalValidator) (api.RouteRemovalApproval, error)
	CheckRouteRemoval(context.Context, string, string, string) (api.RouteRemovalCheck, error)
}

func defaultRouteRemovalPolicy(appID string) api.RouteRemovalPolicy {
	return api.RouteRemovalPolicy{AppID: appID, Mode: "report", GracePeriod: "720h0m0s", MaxApprovalAge: "1h0m0s"}
}
func validateRouteRemovalPolicy(r api.SetRouteRemovalPolicyRequest) (time.Duration, time.Duration, error) {
	if r.ExpectedRevision == nil || *r.ExpectedRevision < 0 || *r.ExpectedRevision >= 2147483647 || (r.Mode != "report" && r.Mode != "enforce") {
		return 0, 0, ErrInvalidArgument
	}
	grace, age := 30*24*time.Hour, time.Hour
	var err error
	if r.GracePeriod != "" {
		grace, err = time.ParseDuration(r.GracePeriod)
		if err != nil {
			return 0, 0, ErrInvalidArgument
		}
	}
	if r.MaxApprovalAge != "" {
		age, err = time.ParseDuration(r.MaxApprovalAge)
		if err != nil {
			return 0, 0, ErrInvalidArgument
		}
	}
	if grace < time.Hour || grace > 90*24*time.Hour || age < time.Minute || age > 72*time.Hour || grace%time.Second != 0 || age%time.Second != 0 {
		return 0, 0, ErrInvalidArgument
	}
	return grace, age, nil
}
func NormalizeRouteRemovalMappings(m []api.RouteRemovalMapping) ([]api.RouteRemovalMapping, string, error) {
	if len(m) == 0 || len(m) > 2000 {
		return nil, "", ErrInvalidArgument
	}
	out := append([]api.RouteRemovalMapping(nil), m...)
	seen := map[string]bool{}
	for i := range out {
		r := &out[i]
		r.Method = strings.ToUpper(strings.TrimSpace(r.Method))
		r.SuccessorMethod = strings.ToUpper(strings.TrimSpace(r.SuccessorMethod))
		if !routeRemovalMethod(r.Method) || !routeRemovalMethod(r.SuccessorMethod) || !strings.HasPrefix(r.Path, "/") || !strings.HasPrefix(r.SuccessorPath, "/") || len(r.Path) > 4096 || len(r.SuccessorPath) > 4096 || strings.ContainsAny(r.Path+r.SuccessorPath, "\r\n\x00") || seen[r.Method+" "+r.Path] {
			return nil, "", ErrInvalidArgument
		}
		seen[r.Method+" "+r.Path] = true
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Method+" "+out[i].Path < out[j].Method+" "+out[j].Path })
	b, _ := json.Marshal(out)
	return out, fmt.Sprintf("%x", sha256.Sum256(b)), nil
}
func routeRemovalMethod(m string) bool {
	switch m {
	case "GET", "PUT", "POST", "DELETE", "OPTIONS", "HEAD", "PATCH", "TRACE":
		return true
	}
	return false
}
func RouteRemovalOperations(doc []byte) (map[string]bool, error) {
	var root struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if json.Unmarshal(doc, &root) != nil || !strings.HasPrefix(root.OpenAPI, "3.") || root.Paths == nil {
		return nil, &RouteRemovalBlockedError{"contract_unavailable"}
	}
	ops := map[string]bool{}
	for p, item := range root.Paths {
		if !strings.HasPrefix(p, "/") {
			return nil, &RouteRemovalBlockedError{"invalid_path"}
		}
		if _, ok := item["$ref"]; ok {
			return nil, &RouteRemovalBlockedError{"path_reference_unsupported"}
		}
		for method, op := range item {
			if routeRemovalMethod(strings.ToUpper(method)) {
				var object map[string]json.RawMessage
				if json.Unmarshal(op, &object) != nil || object == nil {
					return nil, &RouteRemovalBlockedError{"invalid_operation"}
				}
				ops[strings.ToUpper(method)+" "+p] = true
			}
		}
	}
	return ops, nil
}
func validateRouteRemovalRequest(r api.ApproveRouteRemovalRequest) error {
	if r.ExpectedPolicyRevision == nil || *r.ExpectedPolicyRevision <= 0 || !r.AcknowledgeObservedOnly {
		return ErrInvalidArgument
	}
	for _, id := range []string{r.BaselineDeploymentID, r.CandidateDeploymentID} {
		v, e := uuid.Parse(id)
		if e != nil || v == uuid.Nil || v.String() != id {
			return ErrInvalidArgument
		}
	}
	if r.BaselineDeploymentID == r.CandidateDeploymentID {
		return ErrInvalidArgument
	}
	for _, h := range []string{r.BaselineContractSHA256, r.CandidateContractSHA256} {
		if len(h) != 64 || strings.Trim(h, "0123456789abcdef") != "" {
			return ErrInvalidArgument
		}
	}
	_, _, err := NormalizeRouteRemovalMappings(r.Mappings)
	return err
}

// WithRouteRemovalActor stamps the authenticated actor for transactional policy
// history. It is set by the API authentication boundary, never a request field.
type routeRemovalActorKey struct{}

func WithRouteRemovalActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, routeRemovalActorKey{}, actor)
}
func routeRemovalActor(ctx context.Context, accountID string) string {
	if actor, ok := ctx.Value(routeRemovalActorKey{}).(string); ok && actor != "" {
		return actor
	}
	return "account:" + accountID
}

func routeRemovalProductionScope(scope string) bool {
	scope = normalizedDeploymentScope(scope)
	return scope == DefaultEnvScope || scope == "prod" || scope == "production"
}

// Guidance is advisory; traffic transitions always recheck database evidence.
func routeRemovalCheckGuidance(check *api.RouteRemovalCheck) {
	for _, blocker := range check.Blockers {
		switch blocker {
		case "grace_period_not_elapsed":
			check.NextActions = append(check.NextActions, "Keep the staged baseline serving until earliest_approval_at, then request a fresh approval.")
		case "fresh_authenticated_approval_required":
			check.NextActions = append(check.NextActions, "Request an owner or admin approval for the current policy revision and exact captured contracts; expired or changed evidence requires a new approval.")
		case "old_route_observed":
			check.NextActions = append(check.NextActions, "Migrate remaining clients and wait for a full quiet grace period before requesting another approval.")
		case "telemetry_coverage_missing":
			check.NextActions = append(check.NextActions, "Upgrade or restore coverage reporting on every active gateway node, then collect a full healthy grace period.")
		case "telemetry_coverage_stale":
			check.NextActions = append(check.NextActions, "Restore gateway-to-apid heartbeat delivery; after an outage, collect a new full healthy grace period.")
		case "telemetry_disabled", "telemetry_sampled":
			check.NextActions = append(check.NextActions, "Enable unsampled request telemetry on every active gateway and its receiver, then collect a full healthy grace period.")
		case "telemetry_ingestion_pending":
			check.NextActions = append(check.NextActions, "Resolve this app's ingestion backlog or unattributed pending rows and wait for delivery to catch up, then recheck.")
		case "telemetry_window_incomplete":
			check.NextActions = append(check.NextActions, "Collect a new uninterrupted grace period for this app after its dropped or backlogged events, or after a shared gateway restart or outage; allow two minutes for bucket aggregation.")
		case "contract_unavailable", "path_reference_or_invalid_item", "invalid_operation":
			check.NextActions = append(check.NextActions, "Capture complete OpenAPI 3 contracts with inline path items for both deployments, then recheck.")
		}
	}
	if check.Status == "passed" {
		check.NextActions = []string{"Promote before approval_valid_until; new old-route traffic or changed evidence will block the transition."}
	}
}
