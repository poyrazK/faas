package state

import "context"

// ProjectOwnershipStore resolves the persisted organization, independently of
// the project creator's membership and identity lifecycle.
type ProjectOwnershipStore interface {
	ProjectByOrgSlug(context.Context, string, string) (Project, error)
	ListProjectsForOrg(context.Context, string) ([]Project, error)
}

// ProjectResourceAccount supplies policy while retaining the storage namespace
// used by sealed configuration, plan tokens and release history.
func ProjectResourceAccount(ctx context.Context, store AppOwnerReader, project Project) (Account, error) {
	return AppResourceAccount(ctx, store, App{AccountID: project.AccountID, OrgID: project.OrgID})
}

// ProjectResourceAccountByID is for durable background work, which must resolve
// the persisted project rather than a creator supplied by an old operation row.
func ProjectResourceAccountByID(ctx context.Context, store interface {
	AppOwnerReader
	ProjectByID(context.Context, string) (Project, error)
}, projectID string) (Account, error) {
	project, err := store.ProjectByID(ctx, projectID)
	if err != nil {
		return Account{}, err
	}
	return ProjectResourceAccount(ctx, store, project)
}
