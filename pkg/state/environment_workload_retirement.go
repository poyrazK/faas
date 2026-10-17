package state

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// ErrEnvironmentWorkloadRetired is returned when durable scoped admission is closed.
var ErrEnvironmentWorkloadRetired = errors.New("workload is retired in this environment")

type EnvironmentWorkloadRetirement = api.EnvironmentWorkloadRetirement

// Begin publishes only retained retirement fences. Reconciliation, gateway
// acknowledgements and original execution drain are independent completion
// conditions. These methods are apid intent operations, never VM operations.
type EnvironmentGitOpsWorkloadRetirementStore interface {
	BeginEnvironmentGitOpsWorkloadRetirements(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) ([]EnvironmentGitOpsStep, error)
	ActivateEnvironmentGitOpsEmptyRetirement(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) (bool, error)
	PendingEnvironmentGitOpsWorkloadRetirements(context.Context, EnvironmentGitOpsLease, []string) ([]EnvironmentWorkloadRetirement, error)
	AcknowledgeEnvironmentGitOpsWorkloadRetirement(context.Context, EnvironmentGitOpsLease, string, int64, string) error
	ReconcileEnvironmentGitOpsWorkloadRetirements(context.Context, EnvironmentGitOpsLease) (bool, error)
	ListEnvironmentGitOpsWorkloadRetirements(context.Context, string, string) ([]EnvironmentWorkloadRetirement, error)
}

// Admission checks use the physical app/environment identity. A logical rename
// or a retained release pin cannot provide another path into a retired lane.
type EnvironmentWorkloadRetirementAdmissionStore interface {
	EnvironmentWorkloadAdmissionRetired(context.Context, string, string) (bool, error)
}

// Project production traffic also arrives through the legacy default scope.
// A named project environment cannot use "default" as its own slug.
func workloadRetirementAdmissionScope(scope string) string {
	if scope == "" || scope == DefaultEnvScope {
		return "production"
	}
	return scope
}

func cloneWorkloadRetirement(in EnvironmentWorkloadRetirement) EnvironmentWorkloadRetirement {
	out := in
	out.DeploymentIDs = slices.Clone(in.DeploymentIDs)
	out.QueueBindingIDs = slices.Clone(in.QueueBindingIDs)
	out.ExpectedGateways = slices.Clone(in.ExpectedGateways)
	out.AcknowledgedGateways = slices.Clone(in.AcknowledgedGateways)
	if in.RetiredAt != nil {
		at := *in.RetiredAt
		out.RetiredAt = &at
	}
	if in.RecoveredAt != nil {
		at := *in.RecoveredAt
		out.RecoveredAt = &at
	}
	return out
}

func workloadRetirementResources(plan environmentsync.Plan) []string {
	var resources []string
	for _, change := range plan.Changes {
		if change.Path == "presence" && change.Action == "remove" {
			resources = append(resources, change.Resource)
		}
	}
	slices.Sort(resources)
	return slices.Compact(resources)
}

// The journal ID participates in the reviewed physical identity. Mutable drain
// and acknowledgement progress must not invalidate an already qualified graph.
func workloadRetirementResource(resource string) string { return resource + "/retirement" }

func projectWorkloadRetirements(out *EnvironmentGitOpsObservation, snapshot gitOpsIntentSnapshot, desired environmentsync.DesiredState) {
	for _, r := range snapshot.Retirements {
		out.State.ResourceIDs[workloadRetirementResource(r.Resource)] = r.ID
		name := strings.TrimPrefix(r.Resource, "workload/")
		_, wanted := desired.Definition.Workloads[name]
		if r.Phase == "cancelled" || r.Phase == "recovered" {
			continue
		}
		if wanted {
			if r.Phase == "planned" {
				continue
			}
			if desired.Definition.Workloads[name].Source == nil {
				out.State.Unsupported = append(out.State.Unsupported, r.Resource+": recovery requires an explicit reviewed workload source")
			}
			if r.Phase == "retiring" {
				out.State.Unsupported = append(out.State.Unsupported, r.Resource+": retirement must finish before recovery")
			}
			if !workloadRetirementRecoveryMatches(desired.Definition.WorkloadRecoveries[name], r.AppID) {
				out.State.Unsupported = append(out.State.Unsupported, r.Resource+": recovery requires its original app UUID")
			}
			continue
		}
		// Retained configuration is kept physically, but its derived baselines
		// no longer take part in the desired graph's serving identity.
		for name := range desired.Definition.Workloads {
			if name != strings.TrimPrefix(r.Resource, "workload/") && out.State.ResourceIDs["workload/"+name] == r.AppID {
				out.State.Unsupported = append(out.State.Unsupported, r.Resource+": retained identity cannot be reused under another workload name")
			}
		}
		for key := range out.State.ResourceIDs {
			if strings.HasPrefix(key, r.Resource+"/") && key != workloadRetirementResource(r.Resource) {
				delete(out.State.ResourceIDs, key)
			}
		}
		for _, owner := range out.Owners {
			if owner.Resource == r.Resource && owner.Manager != snapshot.SourceID {
				out.State.Unsupported = append(out.State.Unsupported, r.Resource+": retirement conflicts with another field manager")
				break
			}
		}
		for _, override := range snapshot.Overrides {
			if override.Resource == r.Resource && override.ExpiresAt.After(time.Now()) {
				out.State.Unsupported = append(out.State.Unsupported, r.Resource+": retirement conflicts with an active override")
			}
		}
		out.State.Fields = slices.DeleteFunc(out.State.Fields, func(f environmentsync.Field) bool { return f.Resource == r.Resource })
		out.Owners = slices.DeleteFunc(slices.Clone(out.Owners), func(f environmentsync.Ownership) bool {
			return f.Resource == r.Resource && f.Manager == snapshot.SourceID
		})
	}
}

