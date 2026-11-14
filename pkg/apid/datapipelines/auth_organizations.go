package datapipelines

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// The account-owned live Auth app and its exact managed binding are checked at
// approval and on every snapshot. Provider credentials never leave the worker.
func (s *DataAPISetup) ResolveOrganizations(ctx context.Context, account, mirror string, a api.DataPipelineAuthOrganizations) (*api.DataPipelineAuthOrganizations, error) {
	resolved, _, err := s.boundOrganizations(ctx, account, mirror, a)
	if err != nil {
		return nil, err
	}
	if err = s.authVisible(ctx, account, a.AuthAppID); err != nil {
		return nil, err
	}
	return resolved, nil
}

// Background reconciliation follows the saved account-owned app/binding. Human
// visibility is enforced separately when a new owner mapping is reviewed.
func (s *DataAPISetup) boundOrganizations(ctx context.Context, account, mirror string, a api.DataPipelineAuthOrganizations) (*api.DataPipelineAuthOrganizations, string, error) {
	issuer, err := s.auth(ctx, account, a.AuthAppID)
	if err != nil {
		return nil, "", err
	}
	bindings, err := s.Bindings.ListForApp(ctx, account, a.AuthAppID, api.DefaultEnvScope)
	if err != nil {
		return nil, "", dataAPIError(err)
	}
	a.BindingID, a.DatabaseID = "", ""
	for _, b := range bindings {
		if b.Access != string(managedpostgres.CredentialAuth) || b.State != string(managedpostgres.BindingStateReady) || b.EnvironmentKey != "DATABASE_URL" {
			continue
		}
		binding, err := s.Bindings.Get(ctx, account, b.BindingID)
		if err != nil {
			return nil, "", dataAPIError(err)
		}
		if a.BindingID != "" || binding.DatabaseID == mirror {
			return nil, "", datapipeline.ErrConflict
		}
		if binding.AppID != a.AuthAppID || binding.Access != managedpostgres.CredentialAuth || binding.State != managedpostgres.BindingStateReady || binding.Scope != api.DefaultEnvScope || binding.EnvironmentKey != "DATABASE_URL" {
			return nil, "", datapipeline.ErrConflict
		}
		a.BindingID, a.DatabaseID = binding.ID, binding.DatabaseID
	}
	if a.BindingID == "" {
		return nil, "", datapipeline.ErrConflict
	}
	return &a, issuer, nil
}

func (s *DataAPISetup) OrganizationMembers(ctx context.Context, p datapipeline.Pipeline) (datapipeline.AuthMembershipSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, api.DataPipelineAuthQueryTimeout)
	defer cancel()
	a := p.Serving.AuthOrganizations
	current, issuer, err := s.boundOrganizations(ctx, p.AccountID, p.DatabaseID, *a)
	if err != nil {
		return datapipeline.AuthMembershipSnapshot{}, err
	}
	if current.BindingID != a.BindingID || current.DatabaseID != a.DatabaseID || s.SQL == nil {
		return datapipeline.AuthMembershipSnapshot{}, datapipeline.ErrConflict
	}
	var members []api.DataPipelineTenantMembership
	err = s.SQL(ctx, p.AccountID, a.DatabaseID, func(conn *pgx.Conn, _ string) error {
		var readErr error
		members, readErr = datapipeline.ReadAuthOrganizationMembers(ctx, conn, *a)
		return readErr
	})
	if err != nil {
		return datapipeline.AuthMembershipSnapshot{}, datapipeline.ErrUnavailable
	}
	return datapipeline.AuthMembershipSnapshot{Members: members, Issuer: issuer}, nil
}
