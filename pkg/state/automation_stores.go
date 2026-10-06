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

func (m *MemStore) automationRecordsLocked(appID string) []Automation {
	records := make([]Automation, 0)
	for _, record := range m.automations {
		if record.AppID == appID {
			records = append(records, copyAutomation(record))
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	return records
}
func (m *MemStore) automationDeploymentLocked(appID string) Deployment {
	var result Deployment
	for _, candidate := range m.deployments {
		if candidate.AppID == appID && candidate.Status == DeployLive && (candidate.Scope == "" || candidate.Scope == "default") && (result.ID == "" || deploymentPreferredForWake(candidate, result)) {
			result = candidate
		}
	}
	return result
}
func (m *MemStore) ListAutomations(_ context.Context, appID string) ([]Automation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.automationRecordsLocked(appID), nil
}
func (m *MemStore) ListAutomationRevisions(_ context.Context, appID, name string, opts AutomationRevisionListOptions) ([]AutomationRevision, int, error) {
	if !validAutomationRevisionListOptions(opts) {
		return nil, 0, ErrWorkflowInvalidPagination
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.automationRevisions[appID+"/"+name]
	items := make([]AutomationRevision, len(all))
	for i := range all {
		items[i] = copyAutomationRevision(all[len(all)-1-i])
	}
	total := len(items)
	if opts.Offset >= total {
		return []AutomationRevision{}, total, nil
	}
	end := total
	if opts.Limit < total-opts.Offset {
		end = opts.Offset + opts.Limit
	}
	return items[opts.Offset:end], total, nil
}
func (m *MemStore) GetAutomationRevision(_ context.Context, appID, name string, version int64) (AutomationRevision, error) {
	if version <= 0 {
		return AutomationRevision{}, ErrAutomationRevisionNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, revision := range m.automationRevisions[appID+"/"+name] {
		if revision.Version == version {
			return copyAutomationRevision(revision), nil
		}
	}
	return AutomationRevision{}, ErrAutomationRevisionNotFound
}
func (m *MemStore) EffectiveWorkflowDefinitions(_ context.Context, appID string, manifest json.RawMessage) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return mergeAutomationDefinitions(manifest, m.automationRecordsLocked(appID))
}
func (m *MemStore) MutateAutomation(_ context.Context, appID, name string, mutation AutomationMutation) (Automation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return Automation{}, ErrNotFound
	}
	account, ok := m.accounts[app.AccountID]
	if !ok {
		return Automation{}, ErrNotFound
	}
	if err := automationAccountGate(account.Plan, account.Active(), mutation.Action); err != nil {
		return Automation{}, err
	}
	key := appID + "/" + name
	var previous *Automation
	if record, ok := m.automations[key]; ok {
		previous = &record
	}
	dep := m.automationDeploymentLocked(appID)
	next, err := mutateAutomation(appID, name, mutation, previous, dep.Workflows, m.automationRecordsLocked(appID), account.Plan, dep.ID != "")
	if err != nil {
		return Automation{}, err
	}
	if mutation.Action == "publish" || mutation.Action == "enable" && mutation.Enabled {
		var spec api.WorkflowSpec
		if err := json.Unmarshal(next.Published, &spec); err != nil {
			return Automation{}, err
		}
		if err := m.validateWorkflowOutboundLocked(appID, app.AccountID, spec); err != nil {
			return Automation{}, err
		}
	}
	m.automationVersion++
	next.Version = m.automationVersion
	if mutation.Action == "publish" {
		next.PublishedVersion = next.Version
		actorAccountID := mutation.ActorAccountID
		if actorAccountID == "" {
			actorAccountID = app.AccountID
		}
		if m.automationRevisions == nil {
			m.automationRevisions = make(map[string][]AutomationRevision)
		}
		m.automationRevisions[key] = append(m.automationRevisions[key], AutomationRevision{
			AppID: appID, Name: name, Version: next.PublishedVersion,
			Definition: cloneWorkflowJSON(next.Published), RecordedAt: next.UpdatedAt,
			PublishedByAccountID: actorAccountID, PublishedByAPIKeyID: mutation.ActorAPIKeyID,
		})
	}
	if m.automations == nil {
		m.automations = make(map[string]Automation)
	}
	if mutation.Action == "delete" {
		delete(m.automations, key)
	} else {
		m.automations[key] = copyAutomation(next)
	}
	if mutation.Action != "save" {
		delete(m.workflowSchedules, key)
	}
	return next, nil
}
func automationAccountGate(plan api.Plan, active bool, action string) error {
	if !active {
		return ErrWorkflowEventTargetUnavailable
	}
	// Removal and pausing remain possible after a downgrade.
	if !plan.WorkflowsAllowed() && (action == "save" || action == "publish") {
		return api.ErrPlanWorkflowsNotAllowed(plan)
	}
	return nil
}
func automationFromSQL(row sqlc.WorkflowAutomationDefinition) Automation {
	return Automation{AppID: uuidFromPgtype(row.AppID).String(), Name: row.Name, Version: row.Version, Draft: row.Draft, Published: row.Published, PublishedVersion: row.PublishedVersion, Enabled: row.Enabled, UpdatedAt: row.UpdatedAt.Time}
}
func automationRevisionFromSQL(row sqlc.WorkflowAutomationRevision) AutomationRevision {
	revision := AutomationRevision{
		AppID: uuidFromPgtype(row.AppID).String(), Name: row.Name, Version: row.Version,
		Definition: row.Definition, RecordedAt: row.RecordedAt.Time, LegacySnapshot: row.LegacySnapshot,
		PublishedByAccountID: uuidFromPgtype(row.PublishedByAccountID).String(),
	}
	if row.PublishedByApiKeyID.Valid {
		revision.PublishedByAPIKeyID = uuidFromPgtype(row.PublishedByApiKeyID).String()
	}
	return revision
}
func (s *PgStore) ListAutomations(ctx context.Context, appID string) ([]Automation, error) {
	rows, err := sqlc.New().ListAutomations(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return nil, err
	}
	records := make([]Automation, 0, len(rows))
	for _, row := range rows {
		records = append(records, automationFromSQL(row))
	}
	return records, nil
}
func (s *PgStore) ListAutomationRevisions(ctx context.Context, appID, name string, opts AutomationRevisionListOptions) ([]AutomationRevision, int, error) {
	if !validAutomationRevisionListOptions(opts) {
		return nil, 0, ErrWorkflowInvalidPagination
	}
	q := sqlc.New()
	appUUID := mustPgUUID(appID)
	total, err := q.CountWorkflowAutomationRevisions(ctx, s.pool, sqlc.CountWorkflowAutomationRevisionsParams{AppID: appUUID, Name: name})
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.ListWorkflowAutomationRevisions(ctx, s.pool, sqlc.ListWorkflowAutomationRevisionsParams{AppID: appUUID, Name: name, Limit: int32(opts.Limit), Offset: int32(opts.Offset)})
	if err != nil {
		return nil, 0, err
	}
	revisions := make([]AutomationRevision, 0, len(rows))
	for _, row := range rows {
		revisions = append(revisions, automationRevisionFromSQL(row))
	}
	return revisions, int(total), nil
}
func (s *PgStore) GetAutomationRevision(ctx context.Context, appID, name string, version int64) (AutomationRevision, error) {
	row, err := sqlc.New().GetWorkflowAutomationRevision(ctx, s.pool, sqlc.GetWorkflowAutomationRevisionParams{AppID: mustPgUUID(appID), Name: name, Version: version})
	if errors.Is(err, pgx.ErrNoRows) {
		return AutomationRevision{}, ErrAutomationRevisionNotFound
	}
	if err != nil {
		return AutomationRevision{}, err
	}
	return automationRevisionFromSQL(row), nil
}
func (s *PgStore) EffectiveWorkflowDefinitions(ctx context.Context, appID string, manifest json.RawMessage) (json.RawMessage, error) {
	if len(manifest) == 0 {
		manifest = json.RawMessage(`[]`)
	}
	return sqlc.New().EffectiveWorkflowDefinitions(ctx, s.pool, sqlc.EffectiveWorkflowDefinitionsParams{AppID: mustPgUUID(appID), Manifest: manifest})
}
func (s *PgStore) MutateAutomation(ctx context.Context, appID, name string, mutation AutomationMutation) (Automation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Automation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return Automation{}, err
	}
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) || target.AppStatus == string(AppDeleted) {
		return Automation{}, ErrNotFound
	}
	if err != nil {
		return Automation{}, err
	}
	plan := api.Plan(target.Plan)
	if err := automationAccountGate(plan, (target.AccountStatus == "active" || target.AccountStatus == "past_due") && !target.AbuseHoldAt.Valid, mutation.Action); err != nil {
		return Automation{}, err
	}
	next, err := mutateAutomationTx(ctx, tx, q, appID, name, mutation, plan, pgUUIDString(target.AccountID))
	if err != nil {
		return Automation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Automation{}, err
	}
	return next, nil
}
func mutateAutomationTx(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, appID, name string, mutation AutomationMutation, plan api.Plan, accountID string) (Automation, error) {
	if mutation.ActorAccountID == "" {
		mutation.ActorAccountID = accountID
	}
	rows, err := q.ListAutomations(ctx, tx, mustPgUUID(appID))
	if err != nil {
		return Automation{}, err
	}
	records := make([]Automation, 0, len(rows))
	var previous *Automation
	for _, row := range rows {
		record := automationFromSQL(row)
		records = append(records, record)
		if record.Name == name {
			previous = &record
		}
	}
	dep, err := q.AutomationManifest(ctx, tx, mustPgUUID(appID))
	hasDeployment := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Automation{}, err
	}
	next, err := mutateAutomation(appID, name, mutation, previous, dep.Workflows, records, plan, hasDeployment)
	if err != nil {
		return Automation{}, err
	}
	if mutation.Action == "publish" || mutation.Action == "enable" && mutation.Enabled {
		var spec api.WorkflowSpec
		if err := json.Unmarshal(next.Published, &spec); err != nil {
			return Automation{}, err
		}
		if err := validateWorkflowOutboundTx(ctx, tx, appID, accountID, spec); err != nil {
			return Automation{}, err
		}
	}
	next.Version, err = q.NextAutomationVersion(ctx, tx)
	if err != nil {
		return Automation{}, err
	}
	if mutation.Action == "publish" {
		next.PublishedVersion = next.Version
	}
	if mutation.Action == "delete" {
		err = q.DeleteAutomation(ctx, tx, sqlc.DeleteAutomationParams{AppID: mustPgUUID(appID), Name: name})
	} else {
		err = q.SaveAutomation(ctx, tx, sqlc.SaveAutomationParams{AppID: mustPgUUID(appID), Name: name, Version: next.Version, Draft: next.Draft, Published: next.Published, PublishedVersion: next.PublishedVersion, Enabled: next.Enabled, UpdatedAt: pgtype.Timestamptz{Time: next.UpdatedAt, Valid: true}})
	}
	if err != nil {
		return Automation{}, err
	}
	if mutation.Action == "publish" {
		apiKeyID := pgtype.UUID{}
		if mutation.ActorAPIKeyID != "" {
			apiKeyID = mustPgUUID(mutation.ActorAPIKeyID)
		}
		if err := q.InsertWorkflowAutomationRevision(ctx, tx, sqlc.InsertWorkflowAutomationRevisionParams{
			AppID: mustPgUUID(appID), Name: name, Version: next.PublishedVersion,
			Definition: next.Published, RecordedAt: pgtype.Timestamptz{Time: next.UpdatedAt, Valid: true},
			PublishedByAccountID: mustPgUUID(mutation.ActorAccountID), PublishedByApiKeyID: apiKeyID,
		}); err != nil {
			return Automation{}, err
		}
	}
	if mutation.Action != "save" {
		err = q.DeleteAutomationScheduleCursor(ctx, tx, sqlc.DeleteAutomationScheduleCursorParams{AppID: mustPgUUID(appID), WorkflowName: name})
	}
	return next, err
}

