package mcpmanagement

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Detached work must not bypass the process's existing CLI worker bound.
var asyncProvisionSlots = make(chan struct{}, cap(hostedCLISlots))

type provisionApplyArgs struct {
	PlanID string `json:"plan_id"`
	Async  bool   `json:"async,omitempty" jsonschema:"Return a status handle immediately; poll gregale_get_provision with the same plan_id"`
}

type provisionWork struct {
	cancel  context.CancelFunc
	status  string
	problem *ToolError
}

func (m *management) applyProvisionRequest(ctx context.Context, a provisionApplyArgs) (any, error) {
	if !a.Async {
		return m.applyProvision(ctx, applyArgs{PlanID: a.PlanID})
	}
	m.mu.Lock()
	if m.provisions == nil {
		m.provisions = map[string]*provisionWork{}
	}
	if work, ok := m.provisions[a.PlanID]; ok && work.status == "running" {
		if p := m.plans[a.PlanID]; p != nil {
			if _, err := m.checkedPlan(ctx, p.ID, p.Action); err != nil {
				m.mu.Unlock()
				return nil, err
			}
		}
		out := m.provisionStatusLocked(a.PlanID)
		m.mu.Unlock()
		return out, nil
	}
	p := m.plans[a.PlanID]
	if p != nil && p.localReceipt != nil {
		out, err := m.completedReceipt(ctx, p)
		m.mu.Unlock()
		return out, err
	}
	if p == nil || p.Provision == nil {
		m.mu.Unlock()
		return nil, invalid("invalid_plan", "unknown provisioning plan")
	}
	if _, err := m.checkedPlan(ctx, p.ID, p.Action); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	select {
	case asyncProvisionSlots <- struct{}{}:
	default:
		m.mu.Unlock()
		wait := int64(2)
		return nil, &api.APIError{Problem: api.Problem{Status: 429, Code: "provision_capacity", Title: "Provisioning capacity reached", Detail: "Retry the same plan shortly", RetryAfterSeconds: &wait}}
	}
	started := false
	defer func() {
		if !started {
			<-asyncProvisionSlots
		}
	}()
	p.ExpiresAt = time.Now().Add(provisionTimeout + releasePlanTTL)
	if p.Provision.Native != nil {
		p.Provision.Native.Phase = "queued"
		p.attempted = true
		if err := m.admitNativeProvisionLocked(p); err != nil {
			m.mu.Unlock()
			return nil, err
		}
	}
	// A request may disconnect after accepting asynchronous work. Keep the
	// authorization expiry and lifecycle cancellation while detaching that
	// transport cancellation; the credential is rechecked by native API calls.
	base := context.WithoutCancel(ctx)
	workCtx, cancel := context.WithTimeout(base, provisionTimeout)
	if expiry, ok := ctx.Value(authorizationLeaseExpiresKey{}).(time.Time); ok {
		var stop context.CancelFunc
		workCtx, stop = context.WithDeadline(workCtx, expiry)
		old := cancel
		cancel = func() { stop(); old() }
	}
	workCtx, finish, err := m.lifecycle.enter(workCtx, false)
	if err != nil {
		cancel()
		m.mu.Unlock()
		return nil, err
	}
	workCtx, finishGrant, err := m.grantLifecycle.enter(workCtx, false)
	if err != nil {
		finish()
		cancel()
		m.mu.Unlock()
		return nil, err
	}
	work := &provisionWork{cancel: cancel, status: "running"}
	m.provisions[a.PlanID] = work
	m.mu.Unlock()
	go func() {
		defer func() { <-asyncProvisionSlots }()
		defer cancel()
		defer finish()
		defer finishGrant()
		_, err := m.applyProvision(workCtx, applyArgs{PlanID: a.PlanID})
		m.mu.Lock()
		defer m.mu.Unlock()
		work.status = "completed"
		if err != nil {
			work.status = "failed"
			problem := m.toolError(err).StructuredContent.(ToolError)
			work.problem = &problem
			if p := m.plans[a.PlanID]; p != nil && p.Provision != nil && p.Provision.Native != nil {
				n := p.Provision.Native
				n.Problem = &problem
				if n.CancelRequested {
					n.Phase = "cancelled"
				} else {
					n.Phase = "interrupted"
				}
				_ = m.persistPlans()
			}
		}
	}()
	started = true
	seconds := int64(2)
	recovery := m.recoveryTool(Recovery{Action: "inspect", AfterSeconds: &seconds}, "gregale_get_provision", map[string]any{"plan_id": a.PlanID})
	return map[string]any{"schema_version": recoveryContractVersion, "plan_id": a.PlanID, "status": "running", "recovery": recovery, "next": "Poll gregale_get_provision with this plan_id. Cancellation does not undo resources already created."}, nil
}

