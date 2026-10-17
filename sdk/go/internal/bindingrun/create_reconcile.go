// adr:1368 — reconcile uncertain creation through bounded reads, never another POST.
package bindingrun

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/poyrazK/faas/sdk/go/internal/api"
)

func usableCreation(s snapshot) error {
	if err := validate(s); err != nil {
		return err
	}
	pending, active, err := inspect(s, map[string]reference{})
	if err != nil {
		return err
	}
	if s.status() == "passed" {
		if len(pending) > 0 || active > 0 || pendingQualificationOf(s) != nil {
			return errors.New("passed creation has unfinished resources")
		}
		for _, m := range s.members() {
			if m.Status != "passed" || m.Qualification != nil {
				return errors.New("graph member is not passed")
			}
		}
	}
	if s.graph != nil {
		seen := map[string]bool{s.id(): true}
		for _, m := range s.members() {
			if seen[m.ID] {
				return errors.New("duplicate graph receipt identity")
			}
			seen[m.ID] = true
		}
	}
	return nil
}
func reconcileCreation(ctx context.Context, cause error, began time.Time, o CreationOptions, read func(context.Context, bool) (snapshot, error), check func(snapshot) error) (snapshot, error) {
	fail := func(err error) (snapshot, error) { return snapshot{}, errors.Join(cause, err) }
	if err := statusReadContextError(ctx); err != nil {
		return fail(err)
	}
	until := minCreationDeadline(began.Add(time.Duration(o.TTLSeconds)*time.Second), time.Now().Add(time.Duration(api.BindingVerificationCreationReconcileTimeoutMilliseconds)*time.Millisecond))
	bounded, cancel := context.WithDeadline(ctx, until)
	defer cancel()
	emit := func(p Progress) error {
		if o.OnProgress != nil {
			if err := o.OnProgress(p); err != nil {
				return err
			}
		}
		return statusReadContextError(bounded)
	}
	if err := emit(Progress{RunID: o.RunID, Step: -1, Status: "reconciling", Operation: "creation"}); err != nil {
		return fail(err)
	}
	s, err := readStatusWithRetry(bounded, o.RunID, Options{StatusReadRetries: o.StatusReadRetries}, read, emit)
	if err != nil {
		return fail(err)
	}
	if err = check(s); err != nil {
		return fail(err)
	}
	if err = usableCreation(s); err != nil {
		return fail(fmt.Errorf("creation is not usable: %w", err))
	}
	if err = statusReadContextError(bounded); err != nil {
		return fail(err)
	}
	return s, nil
}
func minCreationDeadline(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
