package sched

import "context"

type preferredInstanceKey struct{}

// WithPreferredInstance attaches a wake hint (ADR-1151): the instance a
// session-affinity cookie names. A wake that thaws a frozen instance thaws
// this one first when it is one of the deployment's frozen candidates, so
// the session finds the in-memory state it left. The hint never changes
// admission or which deployment serves.
func WithPreferredInstance(ctx context.Context, instanceID string) context.Context {
	if instanceID == "" {
		return ctx
	}
	return context.WithValue(ctx, preferredInstanceKey{}, instanceID)
}

// PreferredInstanceFromContext returns the wake hint WithPreferredInstance
// attached, or "".
func PreferredInstanceFromContext(ctx context.Context) string {
	id, _ := ctx.Value(preferredInstanceKey{}).(string)
	return id
}
