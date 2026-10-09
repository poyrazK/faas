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
	"github.com/onebox-faas/faas/pkg/automationchecks"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrAutomationPublishCheckRequired = errors.New("automation: publishing checks required; rerun publish with --scenarios (receipt missing, expired or stale)")

type AutomationPublishCheckStore interface {
	GetAutomationPublishPolicy(context.Context, string) (api.AutomationPublishPolicy, error)
	SetAutomationPublishPolicy(context.Context, string, api.SetAutomationPublishPolicyRequest) (api.AutomationPublishPolicy, error)
	CheckAutomationPublication(context.Context, string, string, string, string, api.CheckAutomationPublicationRequest, api.Plan) (api.CheckAutomationPublicationResponse, error)
}
type automationPublishReceipt struct {
	AppID, Name, AccountID, APIKeyID, TokenHash string
	PolicyVersion                               int64
	ExpiresAt                                   time.Time
	Evidence                                    api.AutomationCheckEvidence
}

func defaultAutomationPolicy() api.AutomationPublishPolicy {
	return api.AutomationPublishPolicy{Mode: "optional"}
}
func nextAutomationPolicy(p api.AutomationPublishPolicy, r api.SetAutomationPublishPolicyRequest) (api.AutomationPublishPolicy, error) {
	if r.Mode != "optional" && r.Mode != "scenarios" && r.Mode != "coverage" {
		return p, ErrAutomationInvalid
	}
	if r.ExpectedVersion != p.Version {
		return p, ErrAutomationVersionConflict
	}
	return api.AutomationPublishPolicy{Mode: r.Mode, Version: p.Version + 1}, nil
}
func automationReceiptHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func evaluatePublicationChecks(ctx context.Context, store AutomationStore, outbound WorkflowOutboundStore, appID, name, accountID, keyID string, policy api.AutomationPublishPolicy, request api.CheckAutomationPublicationRequest, plan api.Plan) (automationPublishReceipt, api.CheckAutomationPublicationResponse, error) {
	fail := func(err error) (automationPublishReceipt, api.CheckAutomationPublicationResponse, error) {
		return automationPublishReceipt{}, api.CheckAutomationPublicationResponse{}, err
	}
	if len(request.Scenarios) < 1 || len(request.Scenarios) > api.AutomationPublishCheckMaxScenarios || request.ExpectedVersion <= 0 || accountID == "" {
		return fail(ErrAutomationInvalid)
	}
	raw, err := json.Marshal(request)
	if err != nil || int64(len(raw)) > api.AutomationPublishCheckMaxBytes {
		return fail(ErrAutomationInvalid)
	}
	records, err := store.ListAutomations(ctx, appID)
	if err != nil {
		return fail(err)
	}
	var draft Automation
	for _, a := range records {
		if a.Name == name {
			draft = a
		}
	}
	if draft.Name == "" {
		return fail(ErrNotFound)
	}
	if draft.Version != request.ExpectedVersion {
		return fail(ErrAutomationVersionConflict)
	}
	var def api.WorkflowSpec
	if json.Unmarshal(draft.Draft, &def) != nil {
		return fail(ErrAutomationInvalid)
	}
	if err := outbound.ValidateWorkflowOutboundBindings(ctx, appID, def); err != nil {
		return fail(err)
	}
	snapshot, _ := json.Marshal(def)
	coverage := automationchecks.Coverage{}
	evidence := api.AutomationCheckEvidence{ServerVerified: true, DefinitionHash: automationReceiptHash(string(snapshot)), CheckedVersion: draft.Version, CheckedAt: time.Now().UTC(), Scenarios: []api.AutomationCheckScenario{}, Exclusions: append([]api.AutomationCheckExclusion{}, request.Exclusions...), CoverageRequired: request.RequireCoverage || policy.Mode == "coverage"}
	seen := map[string]bool{}
	for _, scenario := range request.Scenarios {
		if seen[scenario.Name] {
			return fail(ErrAutomationInvalid)
		}
		seen[scenario.Name] = true
		local, _ := json.Marshal(scenario.Simulation.Definition)
		if !automationchecks.EqualJSON(local, snapshot) {
			return fail(ErrAutomationInvalid)
		}
		if err := automationchecks.ValidateExpectations(def, scenario.Expectations); err != nil {
			return fail(fmt.Errorf("%w: %s", ErrAutomationInvalid, err))
		}
		simRaw, _ := json.Marshal(scenario.Simulation)
		if int64(len(simRaw)) > api.AutomationSimulationRequestMaxBytes {
			return fail(ErrAutomationInvalid)
		}
		scenario.Simulation.Definition = def
		response, err := SimulateAutomation(ctx, scenario.Simulation, plan)
		if err != nil {
			return fail(fmt.Errorf("%w: simulation rejected", ErrAutomationInvalid))
		}
		if !automationchecks.Passes(scenario.Expectations, response) {
			return fail(fmt.Errorf("%w: scenario assertions failed or trace incomplete", ErrAutomationInvalid))
		}
		coverage.Observe(response)
		evidence.Scenarios = append(evidence.Scenarios, api.AutomationCheckScenario{Name: scenario.Name, Passed: true, DefinitionValid: true, Complete: true})
	}
	remaining, err := automationchecks.RemainingCoverage(def, coverage, evidence.Exclusions)
	if err != nil {
		return fail(fmt.Errorf("%w: invalid coverage exclusions", ErrAutomationInvalid))
	}
	evidence.CoverageRemaining = remaining
	evidence.CoveragePassed = remaining == 0
	if err := evidence.Validate(def, draft.Version, time.Now()); err != nil {
		return fail(fmt.Errorf("%w: checks or coverage did not pass", ErrAutomationInvalid))
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return fail(err)
	}
	token := hex.EncodeToString(entropy[:])
	expiry := time.Now().UTC().Add(api.AutomationPublishCheckReceiptTTL)
	receipt := automationPublishReceipt{AppID: appID, Name: name, AccountID: accountID, APIKeyID: keyID, TokenHash: automationReceiptHash(token), PolicyVersion: policy.Version, ExpiresAt: expiry, Evidence: evidence}
	return receipt, api.CheckAutomationPublicationResponse{Receipt: token, ExpiresAt: expiry, Evidence: evidence}, nil
}
func validatePublicationReceipt(p api.AutomationPublishPolicy, r automationPublishReceipt, appID, name string, m *AutomationMutation, previous *Automation) error {
	if m.Action != "publish" {
		return nil
	}
	if m.CheckReceipt == "" && p.Mode == "optional" {
		return nil
	}
	if previous == nil {
		return ErrNotFound
	}
	if r.TokenHash == "" || r.TokenHash != automationReceiptHash(m.CheckReceipt) || r.AppID != appID || r.Name != name || r.AccountID != m.ActorAccountID || r.APIKeyID != m.ActorAPIKeyID || r.PolicyVersion != p.Version || !time.Now().Before(r.ExpiresAt) || r.Evidence.CheckedVersion != previous.Version {
		return ErrAutomationPublishCheckRequired
	}
	var def api.WorkflowSpec
	if json.Unmarshal(previous.Draft, &def) != nil || r.Evidence.Validate(def, previous.Version, time.Now()) != nil || (p.Mode == "coverage" && (!r.Evidence.CoverageRequired || !r.Evidence.CoveragePassed)) {
		return ErrAutomationPublishCheckRequired
	}
	// Only stored server results are trusted; supplied client evidence cannot override them.
	evidence := r.Evidence
	evidence.ServerVerified = true
	m.CheckEvidence = &evidence
	return nil
}

