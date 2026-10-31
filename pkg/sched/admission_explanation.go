package sched

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Keep existing Problem codes/HTTP behavior discoverable with errors.As. Only
// the worker reconciler persists these bounded, customer-safe explanations.
type admissionRefusal struct {
	problem  *api.Problem
	blockers []api.WorkerAdmissionBlocker
}

func (e *admissionRefusal) Error() string { return e.problem.Error() }
func (e *admissionRefusal) Unwrap() error { return e.problem }

func explainAdmission(problem *api.Problem, blockers ...api.WorkerAdmissionBlocker) error {
	return &admissionRefusal{problem: problem, blockers: blockers}
}

func resourceBlocker(code, resource string, requested int64) api.WorkerAdmissionBlocker {
	return api.WorkerAdmissionBlocker{Code: code, Resource: resource, Requested: &requested}
}

func limitBlocker(code, resource string, used, limit int64) api.WorkerAdmissionBlocker {
	return api.WorkerAdmissionBlocker{Code: code, Resource: resource, Used: &used, Limit: &limit}
}

// customerCapacityProblem restates a deployment refused by node budgets in
// terms of the app's own size, the one thing its owner can change. The
// scheduler's detail names nodes and their ledgers; deploying several
// standalone Scale apps read only "capacity_unavailable" until --ram was
// lowered. Other refusals keep their problem (nil).
func customerCapacityProblem(err error) *api.Problem {
	var refusal *admissionRefusal
	if !errors.As(err, &refusal) {
		return nil
	}
	for _, b := range refusal.blockers {
		if b.Requested == nil {
			continue
		}
		switch b.Code {
		case "node_ram":
			return api.ErrCapacity(fmt.Sprintf("no compute node has %d MB of memory free for this app right now (its ram_mb plus the %d MB VM overhead)", *b.Requested, api.PerVMOverheadMB)).
				WithHint("Lower the app's memory with `gregale app <slug> --ram MB` or `--size`, or deploy again once capacity frees up.")
		case "node_vcpu":
			return api.ErrCapacity(fmt.Sprintf("no compute node has %d guest vCPUs free for this app right now", *b.Requested)).
				WithHint("Deploy again once capacity frees up.")
		case "node_cpu":
			return api.ErrCapacity(fmt.Sprintf("no compute node has %d millicores of CPU free for this app right now", *b.Requested)).
				WithHint("Lower the app's CPU with `gregale app <slug> --cpu-millicores` or `--size`, or deploy again once capacity frees up.")
		}
	}
	return nil
}

func admissionBlockers(err error) []api.WorkerAdmissionBlocker {
	var refusal *admissionRefusal
	if errors.As(err, &refusal) {
		return refusal.blockers
	}
	return []api.WorkerAdmissionBlocker{{Code: "admission_refused"}}
}

func (p *workerScopePlan) observeAdmission(now time.Time, blockers ...api.WorkerAdmissionBlocker) {
	for _, b := range blockers {
		b = b.Clone()
		if b.ObservedAt.IsZero() {
			b.ObservedAt = now.UTC()
		}
		replaced := false
		for i := range p.blockers {
			if p.blockers[i].Code == b.Code {
				p.blockers[i], replaced = b, true
				break
			}
		}
		if !replaced && len(p.blockers) < api.WorkerAdmissionBlockersMax {
			p.blockers = append(p.blockers, b)
		}
	}
}

func (p *workerScopePlan) retainAdmissionEvidence(a *workerRecoveryAttempt) {
	for _, old := range a.lease.Pools {
		if old.Scope != p.scope || old.Revision != a.lease.Revision {
			continue
		}
		for _, b := range old.Blockers {
			// The account check precedes the cooldown gate. Its current
			// refusal (or success) replaces that historical evidence.
			if b.Code == "retry_cooldown" || p.admissionObserved && b.Code == "account_worker_limit" {
				continue
			}
			if slices.ContainsFunc(p.blockers, func(current api.WorkerAdmissionBlocker) bool { return current.Code == b.Code }) {
				continue
			}
			p.observeAdmission(b.ObservedAt, b)
		}
	}
}

func (p *workerScopePlan) observeAdmissionHold(a *workerRecoveryAttempt) {
	if a == nil {
		return
	}
	if a.cooling {
		p.retainAdmissionEvidence(a)
		retryAt := a.lease.NextAttemptAt
		p.observeAdmission(a.lease.UpdatedAt, api.WorkerAdmissionBlocker{Code: "retry_cooldown", RetryAt: &retryAt})
	}
}
