package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) storedWorkflowRecordsLocked(appID string) []StoredWorkflow {
	records := make([]StoredWorkflow, 0)
	for _, record := range m.storedWorkflows {
		if record.AppID == appID {
			records = append(records, copyStoredWorkflow(record))
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	return records
}
func (m *MemStore) storedWorkflowDeploymentLocked(appID string) Deployment {
	var result Deployment
	for _, candidate := range m.deployments {
		if candidate.AppID == appID && candidate.Status == DeployLive && (candidate.Scope == "" || candidate.Scope == "default") && (result.ID == "" || deploymentPreferredForWake(candidate, result)) {
			result = candidate
		}
	}
	return result
}
func (m *MemStore) ListStoredWorkflows(_ context.Context, appID string) ([]StoredWorkflow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.storedWorkflowRecordsLocked(appID), nil
}
func (m *MemStore) ListStoredWorkflowRevisions(_ context.Context, appID, name string, opts StoredWorkflowRevisionListOptions) ([]StoredWorkflowRevision, int, error) {
	if !validStoredWorkflowRevisionListOptions(opts) {
		return nil, 0, ErrWorkflowInvalidPagination
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.storedWorkflowRevisions[appID+"/"+name]
	items := make([]StoredWorkflowRevision, len(all))
	for i := range all {
		items[i] = copyStoredWorkflowRevision(all[len(all)-1-i])
	}
	total := len(items)
	if opts.Offset >= total {
		return []StoredWorkflowRevision{}, total, nil
	}
	end := total
	if opts.Limit < total-opts.Offset {
		end = opts.Offset + opts.Limit
	}
	return items[opts.Offset:end], total, nil
}
func (m *MemStore) GetStoredWorkflowRevision(_ context.Context, appID, name string, version int64) (StoredWorkflowRevision, error) {
	if version <= 0 {
		return StoredWorkflowRevision{}, ErrStoredWorkflowRevisionNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, revision := range m.storedWorkflowRevisions[appID+"/"+name] {
		if revision.Version == version {
			return copyStoredWorkflowRevision(revision), nil
		}
	}
	return StoredWorkflowRevision{}, ErrStoredWorkflowRevisionNotFound
}
func (m *MemStore) EffectiveWorkflowDefinitions(_ context.Context, appID string, manifest json.RawMessage) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mergeRuntimeStoredWorkflowDefinitionsLocked(appID, manifest)
}
func (m *MemStore) MutateStoredWorkflow(_ context.Context, appID, name string, mutation StoredWorkflowMutation) (StoredWorkflow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return StoredWorkflow{}, ErrNotFound
	}
	account, ok := m.accounts[app.AccountID]
	if !ok {
		return StoredWorkflow{}, ErrNotFound
	}
	if err := storedWorkflowMutationGate(account.Plan, account.Active(), mutation); err != nil {
		return StoredWorkflow{}, err
	}
	key := appID + "/" + name
	var previous *StoredWorkflow
	if record, ok := m.storedWorkflows[key]; ok {
		previous = &record
	}
	if mutation.ActorAccountID == "" {
		mutation.ActorAccountID = app.AccountID
	}
	policy, exists := m.storedWorkflowPublishPolicies[appID]
	if !exists {
		policy = defaultPublishPolicy()
	}
	receipt := m.storedWorkflowPublishReceipts[storedWorkflowReceiptHash(mutation.CheckReceipt)]
	if err := validatePublicationReceipt(policy, receipt, appID, name, &mutation, previous); err != nil {
		return StoredWorkflow{}, err
	}
	dep := m.storedWorkflowDeploymentLocked(appID)
	next, err := mutateStoredWorkflow(appID, name, mutation, previous, dep.Workflows, m.storedWorkflowRecordsLocked(appID), account.Plan, dep.ID != "")
	if err != nil {
		return StoredWorkflow{}, err
	}
	if mutation.Action == "publish" || mutation.Action == "enable" && mutation.Enabled {
		var spec api.WorkflowSpec
		if err := json.Unmarshal(next.Published, &spec); err != nil {
			return StoredWorkflow{}, err
		}
		if err := m.validateWorkflowOutboundLocked(appID, app.AccountID, spec); err != nil {
			return StoredWorkflow{}, err
		}
	}
	m.storedWorkflowVersion++
	next.Version = m.storedWorkflowVersion
	if mutation.Action == "publish" {
		next.PublishedVersion = next.Version
		actorAccountID := mutation.ActorAccountID
		if actorAccountID == "" {
			actorAccountID = app.AccountID
		}
		if m.storedWorkflowRevisions == nil {
			m.storedWorkflowRevisions = make(map[string][]StoredWorkflowRevision)
		}
		m.storedWorkflowRevisions[key] = append(m.storedWorkflowRevisions[key], StoredWorkflowRevision{
			AppID: appID, Name: name, Version: next.PublishedVersion,
			CheckEvidence: storedWorkflowCheckEvidenceJSON(mutation.CheckEvidence),
			Definition:    cloneWorkflowJSON(next.Published), RecordedAt: next.UpdatedAt,
			PublishedByAccountID: actorAccountID, PublishedByAPIKeyID: mutation.ActorAPIKeyID,
		})
	}
	if m.storedWorkflows == nil {
		m.storedWorkflows = make(map[string]StoredWorkflow)
	}
	if mutation.Action == "delete" {
		m.clearStoredWorkflowLiveBaselinesLocked(appID, name)
		delete(m.storedWorkflows, key)
	} else {
		m.storedWorkflows[key] = copyStoredWorkflow(next)
	}
	if mutation.Action == "delete" || mutation.Action == "tests" && len(mutation.TestSuite) == 0 {
		m.clearStoredWorkflowLiveLocked(appID, name)
	}
	if mutation.Action != "save" && mutation.Action != "tests" {
		delete(m.workflowSchedules, key)
		for key, cursor := range m.workflowTenantSchedules {
			if cursor.AppID == appID && cursor.WorkflowName == name {
				cursor.DeploymentID, cursor.Status, cursor.LastRunID = "", WorkflowScheduleArmed, ""
				cursor.ScheduledFor = nil
				m.workflowTenantSchedules[key] = cursor
			}
		}
	}
	return next, nil
}
func storedWorkflowMutationGate(plan api.Plan, active bool, mutation StoredWorkflowMutation) error {
	action := mutation.Action
	if action == "tests" && len(mutation.TestSuite) == 0 {
		action = "clear-tests"
	}
	return storedWorkflowAccountGate(plan, active, action)
}
func storedWorkflowAccountGate(plan api.Plan, active bool, action string) error {
	if !active {
		return ErrWorkflowEventTargetUnavailable
	}
	// Removal and pausing remain possible after a downgrade.
	if !plan.WorkflowsAllowed() && (action == "save" || action == "publish" || action == "tests") {
		return api.ErrPlanWorkflowsNotAllowed(plan)
	}
	return nil
}
func storedWorkflowFromSQL(row sqlc.StoredWorkflow) StoredWorkflow {
	return StoredWorkflow{TestSuite: row.TestSuite, TestReport: row.TestReport, AppID: uuidFromPgtype(row.AppID).String(), Name: row.Name, Version: row.Version, Draft: row.Draft, Published: row.Published, PublishedVersion: row.PublishedVersion, Enabled: row.Enabled, UpdatedAt: row.UpdatedAt.Time}
}
func storedWorkflowRevisionFromSQL(row sqlc.StoredWorkflowRevision) StoredWorkflowRevision {
	revision := StoredWorkflowRevision{
		AppID: uuidFromPgtype(row.AppID).String(), Name: row.Name, Version: row.Version,
		CheckEvidence: cloneWorkflowJSON(row.CheckEvidence),
		Definition:    row.Definition, RecordedAt: row.RecordedAt.Time, LegacySnapshot: row.LegacySnapshot,
		PublishedByAccountID: uuidFromPgtype(row.PublishedByAccountID).String(),
	}
	if row.PublishedByApiKeyID.Valid {
		revision.PublishedByAPIKeyID = uuidFromPgtype(row.PublishedByApiKeyID).String()
	}
	return revision
}
func (s *PgStore) ListStoredWorkflows(ctx context.Context, appID string) ([]StoredWorkflow, error) {
	rows, err := sqlc.New().ListStoredWorkflows(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return nil, err
	}
	records := make([]StoredWorkflow, 0, len(rows))
	for _, row := range rows {
		records = append(records, storedWorkflowFromSQL(row))
	}
	return records, nil
}
func (s *PgStore) ListStoredWorkflowRevisions(ctx context.Context, appID, name string, opts StoredWorkflowRevisionListOptions) ([]StoredWorkflowRevision, int, error) {
	if !validStoredWorkflowRevisionListOptions(opts) {
		return nil, 0, ErrWorkflowInvalidPagination
	}
	q := sqlc.New()
	appUUID := mustPgUUID(appID)
	total, err := q.CountStoredWorkflowRevisions(ctx, s.pool, sqlc.CountStoredWorkflowRevisionsParams{AppID: appUUID, Name: name})
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.ListStoredWorkflowRevisions(ctx, s.pool, sqlc.ListStoredWorkflowRevisionsParams{AppID: appUUID, Name: name, Limit: int32(opts.Limit), Offset: int32(opts.Offset)})
	if err != nil {
		return nil, 0, err
	}
	revisions := make([]StoredWorkflowRevision, 0, len(rows))
	for _, row := range rows {
		revisions = append(revisions, storedWorkflowRevisionFromSQL(row))
	}
	return revisions, int(total), nil
}
func (s *PgStore) GetStoredWorkflowRevision(ctx context.Context, appID, name string, version int64) (StoredWorkflowRevision, error) {
	row, err := sqlc.New().GetStoredWorkflowRevision(ctx, s.pool, sqlc.GetStoredWorkflowRevisionParams{AppID: mustPgUUID(appID), Name: name, Version: version})
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredWorkflowRevision{}, ErrStoredWorkflowRevisionNotFound
	}
	if err != nil {
		return StoredWorkflowRevision{}, err
	}
	return storedWorkflowRevisionFromSQL(row), nil
}
func (s *PgStore) EffectiveWorkflowDefinitions(ctx context.Context, appID string, manifest json.RawMessage) (json.RawMessage, error) {
	if len(manifest) == 0 {
		manifest = json.RawMessage(`[]`)
	}
	return sqlc.New().EffectiveWorkflowDefinitions(ctx, s.pool, sqlc.EffectiveWorkflowDefinitionsParams{AppID: mustPgUUID(appID), Manifest: manifest})
}
func (s *PgStore) MutateStoredWorkflow(ctx context.Context, appID, name string, mutation StoredWorkflowMutation) (StoredWorkflow, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StoredWorkflow{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return StoredWorkflow{}, err
	}
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) || target.AppStatus == string(AppDeleted) {
		return StoredWorkflow{}, ErrNotFound
	}
	if err != nil {
		return StoredWorkflow{}, err
	}
	plan := api.Plan(target.Plan)
	if err := storedWorkflowMutationGate(plan, (target.AccountStatus == "active" || target.AccountStatus == "past_due") && !target.AbuseHoldAt.Valid, mutation); err != nil {
		return StoredWorkflow{}, err
	}
	next, err := mutateStoredWorkflowTx(ctx, tx, q, appID, name, mutation, plan, pgUUIDString(target.AccountID))
	if err != nil {
		return StoredWorkflow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StoredWorkflow{}, err
	}
	return next, nil
}
func mutateStoredWorkflowTx(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, appID, name string, mutation StoredWorkflowMutation, plan api.Plan, accountID string) (StoredWorkflow, error) {
	if mutation.ActorAccountID == "" {
		mutation.ActorAccountID = accountID
	}
	rows, err := q.ListStoredWorkflows(ctx, tx, mustPgUUID(appID))
	if err != nil {
		return StoredWorkflow{}, err
	}
	records := make([]StoredWorkflow, 0, len(rows))
	var previous *StoredWorkflow
	for _, row := range rows {
		record := storedWorkflowFromSQL(row)
		records = append(records, record)
		if record.Name == name {
			previous = &record
		}
	}
	if err := enforcePublicationReceiptTx(ctx, tx, appID, name, &mutation, previous); err != nil {
		return StoredWorkflow{}, err
	}
	dep, err := q.StoredWorkflowManifest(ctx, tx, mustPgUUID(appID))
	hasDeployment := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return StoredWorkflow{}, err
	}
	next, err := mutateStoredWorkflow(appID, name, mutation, previous, dep.Workflows, records, plan, hasDeployment)
	if err != nil {
		return StoredWorkflow{}, err
	}
	if mutation.Action == "publish" || mutation.Action == "enable" && mutation.Enabled {
		var spec api.WorkflowSpec
		if err := json.Unmarshal(next.Published, &spec); err != nil {
			return StoredWorkflow{}, err
		}
		if err := validateWorkflowOutboundTx(ctx, tx, appID, accountID, spec); err != nil {
			return StoredWorkflow{}, err
		}
	}
	next.Version, err = q.NextStoredWorkflowVersion(ctx, tx)
	if err != nil {
		return StoredWorkflow{}, err
	}
	if mutation.Action == "publish" {
		next.PublishedVersion = next.Version
	}
	if mutation.Action == "delete" {
		err = q.DeleteStoredWorkflow(ctx, tx, sqlc.DeleteStoredWorkflowParams{AppID: mustPgUUID(appID), Name: name})
	} else {
		err = q.SaveStoredWorkflow(ctx, tx, sqlc.SaveStoredWorkflowParams{TestSuite: next.TestSuite, TestReport: next.TestReport, AppID: mustPgUUID(appID), Name: name, Version: next.Version, Draft: next.Draft, Published: next.Published, PublishedVersion: next.PublishedVersion, Enabled: next.Enabled, UpdatedAt: pgtype.Timestamptz{Time: next.UpdatedAt, Valid: true}})
	}
	if err != nil {
		return StoredWorkflow{}, err
	}
	if mutation.Action == "publish" {
		apiKeyID := pgtype.UUID{}
		if mutation.ActorAPIKeyID != "" {
			apiKeyID = mustPgUUID(mutation.ActorAPIKeyID)
		}
		if err := q.InsertStoredWorkflowRevision(ctx, tx, sqlc.InsertStoredWorkflowRevisionParams{
			AppID: mustPgUUID(appID), Name: name, Version: next.PublishedVersion,
			CheckEvidence: storedWorkflowCheckEvidenceJSON(mutation.CheckEvidence),
			Definition:    next.Published, RecordedAt: pgtype.Timestamptz{Time: next.UpdatedAt, Valid: true},
			PublishedByAccountID: mustPgUUID(mutation.ActorAccountID), PublishedByApiKeyID: apiKeyID,
		}); err != nil {
			return StoredWorkflow{}, err
		}
	}
	if mutation.Action == "tests" && len(mutation.TestSuite) == 0 {
		if err := q.DeleteStoredWorkflowLiveSuites(ctx, tx, sqlc.DeleteStoredWorkflowLiveSuitesParams{AppID: mustPgUUID(appID), Name: name}); err != nil {
			return StoredWorkflow{}, err
		}
		if err := q.DeleteStoredWorkflowLiveVerifications(ctx, tx, sqlc.DeleteStoredWorkflowLiveVerificationsParams{AppID: mustPgUUID(appID), Name: name}); err != nil {
			return StoredWorkflow{}, err
		}
	}
	if mutation.Action != "save" && mutation.Action != "tests" {
		err = q.DeleteStoredWorkflowScheduleCursor(ctx, tx, sqlc.DeleteStoredWorkflowScheduleCursorParams{AppID: mustPgUUID(appID), WorkflowName: name})
		if err == nil {
			err = q.RearmTenantStoredWorkflowSchedules(ctx, tx, sqlc.RearmTenantStoredWorkflowSchedulesParams{AppID: mustPgUUID(appID), WorkflowName: name})
		}
	}
	return next, err
}

