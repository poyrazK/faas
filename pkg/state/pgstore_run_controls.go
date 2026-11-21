package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func runLimitSQL(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}
func runLimitAPI(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}
func runPolicyFromSQL(r sqlc.ExecutionRunBudget) api.RunBudgetPolicy {
	return api.RunBudgetPolicy{Kind: r.Kind, Ref: r.Ref, Revision: r.Revision, Enabled: r.Enabled, Limits: api.RunBudgetLimits{MaxConcurrent: runLimitAPI(r.MaxConcurrent), MaxInvocationsPerDay: runLimitAPI(r.MaxInvocations), MaxComputeMBMSPerDay: runLimitAPI(r.MaxComputeMbMs)}, Alerts: []api.RunBudgetAlert{}}
}
func (s *PgStore) runControlTx(ctx context.Context, account string, apply func(pgx.Tx, api.Plan) error) error {
	id, err := parsePgUUID(account)
	if err != nil {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	a, err := sqlc.New().ExecutionLockAccount(ctx, tx, id)
	if err != nil {
		return mapErr(err)
	}
	if err := apply(tx, api.Plan(a.Plan)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) PutRunBudget(ctx context.Context, account, kind, ref string, req api.SetRunBudgetRequest) (api.RunBudgetPolicy, error) {
	var out api.RunBudgetPolicy
	if !validRunPolicy(kind, ref, req) {
		return out, ErrExecutionInvalid
	}
	err := s.runControlTx(ctx, account, func(tx pgx.Tx, plan api.Plan) error {
		q := sqlc.New()
		id := mustPgUUID(account)
		old, err := q.RunBudgetGet(ctx, tx, sqlc.RunBudgetGetParams{AccountID: id, Kind: kind, Ref: ref})
		exists := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if old.Revision != req.ExpectedRevision {
			return ErrRunControlConflict
		}
		if kind == "task" {
			ok, err := q.RunTaskNameExists(ctx, tx, sqlc.RunTaskNameExistsParams{AccountID: id, Name: ref})
			if err != nil {
				return err
			}
			if !ok {
				return ErrNotFound
			}
		} else if !exists {
			n, err := q.RunCustomerCount(ctx, tx, id)
			if err != nil {
				return err
			}
			max := int64(api.RunCustomersByPlan[plan.PlanIndex()])
			if n >= max {
				return &ExecutionTaskQuotaError{"run_customers", max, n + 1}
			}
		}
		r, err := q.RunBudgetPut(ctx, tx, sqlc.RunBudgetPutParams{AccountID: id, Kind: kind, Ref: ref, Revision: old.Revision + 1, Enabled: req.Enabled, MaxConcurrent: runLimitSQL(req.Limits.MaxConcurrent), MaxInvocations: runLimitSQL(req.Limits.MaxInvocationsPerDay), MaxComputeMbMs: runLimitSQL(req.Limits.MaxComputeMBMSPerDay)})
		if err != nil {
			return err
		}
		out, err = readRunPolicy(ctx, tx, account, runPolicyFromSQL(r))
		return err
	})
	return out, err
}
func (s *PgStore) GetRunBudget(ctx context.Context, account, kind, ref string) (api.RunBudgetPolicy, error) {
	id, err := parsePgUUID(account)
	if err != nil {
		return api.RunBudgetPolicy{}, ErrNotFound
	}
	r, err := sqlc.New().RunBudgetGet(ctx, s.pool, sqlc.RunBudgetGetParams{AccountID: id, Kind: kind, Ref: ref})
	if err != nil {
		return api.RunBudgetPolicy{}, mapErr(err)
	}
	return readRunPolicy(ctx, s.pool, account, runPolicyFromSQL(r))
}
func readRunUsage(ctx context.Context, db sqlc.DBTX, account, kind, ref string, at time.Time) (api.RunBudgetUsage, error) {
	day := runControlDay(at)
	r, err := sqlc.New().RunBudgetUsage(ctx, db, sqlc.RunBudgetUsageParams{AccountID: mustPgUUID(account), Kind: kind, Ref: ref, DayStart: executionTime(day), DayEnd: executionTime(day.AddDate(0, 0, 1))})
	return api.RunBudgetUsage{Day: day.Format(time.DateOnly), Active: r.Active, Invocations: r.Invocations, ComputeUsedMBMS: r.UsedMbMs, ComputeReservedMBMS: r.ReservedMbMs}, err
}
func runAlertFromSQL(r sqlc.ExecutionRunBudgetAlert) api.RunBudgetAlert {
	return api.RunBudgetAlert{EventID: pgUUIDString(r.EventID), Kind: r.Kind, Ref: r.Ref, Day: r.Day.Time.UTC().Format(time.DateOnly), Metric: r.Metric, ThresholdPercent: int(r.ThresholdPercent), Limit: r.Maximum, Observed: r.Observed, CreatedAt: r.CreatedAt.Time.UTC().Format(time.RFC3339Nano)}
}
func readRunPolicy(ctx context.Context, db sqlc.DBTX, account string, p api.RunBudgetPolicy) (api.RunBudgetPolicy, error) {
	databaseNow, clockErr := sqlc.New().RunControlNow(ctx, db)
	if clockErr != nil {
		return p, clockErr
	}
	at := databaseNow.Time.UTC()
	var err error
	p.Usage, err = readRunUsage(ctx, db, account, p.Kind, p.Ref, at)
	if err != nil {
		return p, err
	}
	alerts, err := sqlc.New().RunBudgetAlertsGet(ctx, db, sqlc.RunBudgetAlertsGetParams{AccountID: mustPgUUID(account), Kind: p.Kind, Ref: p.Ref, Day: pgtype.Date{Time: runControlDay(at), Valid: true}})
	if err != nil {
		return p, err
	}
	for _, a := range alerts {
		p.Alerts = append(p.Alerts, runAlertFromSQL(a))
	}
	return p, nil
}
func runSubjectSQL(ctx context.Context, db sqlc.DBTX, account, kind, id string) (pgtype.UUID, error) {
	if !validRunSubject(kind, id) {
		return pgtype.UUID{}, ErrExecutionInvalid
	}
	if kind == "api_key" {
		v, err := sqlc.New().RunControlKeyFamily(ctx, db, sqlc.RunControlKeyFamilyParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
		return v, mapErr(err)
	}
	ok, err := sqlc.New().RunTaskEndpointsOwned(ctx, db, sqlc.RunTaskEndpointsOwnedParams{AccountID: mustPgUUID(account), EndpointIds: []pgtype.UUID{mustPgUUID(id)}})
	if err != nil {
		return pgtype.UUID{}, err
	}
	if !ok {
		return pgtype.UUID{}, ErrNotFound
	}
	return mustPgUUID(id), nil
}
func runBindingFromSQL(r sqlc.ExecutionRunBinding, publicID string) api.RunAttributionBinding {
	return api.RunAttributionBinding{Enabled: r.Enabled, SubjectKind: r.SubjectKind, SubjectID: publicID, Revision: r.Revision, RunAttribution: api.RunAttribution{CustomerRef: r.CustomerRef, AgentRef: r.AgentRef}}
}
func (s *PgStore) PutRunBinding(ctx context.Context, account, kind, id string, req api.SetRunAttributionBindingRequest) (api.RunAttributionBinding, error) {
	var out api.RunAttributionBinding
	if !validRunSubject(kind, id) || !api.ValidRunControlRef(req.CustomerRef) || req.AgentRef != "" && !api.ValidRunControlRef(req.AgentRef) || req.ExpectedRevision < 0 {
		return out, ErrExecutionInvalid
	}
	err := s.runControlTx(ctx, account, func(tx pgx.Tx, plan api.Plan) error {
		q := sqlc.New()
		accountID := mustPgUUID(account)
		subject, err := runSubjectSQL(ctx, tx, account, kind, id)
		if err != nil {
			return err
		}
		if _, err := q.RunBudgetGet(ctx, tx, sqlc.RunBudgetGetParams{AccountID: accountID, Kind: "customer", Ref: req.CustomerRef}); err != nil {
			return mapErr(err)
		}
		old, err := q.RunBindingGet(ctx, tx, sqlc.RunBindingGetParams{AccountID: accountID, SubjectKind: kind, SubjectID: subject})
		exists := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if old.Revision != req.ExpectedRevision {
			return ErrRunControlConflict
		}
		if !exists {
			n, err := q.RunBindingCount(ctx, tx, accountID)
			if err != nil {
				return err
			}
			max := int64(api.RunBindingsByPlan[plan.PlanIndex()])
			if n >= max {
				return &ExecutionTaskQuotaError{"run_bindings", max, n + 1}
			}
		}
		if req.AgentRef == "" {
			req.AgentRef = old.AgentRef
			if req.AgentRef == "" {
				req.AgentRef = kind + ":" + id
			}
		}
		r, err := q.RunBindingPut(ctx, tx, sqlc.RunBindingPutParams{AccountID: accountID, SubjectKind: kind, SubjectID: subject, CustomerRef: req.CustomerRef, AgentRef: req.AgentRef, Revision: old.Revision + 1})
		if err != nil {
			return err
		}
		out = runBindingFromSQL(r, id)
		return nil
	})
	return out, err
}
func (s *PgStore) GetRunBinding(ctx context.Context, account, kind, id string) (api.RunAttributionBinding, error) {
	if _, err := parsePgUUID(account); err != nil {
		return api.RunAttributionBinding{}, ErrNotFound
	}
	subject, err := runSubjectSQL(ctx, s.pool, account, kind, id)
	if err != nil {
		return api.RunAttributionBinding{}, err
	}
	r, err := sqlc.New().RunBindingGet(ctx, s.pool, sqlc.RunBindingGetParams{AccountID: mustPgUUID(account), SubjectKind: kind, SubjectID: subject})
	if err != nil {
		return api.RunAttributionBinding{}, mapErr(err)
	}
	return runBindingFromSQL(r, id), nil
}
func (s *PgStore) DeleteRunBinding(ctx context.Context, account, kind, id string, revision int64) error {
	return s.runControlTx(ctx, account, func(tx pgx.Tx, _ api.Plan) error {
		subject, err := runSubjectSQL(ctx, tx, account, kind, id)
		if err != nil {
			return err
		}
		q := sqlc.New()
		args := sqlc.RunBindingGetParams{AccountID: mustPgUUID(account), SubjectKind: kind, SubjectID: subject}
		old, err := q.RunBindingGet(ctx, tx, args)
		if err != nil {
			return mapErr(err)
		}
		if revision < 1 || old.Revision != revision {
			return ErrRunControlConflict
		}
		return q.RunBindingDisable(ctx, tx, sqlc.RunBindingDisableParams(args))
	})
}
func prepareRunControlsTx(ctx context.Context, tx pgx.Tx, p CreateExecutionParams, task ExecutionTaskVersion) (runControlReservation, []api.RunBudgetPolicy, error) {
	q := sqlc.New()
	bindings := []api.RunAttributionBinding{}
	for _, subject := range []struct{ kind, id string }{{"api_key", derefString(p.RunsPrincipalID)}, {"agent_endpoint", runCallbackEndpoint(p.Task)}} {
		if subject.id == "" {
			continue
		}
		b, err := q.RunBindingGet(ctx, tx, sqlc.RunBindingGetParams{AccountID: mustPgUUID(p.AccountID), SubjectKind: subject.kind, SubjectID: mustPgUUID(subject.id)})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return runControlReservation{}, nil, err
		}
		if err == nil && b.Enabled {
			bindings = append(bindings, runBindingFromSQL(b, ""))
		}
	}
	a, err := runAttribution(p, bindings)
	if err != nil {
		return runControlReservation{}, nil, err
	}
	databaseNow, err := q.RunControlNow(ctx, tx)
	if err != nil {
		return runControlReservation{}, nil, err
	}
	r := runControlReservation{AccountID: p.AccountID, Attribution: a, TaskName: task.Name, ReservedMBMS: runReservationMBMS(p), AdmittedAt: databaseNow.Time.UTC()}
	policies := []api.RunBudgetPolicy{}
	for _, target := range []struct{ kind, ref string }{{"customer", a.CustomerRef}, {"task", task.Name}} {
		if target.ref == "" {
			continue
		}
		v, err := q.RunBudgetGet(ctx, tx, sqlc.RunBudgetGetParams{AccountID: mustPgUUID(p.AccountID), Kind: target.kind, Ref: target.ref})
		if errors.Is(err, pgx.ErrNoRows) {
			if target.kind == "customer" {
				return r, nil, ErrNotFound
			}
			continue
		}
		if err != nil {
			return r, nil, err
		}
		policy := runPolicyFromSQL(v)
		usage, err := readRunUsage(ctx, tx, p.AccountID, policy.Kind, policy.Ref, r.AdmittedAt)
		if err != nil {
			return r, nil, err
		}
		if err := checkRunBudget(policy, usage, r.ReservedMBMS); err != nil {
			return r, nil, err
		}
		policies = append(policies, policy)
	}
	return r, policies, nil
}
func runCallbackEndpoint(task *ExecutionTaskAdmission) string {
	if task != nil && task.PipelineEndpointID != "" {
		return task.PipelineEndpointID
	}
	if task == nil || task.Callback == nil {
		return ""
	}
	return task.Callback.EndpointID
}
func addRunControlsTx(ctx context.Context, tx pgx.Tx, r *Execution, reservation runControlReservation, policies []api.RunBudgetPolicy) error {
	q := sqlc.New()
	err := q.RunReservationInsert(ctx, tx, sqlc.RunReservationInsertParams{ExecutionID: mustPgUUID(r.ID), AccountID: mustPgUUID(r.AccountID), CustomerRef: reservation.Attribution.CustomerRef, AgentRef: reservation.Attribution.AgentRef, TaskName: reservation.TaskName, ReservedMbMs: reservation.ReservedMBMS, AdmittedAt: executionTime(reservation.AdmittedAt)})
	if err != nil {
		return err
	}
	if reservation.Attribution.CustomerRef != "" || reservation.Attribution.AgentRef != "" {
		a := reservation.Attribution
		r.Attribution = &a
	}
	for _, p := range policies {
		u, err := readRunUsage(ctx, tx, r.AccountID, p.Kind, p.Ref, reservation.AdmittedAt)
		if err != nil {
			return err
		}
		for _, a := range runBudgetAlerts(r.AccountID, p, u, reservation.AdmittedAt) {
			day, _ := time.Parse(time.DateOnly, a.Day)
			n, err := q.RunBudgetAlertInsert(ctx, tx, sqlc.RunBudgetAlertInsertParams{EventID: mustPgUUID(a.EventID), AccountID: mustPgUUID(r.AccountID), Kind: a.Kind, Ref: a.Ref, Day: pgtype.Date{Time: day, Valid: true}, Metric: a.Metric, ThresholdPercent: int32(a.ThresholdPercent), Maximum: a.Limit, Observed: a.Observed, CreatedAt: executionTime(reservation.AdmittedAt)})
			if err != nil {
				return err
			}
			if n == 0 {
				continue
			}
			body, err := json.Marshal(a)
			if err != nil {
				return err
			}
			if err := q.RunBudgetAlertDeliveries(ctx, tx, sqlc.RunBudgetAlertDeliveriesParams{AccountID: mustPgUUID(r.AccountID), Payload: body, EventID: mustPgUUID(a.EventID)}); err != nil {
				return err
			}
		}
	}
	return nil
}
func attachRunAttribution(ctx context.Context, db sqlc.DBTX, r *Execution) error {
	a, err := sqlc.New().RunAttributionGet(ctx, db, sqlc.RunAttributionGetParams{AccountID: mustPgUUID(r.AccountID), ExecutionID: mustPgUUID(r.ID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if a.CustomerRef != "" || a.AgentRef != "" {
		r.Attribution = &api.RunAttribution{CustomerRef: a.CustomerRef, AgentRef: a.AgentRef}
	}
	return nil
}
func (s *PgStore) ListRunHistory(ctx context.Context, q RunHistoryQuery) ([]api.RunHistoryEntry, error) {
	if q.Filter.TaskVersion < 0 || q.Filter.TaskVersion > api.ExecutionTaskVersionMax {
		return nil, ErrExecutionInvalid
	}
	account, err := parsePgUUID(q.AccountID)
	if err != nil {
		return nil, ErrNotFound
	}
	var principal, beforeID pgtype.UUID
	if !q.Wide {
		principal, err = parsePgUUID(q.PrincipalID)
		if err != nil {
			return nil, ErrNotFound
		}
	}
	if !q.Before.IsZero() {
		beforeID, err = parsePgUUID(q.BeforeID)
		if err != nil {
			return nil, ErrExecutionInvalid
		}
	}
	limit := q.Limit
	if limit < 1 || limit > api.RunHistoryPageMax+1 {
		limit = api.RunHistoryPageMax + 1
	}
	f := q.Filter
	rows, err := sqlc.New().RunHistoryList(ctx, s.pool, sqlc.RunHistoryListParams{AccountID: account, Wide: q.Wide, PrincipalID: principal, TaskName: f.TaskName, TaskVersion: int32(f.TaskVersion), CustomerRef: f.CustomerRef, AgentRef: f.AgentRef, Status: string(f.Status), Since: optionalRunTime(f.Since), Until: optionalRunTime(f.Until), BeforeAt: optionalRunTime(q.Before), BeforeID: beforeID, PageLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]api.RunHistoryEntry, 0, len(rows))
	now := time.Now().UTC()
	for _, v := range rows {
		r := Execution{ID: pgUUIDString(v.ID), Runtime: api.ExecutionRuntime(v.Runtime), Status: api.ExecutionStatus(v.Status), CreatedAt: v.CreatedAt.Time, Usage: api.ExecutionUsage{WallTimeMS: v.WallTimeMs, CPUTimeMS: v.CpuTimeMs, PeakMemoryMB: int(v.PeakMemoryMb)}}
		if v.TaskID.Valid {
			r.Task = &api.ExecutionTaskRef{ID: pgUUIDString(v.TaskID), Name: v.TaskName, Version: int(v.TaskVersion), Digest: v.TaskDigest}
		}
		if v.CustomerRef != "" || v.AgentRef != "" {
			r.Attribution = &api.RunAttribution{CustomerRef: v.CustomerRef, AgentRef: v.AgentRef}
		}
		if v.StartedAt.Valid {
			r.StartedAt = &v.StartedAt.Time
		}
		if v.FinishedAt.Valid {
			r.FinishedAt = &v.FinishedAt.Time
		}
		if v.FailureCode != "" {
			r.FailureCode = &v.FailureCode
		}
		entry := runHistoryEntry(r, now)
		entry.ResultValidationStatus = v.ResultValidationStatus
		out = append(out, entry)
	}
	return out, nil
}
func optionalRunTime(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return executionTime(t)
}

var _ RunControlStore = (*PgStore)(nil)
