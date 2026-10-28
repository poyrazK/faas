package edgerules

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// ReferencedEdit is an edit to an object edge rules reference, such as an
// edge-rule list or a CORS preset. It changes the served policy of every
// referencing rule, so it goes through the generation barrier a rule edit
// uses (issue #2683): before the write every serving gateway fences those
// rules' hosts, one convergence per app, and after it they are released. An
// object no rule references changes no served policy and needs no barrier.
type ReferencedEdit struct {
	// Operation is the convergence payload's op, e.g. "list_updated".
	Operation string
	// Subject names the object in messages, e.g. "edge rule list".
	Subject string
	// Hosts maps each app with a referencing rule to those rules' match
	// hosts. It is read once before and once under the account's lock.
	Hosts func(context.Context) (map[string][]string, error)
}

// PrepareReferencedEdit fences the referencing rules' hosts. The returned
// release drops the account's edge-rule lock; call it after the write and
// ApplyReferencedEdit.
func (s *Service) PrepareReferencedEdit(ctx context.Context, edit ReferencedEdit) ([]*EdgeRuleConvergence, func(context.Context), *api.Problem) {
	noop := func(context.Context) {}
	unavailable := api.ErrCapacity("edge-rule fleet convergence is unavailable; the " + edit.Subject + " was not changed")
	hosts, err := edit.Hosts(ctx)
	if err != nil {
		return nil, noop, unavailable
	}
	if len(hosts) == 0 {
		return nil, noop, nil
	}
	unlock, err := s.LockEdgeRuleMutations(ctx, slices.Sorted(maps.Keys(hosts)))
	if err != nil {
		return nil, noop, unavailable
	}
	// Read again under the account's lock, so a rule that started
	// referencing the object in between is fenced too.
	if hosts, err = edit.Hosts(ctx); err != nil {
		unlock(ctx)
		return nil, noop, unavailable
	}
	convergences := make([]*EdgeRuleConvergence, 0, len(hosts))
	for _, appID := range slices.Sorted(maps.Keys(hosts)) {
		conv, err := s.PrepareEdgeRuleMutationLocked(ctx, appID, "", edit.Operation, noop, hosts[appID]...)
		if err != nil {
			AbortEdgeRuleConvergences(ctx, convergences)
			unlock(ctx)
			return nil, noop, unavailable
		}
		convergences = append(convergences, conv)
	}
	return convergences, unlock, nil
}

// ApplyReferencedEdit releases the fences after the write, oldest generation
// first, and labels the response with the newest generation: active, or a
// 503 converging problem when a gateway has not acknowledged.
func (s *Service) ApplyReferencedEdit(ctx context.Context, w http.ResponseWriter, edit ReferencedEdit, subjectID string, convergences []*EdgeRuleConvergence) *api.Problem {
	if len(convergences) == 0 {
		return nil
	}
	var applyErr error
	for _, conv := range convergences {
		applyErr = errors.Join(applyErr, conv.Apply(ctx, ""))
	}
	newest := convergences[len(convergences)-1]
	if applyErr != nil {
		newest.SetResponseState(w, "converging")
		s.Host.Logger().Error(edit.Subject+" saved but fleet convergence is incomplete", "id", subjectID, "generation", newest.Generation, "err", applyErr)
		return api.ErrCapacity(edit.Subject + " was saved but the serving fleet has not acknowledged it; read the " + edit.Subject + " before retrying this change")
	}
	newest.SetResponseState(w, "active")
	return nil
}

// AbortEdgeRuleConvergences releases prepared fences after a failed write.
func AbortEdgeRuleConvergences(ctx context.Context, convergences []*EdgeRuleConvergence) {
	for _, conv := range convergences {
		conv.Abort(ctx)
	}
}

// CorsPresetEdit is the barrier edit for updating or deleting a CORS preset:
// deleting one clears the rules' preset reference, which changes their
// policy as an update does.
func (s *Service) CorsPresetEdit(accountID, presetID, operation string) ReferencedEdit {
	return ReferencedEdit{Operation: operation, Subject: "CORS preset", Hosts: func(ctx context.Context) (map[string][]string, error) {
		rules, err := s.Host.Store().ListEdgeRulesForAccount(ctx, accountID)
		if err != nil {
			return nil, err
		}
		hosts := map[string][]string{}
		for _, rule := range rules {
			if rule.CorsPresetID != nil && *rule.CorsPresetID == presetID {
				hosts[rule.AppID] = append(hosts[rule.AppID], rule.MatchHost)
			}
		}
		return hosts, nil
	}}
}