type workloadRetirementInventory struct {
	RouteDeploymentID  string   `json:"route_deployment_id"`
	DeploymentIDs      []string `json:"deployment_ids"`
	QueueBindingIDs    []string `json:"queue_binding_ids"`
	JobID              string   `json:"job_id"`
	UnmanagedExecution bool     `json:"unmanaged_execution"`
	UnmanagedBinding   bool     `json:"unmanaged_binding"`
}

// CheckEnvironmentWorkloadAdmission also protects warm admissions, which need
// not insert an instance row and therefore cannot rely on the SQL insert guard.
func CheckEnvironmentWorkloadAdmission(ctx context.Context, store any, appID, scope string) error {
	admission, ok := store.(EnvironmentWorkloadRetirementAdmissionStore)
	if !ok {
		return nil
	}
	blocked, err := admission.EnvironmentWorkloadAdmissionRetired(ctx, appID, scope)
	if err != nil {
		return err
	}
	if blocked {
		return ErrEnvironmentWorkloadRetired
	}
	return nil
}

func activeWorkloadRetirement(snapshot gitOpsIntentSnapshot, resource string) (EnvironmentWorkloadRetirement, bool) {
	for _, r := range snapshot.Retirements {
		if r.Resource == resource && r.Phase != "recovered" && r.Phase != "cancelled" {
			return r, true
		}
	}
	return EnvironmentWorkloadRetirement{}, false
}

func observeWorkloadRetirementImpacts(out *EnvironmentGitOpsObservation, snapshot gitOpsIntentSnapshot, desired environmentsync.DesiredState) {
	for _, owner := range snapshot.Owners {
		if owner.Manager != snapshot.SourceID || owner.Path != "presence" {
			continue
		}
		if _, wanted := desired.Definition.Workloads[strings.TrimPrefix(owner.Resource, "workload/")]; wanted {
			continue
		}
		for _, app := range snapshot.Apps {
			if app.ID != out.State.ResourceIDs[owner.Resource] {
				continue
			}
			impact := api.EnvironmentWorkloadRetirementImpact{Resource: owner.Resource, AppID: app.ID, PendingInvocations: app.PendingInvocations, InFlightInvocations: app.InFlightInvocations, QueuedTasks: app.QueuedTasks, InFlightTasks: app.InFlightTasks}
			if app.Routes != nil {
				impact.Routes = slices.Clone(app.Routes.DeclaredRoutes)
			}
			if app.WorkloadIntent != nil {
				impact.JobID = app.WorkloadIntent.JobID
			}
			for _, binding := range app.QueueBindings {
				impact.QueueBindingIDs = append(impact.QueueBindingIDs, binding.ID)
				for _, consumer := range binding.Consumers {
					impact.ConsumerIDs = append(impact.ConsumerIDs, consumer.ID)
				}
			}
			slices.Sort(impact.QueueBindingIDs)
			slices.Sort(impact.ConsumerIDs)
			out.State.RetirementImpacts = append(out.State.RetirementImpacts, impact)
		}
	}
}

func workloadRetirementRecoveryMatches(reviewed, original string) bool {
	id, err := uuid.Parse(original)
	return err == nil && id != uuid.Nil && reviewed == id.String()
}

func workloadRetirementInventoryMatches(r EnvironmentWorkloadRetirement, current workloadRetirementInventory) bool {
	return !current.UnmanagedExecution && !current.UnmanagedBinding && slices.Equal(r.DeploymentIDs, current.DeploymentIDs) && slices.Equal(r.QueueBindingIDs, current.QueueBindingIDs) && r.JobID == current.JobID
}
