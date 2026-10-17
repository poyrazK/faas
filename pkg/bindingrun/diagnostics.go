package bindingrun

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"maps"
	"time"
)

// These optional interfaces preserve custom clients implementing Client alone.
type appDiagnosticsClient interface {
	GetBindingVerificationRunDiagnostics(context.Context, string, string) (api.BindingVerificationRunDiagnostics, error)
}
type projectDiagnosticsClient interface {
	GetProjectBindingVerificationRunDiagnostics(context.Context, string, string, string) (api.ProjectBindingVerificationRunDiagnostics, error)
}

// Only immutable selectors are used from this detached original observation.
func diagnosticAnchor(s snapshot) snapshot {
	if s.app != nil {
		r := *s.app
		return snapshot{app: &r}
	}
	r := *s.graph
	r.Members = maps.Clone(r.Members)
	return snapshot{graph: &r}
}

type diagnosticAttempt struct {
	until    time.Time
	recovery *RecoveryError
}

func prepareDiagnostics(ctx context.Context, id string, o Options, err error) *diagnosticAttempt {
	var recovery *RecoveryError
	if o.SkipDiagnostics || !validID(id) || ctx.Err() != nil || !errors.As(err, &recovery) || recovery.skipDiagnostics || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	until := time.Now().Add(time.Duration(api.BindingVerificationRunnerDiagnosticsTimeoutMilliseconds) * time.Millisecond)
	if recovery.original.app != nil || recovery.original.graph != nil {
		if expiry := recovery.original.expires(); expiry.Before(until) {
			until = expiry
		}
	}
	if !until.After(time.Now()) {
		return nil
	}
	return &diagnosticAttempt{until: until, recovery: recovery}
}
func validAppDiagnostic(d api.BindingVerificationRunDiagnostics, app, id string, original *api.BindingVerificationRun) bool {
	if d.RunID != id || d.App != app || d.CheckedAt.IsZero() || d.ExpiresAt.IsZero() || !validID(canonicalDeploymentID(d.DeploymentID)) || d.Scope == "" {
		return false
	}
	if original != nil && (canonicalDeploymentID(d.DeploymentID) != canonicalDeploymentID(original.DeploymentID) || d.Scope != original.Scope || d.GraphRunID != original.GraphRunID || !d.ExpiresAt.Equal(original.ExpiresAt)) {
		return false
	}
	return true
}
func attachAppDiagnostics(ctx context.Context, c Client, app, id string, o Options, err error) {
	reader, ok := c.(appDiagnosticsClient)
	if !ok {
		return
	}
	attempt := prepareDiagnostics(ctx, id, o, err)
	if attempt == nil {
		return
	}
	bounded, cancel := context.WithDeadline(ctx, attempt.until)
	defer cancel()
	recovery := attempt.recovery
	d, readErr := reader.GetBindingVerificationRunDiagnostics(bounded, app, id)
	if readErr == nil && bounded.Err() == nil && validAppDiagnostic(d, app, id, recovery.original.app) {
		recovery.Diagnostics = &d
	}
}
func attachProjectDiagnostics(ctx context.Context, c Client, project, environment, id string, o Options, err error) {
	reader, ok := c.(projectDiagnosticsClient)
	if !ok {
		return
	}
	attempt := prepareDiagnostics(ctx, id, o, err)
	if attempt == nil {
		return
	}
	bounded, cancel := context.WithDeadline(ctx, attempt.until)
	defer cancel()
	recovery := attempt.recovery
	d, readErr := reader.GetProjectBindingVerificationRunDiagnostics(bounded, project, environment, id)
	if readErr != nil || bounded.Err() != nil || d.RunID != id || d.Project != project || d.Environment != environment || d.CheckedAt.IsZero() || d.ExpiresAt.IsZero() {
		return
	}
	for name, member := range d.Members {
		if !validID(member.RunID) || member.GraphRunID != id || member.Scope != environment || !member.ExpiresAt.Equal(d.ExpiresAt) || !validAppDiagnostic(member, name, member.RunID, nil) {
			return
		}
	}
	if original := recovery.original.graph; original != nil {
		if !d.ExpiresAt.Equal(original.ExpiresAt) || len(d.Members) != len(original.Members) {
			return
		}
		for name, member := range original.Members {
			observed, ok := d.Members[name]
			if !ok || !validAppDiagnostic(observed, name, member.ID, &member) {
				return
			}
		}
	}
	recovery.ProjectDiagnostics = &d
}
