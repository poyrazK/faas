package apidserver

import "context"

func (r *server) EnvironmentReleaseSetWarning(ctx context.Context, accountID, projectSlug, environment string) string {
	return r.environmentReleaseSetWarning(ctx, accountID, projectSlug, environment)
}