func (m *management) provisionStatusLocked(id string) any {
	out := map[string]any{"schema_version": recoveryContractVersion, "plan_id": id, "status": "planned"}
	if w := m.provisions[id]; w != nil {
		out["status"], out["problem"] = w.status, w.problem
	}
	if p := m.plans[id]; p != nil && p.Provision != nil && p.Provision.Native != nil {
		n := p.Provision.Native
		out["operation_id"], out["phase"] = p.ID, n.Phase
		if n.ResourceID != "" {
			out["resource_id"] = n.ResourceID
		}
		if n.BindingID != "" {
			out["binding_id"] = n.BindingID
		}
		if w := m.provisions[id]; w == nil || w.status != "running" {
			out["status"], out["problem"] = n.Phase, n.Problem
			if n.Phase != "planned" && n.Phase != "completed" && n.Phase != "cancelled" {
				out["status"] = "interrupted"
			}
			if n.Phase != "completed" {
				out["next"] = map[string]any{"tool": "gregale_apply_provision", "arguments": map[string]any{"plan_id": id, "async": true}}
			}
		}
	}
	if p := m.plans[id]; p != nil && p.localReceipt != nil {
		out["status"], out["result"] = "completed", p.localReceipt
	}
	if p := m.plans[id]; p != nil {
		verification, _ := m.planVerification(p)
		out["verification"] = verification
		status, _ := out["status"].(string)
		r := m.receiptRecovery(p, status)
		if status == "running" || status == "cancellation_requested" {
			seconds := int64(2)
			r = m.recoveryTool(Recovery{Action: "inspect", AfterSeconds: &seconds}, "gregale_get_provision", map[string]any{"plan_id": id})
		} else if p.Provision != nil && p.Provision.Native != nil && p.Provision.Native.Problem != nil && status != "completed" && status != "cancelled" {
			problem := p.Provision.Native.Problem
			if recoveryForError(*problem).Action == "request_access" {
				r = Recovery{Action: "request_access"}
			}
			if problem.Code == "provision_failed" {
				r = Recovery{Action: "inspect"}
			}
		}
		out["recovery"] = r
		if problem, ok := out["problem"].(*ToolError); ok && problem != nil {
			copyProblem := *problem
			copyProblem.Recovery = r
			out["problem"] = &copyProblem
		}
		if r.Action != "retry_same_plan" {
			delete(out, "next")
		}
	}
	return out
}

func (m *management) getProvision(ctx context.Context, a planLookupArgs) (any, error) {
	if _, err := m.getPlan(ctx, a); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.provisionStatusLocked(a.PlanID), nil
}

func (m *management) cancelProvision(ctx context.Context, a planLookupArgs) (any, error) {
	if _, err := m.getPlan(ctx, a); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.plans[a.PlanID]; p != nil {
		if _, err := m.checkedPlan(ctx, p.ID, p.Action); err != nil {
			return nil, err
		}
	}
	if w := m.provisions[a.PlanID]; w != nil && w.status == "running" {
		w.cancel()
		if p := m.plans[a.PlanID]; p != nil && p.Provision != nil && p.Provision.Native != nil {
			p.Provision.Native.Phase = "cancellation_requested"
			p.Provision.Native.CancelRequested = true
			if err := m.persistPlans(); err != nil {
				return nil, err
			}
		}
		recovery := m.recoveryTool(Recovery{Action: "inspect"}, "gregale_get_provision", map[string]any{"plan_id": a.PlanID})
		return map[string]any{"schema_version": recoveryContractVersion, "plan_id": a.PlanID, "status": "cancellation_requested", "recovery": recovery}, nil
	}
	if p := m.plans[a.PlanID]; p != nil && p.localReceipt == nil && p.Provision != nil && p.Provision.Native != nil {
		p.Provision.Native.CancelRequested = true
		p.Provision.Native.Phase = "cancelled"
		if err := m.persistPlans(); err != nil {
			return nil, err
		}
	}
	return m.provisionStatusLocked(a.PlanID), nil
}
