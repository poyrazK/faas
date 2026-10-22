package bindingrun

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"slices"
	"time"
)

func cloneQualification(q *api.BindingVerificationQualification) *api.BindingVerificationQualification {
	if q == nil {
		return nil
	}
	copy := *q
	copy.Conditions = slices.Clone(q.Conditions)
	return &copy
}
func pendingQualificationOf(s snapshot) *api.BindingVerificationQualification {
	if s.app != nil {
		return s.app.Qualification
	}
	return s.graph.Qualification
}
func validatePendingQualification(s snapshot) error {
	q := pendingQualificationOf(s)
	if s.status() != "pending" || q == nil || q.CheckedAt.IsZero() || q.CheckedAt.After(time.Now()) || !q.CheckedAt.Before(s.expires()) || len(q.Conditions) == 0 || len(q.Conditions) > api.BindingVerificationQualificationMaxConditions {
		return errors.New("server did not confirm pending qualification")
	}
	for _, c := range q.Conditions {
		found := false
		for _, r := range s.members() {
			if r.App != c.App {
				continue
			}
			if q.CheckedAt.Before(r.CreatedAt) {
				return errors.New("qualification observation predates receipt")
			}
			switch c.Code {
			case "runtime_updating", "runtime_stale":
				found = c.Type == "" && c.Name == "" && c.Binding == "" && c.Scope == r.Scope
			case "queue_consumer_unobserved", "queue_consumer_stale":
				if c.Type != api.BindingTypeQueue {
					return errors.New("invalid pending consumer condition")
				}
				found = qualificationBindingSelected(r, c)
			case "application_adoption_unknown", "application_ack_stale", "application_ack_candidate_unobserved":
				if c.Type != api.BindingTypePostgres && c.Type != api.BindingTypeObjectStorage {
					return errors.New("invalid pending acknowledgment condition")
				}
				found = qualificationBindingSelected(r, c)
			default:
				return errors.New("unknown pending qualification condition")
			}
		}
		if !found {
			return errors.New("qualification condition changed the original selection")
		}
	}
	return nil
}
func qualificationBindingSelected(r api.BindingVerificationRun, c api.BindingVerificationQualificationCondition) bool {
	for _, step := range r.Steps {
		p := step.Plan
		if p.Type == c.Type && p.Name == c.Name && p.Binding == c.Binding && p.Scope == c.Scope {
			return true
		}
	}
	for _, item := range r.Inventory.Bindings {
		if item.Type == c.Type && item.Name == c.Name && item.Binding == c.Binding && item.Scope == c.Scope {
			return true
		}
	}
	return false
}
func waitQualification(ctx context.Context, interval time.Duration) error {
	if e := statusReadContextError(ctx); e != nil {
		return e
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return statusReadContextError(ctx)
	}
}
