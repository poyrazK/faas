package meter

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// egressOverageSubjects returns the billing subjects whose egress overage is
// on now (ADR-20261103131742 amendment 2); a store without the choices has
// none. A failed read keeps the last set read, so a database blip neither
// stops the apps of subjects that pay for overage nor drops the ceiling for
// the others.
func (l *Loop) egressOverageSubjects(ctx context.Context) map[string]bool {
	store, ok := l.store.(state.EgressOverageStore)
	if !ok {
		return nil
	}
	subjects, err := store.EgressOverageEnabledSubjects(ctx)
	if err != nil {
		l.log.Warn("meter: egress overage choices read failed", "err", err)
		return l.egressOverageOn
	}
	l.egressOverageOn = subjects
	return subjects
}

// egressOverageChoiceReader is the part of state.EgressOverageStore a
// delivery reads.
type egressOverageChoiceReader interface {
	EgressOverageChoices(ctx context.Context, subjectID string, start, end time.Time) ([]state.EgressOverageChoice, error)
}

// egressOverageOnDuring reports whether subject had egress overage on at any
// moment of [start, end). A store without the choices has it off.
func egressOverageOnDuring(ctx context.Context, store state.Store, subjectID string, start, end time.Time) (bool, error) {
	choices, ok := store.(egressOverageChoiceReader)
	if !ok {
		return false, nil
	}
	made, err := choices.EgressOverageChoices(ctx, subjectID, start, end)
	if err != nil {
		return false, fmt.Errorf("meter: read egress overage choices for %s: %w", subjectID, err)
	}
	return state.EgressOverageOnDuring(made, start, end), nil
}
