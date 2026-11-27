package apidserver

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/executionadmission"
	"github.com/onebox-faas/faas/pkg/apid/postgresaccess"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/executioncontract"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) admitSourceExecution(r *http.Request, acct state.Account, access executionAccess, request api.CreateExecutionRequest, prepare state.PrepareExecutionAdmission) (state.Execution, bool, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		params, err := prepare(r.Context(), executionStoreAdmissionReader{s.store})
		if err != nil {
			return state.Execution{}, false, err
		}
		row, err := s.store.CreateExecution(r.Context(), params)
		return row, false, err
	}
	key := strings.TrimSpace(values[0])
	if len(values) != 1 || !executionadmission.ValidKey(key) {
		return state.Execution{}, false, api.ErrValidation("Idempotency-Key must contain 1 to 255 printable ASCII characters without spaces")
	}
	fingerprint, err := executionadmission.Fingerprint(request, hostHMACKeyBytes())
	if err != nil {
		return state.Execution{}, false, api.ErrCapacity("execution admission signing key is unavailable")
	}
	store, ok := s.store.(state.ExecutionAdmissionStore)
	if !ok {
		return state.Execution{}, false, api.ErrCapacity("atomic execution admission is unavailable in this store")
	}
	namespace := postgresaccess.IdempotencyKey(r, projectaccess.IdempotencyKey(r, appaccess.IdempotencyKey(r, "")))
	if !access.broad {
		namespace = "runs-principal:" + access.principalID + "\n" + namespace
	}
	namespace = "POST /v1/executions\n" + namespace
	return store.AdmitExecution(r.Context(), state.ExecutionAdmissionIdentity{
		AccountID: acct.ID, ScopeHash: sha256.Sum256([]byte(namespace)), KeyHash: sha256.Sum256([]byte(key)),
		Fingerprint: fingerprint, LegacyKey: namespace + key,
	}, prepare)
}

// Unkeyed requests preserve the Store contract. Keyed PostgreSQL admissions
// receive a transaction-bound reader instead, so preparation needs no second
// pool connection while holding the per-account lock.
type executionStoreAdmissionReader struct{ store state.Store }

func (r executionStoreAdmissionReader) ExecutionByID(ctx context.Context, accountID, executionID string) (state.Execution, error) {
	return r.store.ExecutionByID(ctx, accountID, executionID)
}

func (r executionStoreAdmissionReader) ExecutionArtifactGrantByToken(ctx context.Context, accountID string, tokenHash []byte, at time.Time) (state.ExecutionArtifactGrant, error) {
	store, ok := r.store.(state.ExecutionArtifactGrantStore)
	if !ok {
		return state.ExecutionArtifactGrant{}, api.ErrCapacity("execution artifact grants are unavailable in this store")
	}
	return store.ExecutionArtifactGrantByToken(ctx, accountID, tokenHash, at)
}

func (s *server) prepareExecutionAdmission(ctx context.Context, reader state.ExecutionAdmissionReader, acct state.Account, access executionAccess, request api.CreateExecutionRequest) (state.CreateExecutionParams, []state.ExecutionArtifactGrant, error) {
	if err := s.requireExecutionRequestFeatures(request); err != nil {
		return state.CreateExecutionParams{}, nil, err
	}
	if err := executioncontract.CheckRequest(request); err != nil {
		return state.CreateExecutionParams{}, nil, api.NewProblem(http.StatusUnprocessableEntity, "execution_contract_invalid", "Invalid Run contract", err.Error())
	}
	if err := api.ValidateExecutionWorkflowMetadata(request.WorkflowID, request.StepLabel); err != nil {
		return state.CreateExecutionParams{}, nil, api.NewProblem(
			http.StatusUnprocessableEntity,
			api.CodeExecutionPayloadInvalid,
			"Invalid workflow metadata",
			err.Error(),
		)
	}
	artifactGrants, problem := s.resolveExecutionArtifactInputs(ctx, reader, acct, access, &request)
	if problem != nil {
		return state.CreateExecutionParams{}, nil, problem
	}
	resolved, problem := request.Resolve(acct.Plan)
	if problem != nil {
		return state.CreateExecutionParams{}, nil, problem
	}
	fileGrants, err := s.prepareStoredExecutionFiles(ctx, reader, acct, access, request, &resolved)
	if err != nil {
		return state.CreateExecutionParams{}, nil, err
	}
	artifactGrants = append(artifactGrants, fileGrants...)
	resolved.ResultSchema = append([]byte(nil), request.ResultSchema...)
	integrationIDs, err := api.NormalizeExecutionIntegrationIDs(request.IntegrationIDs)
	if err != nil {
		return state.CreateExecutionParams{}, nil, api.ErrValidation("integration_ids must contain at most 25 distinct UUIDs")
	}

	recipients, kid, err := executionPayloadRecipients()
	if err != nil {
		return state.CreateExecutionParams{}, nil, api.ErrCapacity("execution payload recipient is not loaded; refusing to seal execution payload")
	}
	sealed, err := executionpayload.SealRequestTo(recipients, resolved)
	if err != nil {
		if errors.Is(err, executionpayload.ErrInvalid) {
			return state.CreateExecutionParams{}, nil, api.NewProblem(
				http.StatusUnprocessableEntity,
				api.CodeExecutionPayloadInvalid,
				"Invalid execution payload",
				"source or input could not be sealed under the execution payload contract",
			).WithDocs("https://gregale.dev/docs/executions#request")
		}
		if s.log != nil {
			s.log.Error("execution payload sealing failed", "account_id", acct.ID, "err", logsanitize.FieldAny(err))
		}
		return state.CreateExecutionParams{}, nil, api.ErrCapacity("execution payload could not be sealed on this host")
	}

	admittedAt := time.Now().UTC()
	params := state.CreateExecutionParams{
		CustomerRef:            request.CustomerRef,
		ControlAccountWide:     access.controlWide,
		AccountID:              acct.ID,
		WorkflowID:             request.WorkflowID,
		StepLabel:              request.StepLabel,
		OutboundIntegrationIDs: integrationIDs,
		RunsPrincipalID:        access.principal,
		FilesAccountWide:       access.broad,
		Request:                resolved,
		SourceBytes:            resolved.SourceBytes(),
		InputBytes:             len(resolved.Input),
		AdmittedAt:             admittedAt,
		DeadlineAt:             admittedAt.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond),
		SealedPayload:          sealed,
		PayloadKID:             kid,
	}
	for _, grant := range artifactGrants {
		params.ArtifactGrantRedemptions = append(params.ArtifactGrantRedemptions, state.ExecutionArtifactGrantRedemption{
			GrantID: grant.ID, TokenHash: grant.TokenHash,
		})
	}
	return params, artifactGrants, nil
}

func (s *server) requireExecutionRequestFeatures(request api.CreateExecutionRequest) error {
	if request.Limits != nil && request.Limits.Class == api.ExecutionClassExtended && !s.executionExtendedEnabled {
		return api.ErrCapacity("extended Run admissions are disabled")
	}
	if request.EnvironmentID != "" && !s.executionEnvironmentsEnabled {
		return api.ErrCapacity("custom Run environments are disabled")
	}
	if request.UsesExtendedExecution() && !s.executionV2Enabled {
		return api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented, "Extended Runs unavailable", "new file and contract admissions are not enabled")
	}
	return nil
}
