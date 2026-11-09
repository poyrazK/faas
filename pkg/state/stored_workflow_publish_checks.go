package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/storedworkflowchecks"
)

var ErrStoredWorkflowPublishCheckRequired = errors.New("stored workflow: publishing checks required; rerun publish with --scenarios (receipt missing, expired or stale)")

type StoredWorkflowPublishCheckStore interface {
	GetStoredWorkflowPublishPolicy(context.Context, string) (api.StoredWorkflowPublishPolicy, error)
	SetStoredWorkflowPublishPolicy(context.Context, string, api.SetStoredWorkflowPublishPolicyRequest) (api.StoredWorkflowPublishPolicy, error)
	CheckStoredWorkflowPublication(context.Context, string, string, string, string, api.CheckStoredWorkflowPublicationRequest, api.Plan) (api.CheckStoredWorkflowPublicationResponse, error)
}
type storedWorkflowPublishReceipt struct {
	AppID, Name, AccountID, APIKeyID, TokenHash string
	PolicyVersion                               int64
	ExpiresAt                                   time.Time
	Evidence                                    api.StoredWorkflowCheckEvidence
}

func defaultPublishPolicy() api.StoredWorkflowPublishPolicy {
	return api.StoredWorkflowPublishPolicy{Mode: "optional"}
}
func nextPublishPolicy(p api.StoredWorkflowPublishPolicy, r api.SetStoredWorkflowPublishPolicyRequest) (api.StoredWorkflowPublishPolicy, error) {
	if r.Mode != "optional" && r.Mode != "scenarios" && r.Mode != "coverage" {
		return p, ErrStoredWorkflowInvalid
	}
	if r.ExpectedVersion != p.Version {
		return p, ErrStoredWorkflowVersionConflict
	}
	return api.StoredWorkflowPublishPolicy{Mode: r.Mode, Version: p.Version + 1}, nil
}
func storedWorkflowReceiptHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func evaluatePublicationChecks(ctx context.Context, store StoredWorkflowStore, outbound WorkflowOutboundStore, appID, name, accountID, keyID string, policy api.StoredWorkflowPublishPolicy, request api.CheckStoredWorkflowPublicationRequest, plan api.Plan) (storedWorkflowPublishReceipt, api.CheckStoredWorkflowPublicationResponse, error) {
	fail := func(err error) (storedWorkflowPublishReceipt, api.CheckStoredWorkflowPublicationResponse, error) {
		return storedWorkflowPublishReceipt{}, api.CheckStoredWorkflowPublicationResponse{}, err
	}
	if len(request.Scenarios) < 1 || len(request.Scenarios) > api.StoredWorkflowPublishCheckMaxScenarios || request.ExpectedVersion <= 0 || accountID == "" {
		return fail(ErrStoredWorkflowInvalid)
	}
	raw, err := json.Marshal(request)
	if err != nil || int64(len(raw)) > api.StoredWorkflowPublishCheckMaxBytes {
		return fail(ErrStoredWorkflowInvalid)
	}
	records, err := store.ListStoredWorkflows(ctx, appID)
	if err != nil {
		return fail(err)
	}
	var draft StoredWorkflow
	for _, a := range records {
		if a.Name == name {
			draft = a
		}
	}
	if draft.Name == "" {
		return fail(ErrNotFound)
	}
	if draft.Version != request.ExpectedVersion {
		return fail(ErrStoredWorkflowVersionConflict)
	}
	var def api.WorkflowSpec
	if json.Unmarshal(draft.Draft, &def) != nil {
		return fail(ErrStoredWorkflowInvalid)
	}
	if err := outbound.ValidateWorkflowOutboundBindings(ctx, appID, def); err != nil {
		return fail(err)
	}
	snapshot, _ := json.Marshal(def)
	coverage := storedworkflowchecks.Coverage{}
	evidence := api.StoredWorkflowCheckEvidence{ServerVerified: true, DefinitionHash: storedWorkflowReceiptHash(string(snapshot)), CheckedVersion: draft.Version, CheckedAt: time.Now().UTC(), Scenarios: []api.StoredWorkflowCheckScenario{}, Exclusions: append([]api.StoredWorkflowCheckExclusion{}, request.Exclusions...), CoverageRequired: request.RequireCoverage || policy.Mode == "coverage"}
	seen := map[string]bool{}
	for _, scenario := range request.Scenarios {
		if seen[scenario.Name] {
			return fail(ErrStoredWorkflowInvalid)
		}
		seen[scenario.Name] = true
		local, _ := json.Marshal(scenario.Simulation.Definition)
		if !storedworkflowchecks.EqualJSON(local, snapshot) {
			return fail(ErrStoredWorkflowInvalid)
		}
		if err := storedworkflowchecks.ValidateExpectations(def, scenario.Expectations); err != nil {
			return fail(fmt.Errorf("%w: %w", ErrStoredWorkflowInvalid, err))
		}
		simRaw, _ := json.Marshal(scenario.Simulation)
		if int64(len(simRaw)) > api.StoredWorkflowSimulationRequestMaxBytes {
			return fail(ErrStoredWorkflowInvalid)
		}
		scenario.Simulation.Definition = def
		response, err := SimulateStoredWorkflow(ctx, scenario.Simulation, plan)
		if err != nil {
			return fail(fmt.Errorf("%w: simulation rejected", ErrStoredWorkflowInvalid))
		}
		if !storedworkflowchecks.Passes(scenario.Expectations, response) {
			return fail(fmt.Errorf("%w: scenario assertions failed or trace incomplete", ErrStoredWorkflowInvalid))
		}
		coverage.Observe(response)
		evidence.Scenarios = append(evidence.Scenarios, api.StoredWorkflowCheckScenario{Name: scenario.Name, Passed: true, DefinitionValid: true, Complete: true})
	}
	remaining, err := storedworkflowchecks.RemainingCoverage(def, coverage, evidence.Exclusions)
	if err != nil {
		return fail(fmt.Errorf("%w: invalid coverage exclusions", ErrStoredWorkflowInvalid))
	}
	evidence.CoverageRemaining = remaining
	evidence.CoveragePassed = remaining == 0
	if err := evidence.Validate(def, draft.Version, time.Now()); err != nil {
		return fail(fmt.Errorf("%w: checks or coverage did not pass", ErrStoredWorkflowInvalid))
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return fail(err)
	}
	token := hex.EncodeToString(entropy[:])
	expiry := time.Now().UTC().Add(api.StoredWorkflowPublishCheckReceiptTTL)
	receipt := storedWorkflowPublishReceipt{AppID: appID, Name: name, AccountID: accountID, APIKeyID: keyID, TokenHash: storedCredentialHex(credTagPublishReceipt, storedWorkflowReceiptHash(token)), PolicyVersion: policy.Version, ExpiresAt: expiry, Evidence: evidence}
	return receipt, api.CheckStoredWorkflowPublicationResponse{Receipt: token, ExpiresAt: expiry, Evidence: evidence}, nil
}
func validatePublicationReceipt(p api.StoredWorkflowPublishPolicy, r storedWorkflowPublishReceipt, appID, name string, m *StoredWorkflowMutation, previous *StoredWorkflow) error {
	if m.Action != "publish" {
		return nil
	}
	if m.CheckReceipt == "" && p.Mode == "optional" {
		return nil
	}
	if previous == nil {
		return ErrNotFound
	}
	if r.TokenHash == "" || !credentialHexMatches(credTagPublishReceipt, storedWorkflowReceiptHash(m.CheckReceipt), r.TokenHash) || r.AppID != appID || r.Name != name || r.AccountID != m.ActorAccountID || r.APIKeyID != m.ActorAPIKeyID || r.PolicyVersion != p.Version || !time.Now().Before(r.ExpiresAt) || r.Evidence.CheckedVersion != previous.Version {
		return ErrStoredWorkflowPublishCheckRequired
	}
	var def api.WorkflowSpec
	if json.Unmarshal(previous.Draft, &def) != nil || r.Evidence.Validate(def, previous.Version, time.Now()) != nil || (p.Mode == "coverage" && (!r.Evidence.CoverageRequired || !r.Evidence.CoveragePassed)) {
		return ErrStoredWorkflowPublishCheckRequired
	}
	// Only stored server results are trusted; supplied client evidence cannot override them.
	evidence := r.Evidence
	evidence.ServerVerified = true
	m.CheckEvidence = &evidence
	return nil
}

