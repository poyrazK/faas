package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/state"
)

func (r executionStoreAdmissionReader) ExecutionFileByID(ctx context.Context, account, id string) (state.ExecutionFile, error) {
	s, ok := r.store.(state.ExecutionFileAdmissionReader)
	if !ok {
		return state.ExecutionFile{}, state.ErrExecutionFileNotReady
	}
	return s.ExecutionFileByID(ctx, account, id)
}
func (s *server) prepareStoredExecutionFiles(ctx context.Context, reader state.ExecutionAdmissionReader, acct state.Account, access executionAccess, request api.CreateExecutionRequest, resolved *api.ResolvedExecutionRequest) ([]state.ExecutionArtifactGrant, error) {
	svc := s.executionFilesService()
	svc.GrantTokenHash = hashExecutionArtifactGrantToken
	return svc.PrepareAdmission(ctx, reader, runfiles.Access{Account: acct, Principal: access.principal, Wide: access.broad}, request, resolved)
}