// Deployments and dashboard writes reserve names under the same app lock.
// Promotion checks again because drafts can change while a build is pending.
func automationDeploymentQuota(manifest json.RawMessage, records []Automation, plan api.Plan) error {
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
		return api.ErrAutomationDefinitionsQuota(plan, plan.WorkflowMaxPerApp(), len(names))
	}
	return nil
}
func (s *PgStore) checkDeploymentAutomations(ctx context.Context, tx pgx.Tx, dep Deployment) error {
	if normalizedDeploymentScope(dep.Scope) != "default" {
		return nil
	}
	q := sqlc.New()
	rows, err := q.ListAutomations(ctx, tx, mustPgUUID(dep.AppID))
	if err != nil || len(rows) == 0 {
		return err
	}
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(dep.AppID))
	if err != nil {
		return err
	}
	records := make([]Automation, 0, len(rows))
	for _, row := range rows {
		records = append(records, automationFromSQL(row))
	}
	return automationDeploymentQuota(dep.Workflows, records, api.Plan(target.Plan))
}
func (m *MemStore) checkDeploymentAutomationsLocked(dep Deployment) error {
	if normalizedDeploymentScope(dep.Scope) != "default" {
		return nil
	}
	app := m.apps[dep.AppID]
	account := m.accounts[app.AccountID]
	return automationDeploymentQuota(dep.Workflows, m.automationRecordsLocked(dep.AppID), account.Plan)
}