// Deployments and dashboard writes reserve names under the same app lock.
// Promotion checks again because drafts can change while a build is pending.
func storedWorkflowDeploymentQuota(manifest json.RawMessage, records []StoredWorkflow, plan api.Plan) error {
	if len(records) == 0 {
		return nil
	}
	var definitions []api.WorkflowSpec
	if len(manifest) > 0 {
		if err := json.Unmarshal(manifest, &definitions); err != nil {
			return err
		}
	}
	if len(definitions) == 0 && !plan.WorkflowsAllowed() {
		return nil
	}
	names := make(map[string]bool)
	for _, definition := range definitions {
		names[definition.Name] = true
	}
	for _, record := range records {
		names[record.Name] = true
	}
	if len(names) > plan.WorkflowMaxPerApp() {
		return api.ErrStoredWorkflowDefinitionsQuota(plan, plan.WorkflowMaxPerApp(), len(names))
	}
	return nil
}
func (s *PgStore) checkDeploymentStoredWorkflows(ctx context.Context, tx pgx.Tx, dep Deployment) error {
	if normalizedDeploymentScope(dep.Scope) != "default" {
		return nil
	}
	q := sqlc.New()
	rows, err := q.ListStoredWorkflows(ctx, tx, mustPgUUID(dep.AppID))
	if err != nil || len(rows) == 0 {
		return err
	}
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(dep.AppID))
	if err != nil {
		return err
	}
	records := make([]StoredWorkflow, 0, len(rows))
	for _, row := range rows {
		records = append(records, storedWorkflowFromSQL(row))
	}
	return storedWorkflowDeploymentQuota(dep.Workflows, records, api.Plan(target.Plan))
}
func (m *MemStore) checkDeploymentStoredWorkflowsLocked(dep Deployment) error {
	if normalizedDeploymentScope(dep.Scope) != "default" {
		return nil
	}
	app := m.apps[dep.AppID]
	account := m.accounts[app.AccountID]
	return storedWorkflowDeploymentQuota(dep.Workflows, m.storedWorkflowRecordsLocked(dep.AppID), account.Plan)
}

func storedWorkflowCheckEvidenceJSON(e *api.StoredWorkflowCheckEvidence) json.RawMessage {
	if e == nil {
		return nil
	}
	raw, _ := json.Marshal(e)
	return raw
}
