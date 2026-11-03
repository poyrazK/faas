package managedpostgres

import "context"

// Background sweeps (usage collection, health checks, compute and storage
// caps) share a provider account's API request limit with customer actions.
// They mark their context so a provider can pace their requests: a sweep must
// never spend the limit that creates, deletes, undeletes and credential
// issues need.
type backgroundRequestsKey struct{}

// WithBackgroundRequests marks ctx as a background sweep's.
func WithBackgroundRequests(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundRequestsKey{}, true)
}

// BackgroundRequests reports whether ctx belongs to a background sweep.
func BackgroundRequests(ctx context.Context) bool {
	background, _ := ctx.Value(backgroundRequestsKey{}).(bool)
	return background
}