func (m *MemStore) GetAutomationPublishPolicy(_ context.Context, appID string) (api.AutomationPublishPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.apps[appID]; !ok || a.Status == AppDeleted {
		return api.AutomationPublishPolicy{}, ErrNotFound
	}
	if p, ok := m.automationPublishPolicies[appID]; ok {
		return p, nil
	}
	return defaultAutomationPolicy(), nil
}
func (m *MemStore) SetAutomationPublishPolicy(_ context.Context, appID string, r api.SetAutomationPublishPolicyRequest) (api.AutomationPublishPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.apps[appID]; !ok || a.Status == AppDeleted {
		return api.AutomationPublishPolicy{}, ErrNotFound
	}
	p, ok := m.automationPublishPolicies[appID]
	if !ok {
		p = defaultAutomationPolicy()
	}
	next, err := nextAutomationPolicy(p, r)
	if err != nil {
		return p, err
	}
	if m.automationPublishPolicies == nil {
		m.automationPublishPolicies = map[string]api.AutomationPublishPolicy{}
	}
	m.automationPublishPolicies[appID] = next
	return next, nil
}
func (m *MemStore) CheckAutomationPublication(ctx context.Context, appID, name, accountID, keyID string, request api.CheckAutomationPublicationRequest, plan api.Plan) (api.CheckAutomationPublicationResponse, error) {
	p, err := m.GetAutomationPublishPolicy(ctx, appID)
	if err != nil {
		return api.CheckAutomationPublicationResponse{}, err
	}
	receipt, response, err := evaluatePublicationChecks(ctx, m, m, appID, name, accountID, keyID, p, request, plan)
	if err != nil {
		return response, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.automations[appID+"/"+name]
	currentPolicy, exists := m.automationPublishPolicies[appID]
	if !exists {
		currentPolicy = defaultAutomationPolicy()
	}
	if !ok || current.Version != request.ExpectedVersion || currentPolicy.Version != p.Version {
		return api.CheckAutomationPublicationResponse{}, ErrAutomationVersionConflict
	}
	if m.automationPublishReceipts == nil {
		m.automationPublishReceipts = map[string]automationPublishReceipt{}
	}
	for k, r := range m.automationPublishReceipts {
		if !time.Now().Before(r.ExpiresAt) || (r.AppID == appID && r.Name == name && r.AccountID == accountID && r.APIKeyID == keyID) {
			delete(m.automationPublishReceipts, k)
		}
	}
	raw := automationCheckEvidenceJSON(&receipt.Evidence)
	receipt.Evidence = api.AutomationCheckEvidence{}
	if err := json.Unmarshal(raw, &receipt.Evidence); err != nil {
		return api.CheckAutomationPublicationResponse{}, err
	}
	m.automationPublishReceipts[receipt.TokenHash] = receipt
	return response, nil
}
func (s *PgStore) GetAutomationPublishPolicy(ctx context.Context, appID string) (api.AutomationPublishPolicy, error) {
	return readAutomationPolicy(ctx, s.pool, appID)
}

func readAutomationPolicy(ctx context.Context, db sqlc.DBTX, appID string) (api.AutomationPublishPolicy, error) {
	p := defaultAutomationPolicy()
	row, err := sqlc.New().GetAutomationPublishPolicy(ctx, db, mustPgUUID(appID))
	p.Mode, p.Version = row.Mode, row.Version
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return p, err
}
func (s *PgStore) SetAutomationPublishPolicy(ctx context.Context, appID string, r api.SetAutomationPublishPolicyRequest) (api.AutomationPublishPolicy, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.AutomationPublishPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = sqlc.New().LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.AutomationPublishPolicy{}, err
	}
	p, err := readAutomationPolicy(ctx, tx, appID)
	if err != nil {
		return p, err
	}
	next, err := nextAutomationPolicy(p, r)
	if err != nil {
		return p, err
	}
	err = sqlc.New().UpsertAutomationPublishPolicy(ctx, tx, sqlc.UpsertAutomationPublishPolicyParams{AppID: mustPgUUID(appID), Mode: next.Mode, Version: next.Version})
	if err != nil {
		return p, err
	}
	return next, tx.Commit(ctx)
}
func (s *PgStore) CheckAutomationPublication(ctx context.Context, appID, name, accountID, keyID string, request api.CheckAutomationPublicationRequest, plan api.Plan) (api.CheckAutomationPublicationResponse, error) {
	p, err := s.GetAutomationPublishPolicy(ctx, appID)
	if err != nil {
		return api.CheckAutomationPublicationResponse{}, err
	}
	receipt, response, err := evaluatePublicationChecks(ctx, s, s, appID, name, accountID, keyID, p, request, plan)
	if err != nil {
		return response, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.CheckAutomationPublicationResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = sqlc.New().LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.CheckAutomationPublicationResponse{}, err
	}
	policy, err := readAutomationPolicy(ctx, tx, appID)
	if err != nil {
		return api.CheckAutomationPublicationResponse{}, err
	}
	version, err := sqlc.New().GetAutomationPublishDraftVersion(ctx, tx, sqlc.GetAutomationPublishDraftVersionParams{AppID: mustPgUUID(appID), Name: name})
	if err != nil {
		return api.CheckAutomationPublicationResponse{}, err
	}
	if version != request.ExpectedVersion || policy.Version != p.Version {
		return api.CheckAutomationPublicationResponse{}, ErrAutomationVersionConflict
	}
	raw, _ := json.Marshal(receipt.Evidence)
	err = sqlc.New().UpsertAutomationPublishReceipt(ctx, tx, sqlc.UpsertAutomationPublishReceiptParams{AppID: mustPgUUID(appID), Name: name, AccountID: mustPgUUID(accountID), ApiKeyID: keyID, TokenHash: receipt.TokenHash, PolicyVersion: p.Version, ExpiresAt: pgtype.Timestamptz{Time: receipt.ExpiresAt, Valid: true}, Evidence: raw})
	if err != nil {
		return api.CheckAutomationPublicationResponse{}, err
	}
	return response, tx.Commit(ctx)
}
func enforcePublicationReceiptTx(ctx context.Context, tx pgx.Tx, appID, name string, m *AutomationMutation, previous *Automation) error {
	if m.Action != "publish" {
		return nil
	}
	p, err := readAutomationPolicy(ctx, tx, appID)
	if err != nil {
		return err
	}
	r := automationPublishReceipt{}
	if m.CheckReceipt != "" {
		row, queryErr := sqlc.New().GetAutomationPublishReceipt(ctx, tx, sqlc.GetAutomationPublishReceiptParams{AppID: mustPgUUID(appID), Name: name, AccountID: mustPgUUID(m.ActorAccountID), ApiKeyID: m.ActorAPIKeyID})
		err = queryErr
		raw := row.Evidence
		r.TokenHash, r.PolicyVersion, r.ExpiresAt = row.TokenHash, row.PolicyVersion, row.ExpiresAt.Time
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		r.AppID, r.Name, r.AccountID, r.APIKeyID = appID, name, m.ActorAccountID, m.ActorAPIKeyID
		if len(raw) > 0 && json.Unmarshal(raw, &r.Evidence) != nil {
			return ErrAutomationPublishCheckRequired
		}
	}
	return validatePublicationReceipt(p, r, appID, name, m, previous)
}
