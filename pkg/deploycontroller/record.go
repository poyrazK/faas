package deploycontroller

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/releasebundle"
)

// Activation kinds and outcomes an ActivationRecorder records.
const (
	ActivationDeploy   = "deploy"
	ActivationRollback = "rollback"

	ActivationSucceeded = "succeeded"
	ActivationFailed    = "failed"
	// ActivationRolledBack is a deployment that failed after it changed the
	// host and was unwound to the release active before it.
	ActivationRolledBack = "rolled_back"
)

// Activation is a deploy or rollback as an ActivationRecorder sees it.
type Activation struct {
	Kind        string
	ReleaseID   string
	ReleaseRoot string
	CommitSHA   string // empty when the release's manifest is unreadable
	Previous    string // release id active before; empty on a first activation
	StartedAt   time.Time
}

// ActivationRecorder is an optional Runtime capability: it keeps the
// platform's release history (issue #574). Recording never changes the
// outcome of a deploy or rollback, so neither method returns an error; the
// recorder reports its own failures.
//
// Only activations are recorded. A rerun that finds its release already
// active converges without recording, and so does a refused one.
type ActivationRecorder interface {
	// StartActivation records a as in progress and returns an id for
	// FinishActivation, or "" when it could not.
	StartActivation(ctx context.Context, a Activation) string
	// FinishActivation records how a ended: ActivationSucceeded,
	// ActivationFailed or ActivationRolledBack, and the error, if any.
	FinishActivation(ctx context.Context, id string, a Activation, status, detail string)
}

// activationRecord is an activation the controller is recording. A nil
// record (a runtime without the capability) records nothing.
type activationRecord struct {
	recorder   ActivationRecorder
	id         string
	activation Activation
	finished   bool
}

func (c *Controller) startActivation(ctx context.Context, a Activation) *activationRecord {
	recorder, ok := c.runtime.(ActivationRecorder)
	if !ok {
		return nil
	}
	a.StartedAt = time.Now().UTC()
	return &activationRecord{recorder: recorder, id: recorder.StartActivation(ctx, a), activation: a}
}

// finish records the outcome err stands for, once: nil succeeded, a
// deployment unwound to its previous release rolled back, anything else
// failed.
func (r *activationRecord) finish(ctx context.Context, err error) {
	if r == nil || r.finished {
		return
	}
	r.finished = true
	status, detail := ActivationSucceeded, ""
	if err != nil {
		status, detail = ActivationFailed, err.Error()
		var unwound rolledBackError
		if errors.As(err, &unwound) {
			status = ActivationRolledBack
		}
	}
	r.recorder.FinishActivation(ctx, r.id, r.activation, status, detail)
}

// rolledBackError is a deployment that failed and was unwound to the
// release active before it. Its message is the wrapped error's.
type rolledBackError struct{ err error }

func (e rolledBackError) Error() string { return e.err.Error() }
func (e rolledBackError) Unwrap() error { return e.err }

// commitOf returns the commit a retained release was built from, or "" when
// its manifest is unreadable (activating it then fails, and is recorded).
func commitOf(releaseRoot string) string {
	manifest, err := releasebundle.Read(releaseRoot)
	if err != nil {
		return ""
	}
	return manifest.CommitSHA
}