func (m *MemStore) GetStoredWorkflowPublishPolicy(_ context.Context, appID string) (api.StoredWorkflowPublishPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.apps[appID]; !ok || a.Status == AppDeleted {
		return api.StoredWorkflowPublishPolicy{}, ErrNotFound
	}
	if p, ok := m.storedWorkflowPublishPolicies[appID]; ok {
		return p, nil
	}
	return defaultPublishPolicy(), nil
}
func (m *MemStore) SetStoredWorkflowPublishPolicy(_ context.Context, appID string, r api.SetStoredWorkflowPublishPolicyRequest) (api.StoredWorkflowPublishPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.apps[appID]; !ok || a.Status == AppDeleted {
		return api.StoredWorkflowPublishPolicy{}, ErrNotFound
	}
	p, ok := m.storedWorkflowPublishPolicies[appID]
	if !ok {
		p = defaultPublishPolicy()
	}
	next, err := nextPublishPolicy(p, r)
	if err != nil {
		return p, err
	}
	if m.storedWorkflowPublishPolicies == nil {
		m.storedWorkflowPublishPolicies = map[string]api.StoredWorkflowPublishPolicy{}
	}
	m.storedWorkflowPublishPolicies[appID] = next
	return next, nil
}
func (m *MemStore) CheckStoredWorkflowPublication(ctx context.Context, appID, name, accountID, keyID string, request api.CheckStoredWorkflowPublicationRequest, plan api.Plan) (api.CheckStoredWorkflowPublicationResponse, error) {
	p, err := m.GetStoredWorkflowPublishPolicy(ctx, appID)
	if err != nil {
		return api.CheckStoredWorkflowPublicationResponse{}, err
	}
	receipt, response, err := evaluatePublicationChecks(ctx, m, m, appID, name, accountID, keyID, p, request, plan)
	if err != nil {
		return response, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.storedWorkflows[appID+"/"+name]
	currentPolicy, exists := m.storedWorkflowPublishPolicies[appID]
	if !exists {
		currentPolicy = defaultPublishPolicy()
	}
	if !ok || current.Version != request.ExpectedVersion || currentPolicy.Version != p.Version {
		return api.CheckStoredWorkflowPublicationResponse{}, ErrStoredWorkflowVersionConflict
	}
	if m.storedWorkflowPublishReceipts == nil {
		m.storedWorkflowPublishReceipts = map[string]storedWorkflowPublishReceipt{}
	}
	for k, r := range m.storedWorkflowPublishReceipts {
		if !time.Now().Before(r.ExpiresAt) || (r.AppID == appID && r.Name == name && r.AccountID == accountID && r.APIKeyID == keyID) {
			delete(m.storedWorkflowPublishReceipts, k)
		}
	}
	raw := storedWorkflowCheckEvidenceJSON(&receipt.Evidence)
	receipt.Evidence = api.StoredWorkflowCheckEvidence{}
	if err := json.Unmarshal(raw, &receipt.Evidence); err != nil {
		return api.CheckStoredWorkflowPublicationResponse{}, err
	}
	m.storedWorkflowPublishReceipts[receipt.TokenHash] = receipt
	return response, nil
}
func (s *PgStore) GetStoredWorkflowPublishPolicy(ctx context.Context, appID string) (api.StoredWorkflowPublishPolicy, error) {
	return readPublishPolicy(ctx, s.pool, appID)
}

func readPublishPolicy(ctx context.Context, db sqlc.DBTX, appID string) (api.StoredWorkflowPublishPolicy, error) {
	p := defaultPublishPolicy()
	row, err := sqlc.New().GetStoredWorkflowPublishPolicy(ctx, db, mustPgUUID(appID))
	p.Mode, p.Version = row.Mode, row.Version
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return p, err
}
func (s *PgStore) SetStoredWorkflowPublishPolicy(ctx context.Context, appID string, r api.SetStoredWorkflowPublishPolicyRequest) (api.StoredWorkflowPublishPolicy, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.StoredWorkflowPublishPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = sqlc.New().LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.StoredWorkflowPublishPolicy{}, err
	}
	p, err := readPublishPolicy(ctx, tx, appID)
	if err != nil {
		return p, err
	}
	next, err := nextPublishPolicy(p, r)
	if err != nil {
		return p, err
	}
	err = sqlc.New().UpsertStoredWorkflowPublishPolicy(ctx, tx, sqlc.UpsertStoredWorkflowPublishPolicyParams{AppID: mustPgUUID(appID), Mode: next.Mode, Version: next.Version})
	if err != nil {
		return p, err
	}
	return next, tx.Commit(ctx)
}
func (s *PgStore) CheckStoredWorkflowPublication(ctx context.Context, appID, name, accountID, keyID string, request api.CheckStoredWorkflowPublicationRequest, plan api.Plan) (api.CheckStoredWorkflowPublicationResponse, error) {
	p, err := s.GetStoredWorkflowPublishPolicy(ctx, appID)
	if err != nil {
		return api.CheckStoredWorkflowPublicationResponse{}, err
	}
	receipt, response, err := evaluatePublicationChecks(ctx, s, s, appID, name, accountID, keyID, p, request, plan)
	if err != nil {
		return response, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.CheckStoredWorkflowPublicationResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = sqlc.New().LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.CheckStoredWorkflowPublicationResponse{}, err
	}
	policy, err := readPublishPolicy(ctx, tx, appID)
	if err != nil {
		return api.CheckStoredWorkflowPublicationResponse{}, err
	}
	version, err := sqlc.New().GetStoredWorkflowPublishDraftVersion(ctx, tx, sqlc.GetStoredWorkflowPublishDraftVersionParams{AppID: mustPgUUID(appID), Name: name})
	if err != nil {
		return api.CheckStoredWorkflowPublicationResponse{}, err
	}
	if version != request.ExpectedVersion || policy.Version != p.Version {
		return api.CheckStoredWorkflowPublicationResponse{}, ErrStoredWorkflowVersionConflict
	}
	raw, _ := json.Marshal(receipt.Evidence)
	err = sqlc.New().UpsertStoredWorkflowPublishReceipt(ctx, tx, sqlc.UpsertStoredWorkflowPublishReceiptParams{AppID: mustPgUUID(appID), Name: name, AccountID: mustPgUUID(accountID), ApiKeyID: keyID, TokenHash: receipt.TokenHash, PolicyVersion: p.Version, ExpiresAt: pgtype.Timestamptz{Time: receipt.ExpiresAt, Valid: true}, Evidence: raw})
	if err != nil {
		return api.CheckStoredWorkflowPublicationResponse{}, err
	}
	return response, tx.Commit(ctx)
}
func enforcePublicationReceiptTx(ctx context.Context, tx pgx.Tx, appID, name string, m *StoredWorkflowMutation, previous *StoredWorkflow) error {
	if m.Action != "publish" {
		return nil
	}
	p, err := readPublishPolicy(ctx, tx, appID)
	if err != nil {
		return err
	}
	r := storedWorkflowPublishReceipt{}
	if m.CheckReceipt != "" {
		row, queryErr := sqlc.New().GetStoredWorkflowPublishReceipt(ctx, tx, sqlc.GetStoredWorkflowPublishReceiptParams{AppID: mustPgUUID(appID), Name: name, AccountID: mustPgUUID(m.ActorAccountID), ApiKeyID: m.ActorAPIKeyID})
		err = queryErr
		raw := row.Evidence
		r.TokenHash, r.PolicyVersion, r.ExpiresAt = row.TokenHash, row.PolicyVersion, row.ExpiresAt.Time
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		r.AppID, r.Name, r.AccountID, r.APIKeyID = appID, name, m.ActorAccountID, m.ActorAPIKeyID
		if len(raw) > 0 && json.Unmarshal(raw, &r.Evidence) != nil {
			return ErrStoredWorkflowPublishCheckRequired
		}
	}
	return validatePublicationReceipt(p, r, appID, name, m, previous)
}
