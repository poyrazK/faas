package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Analytics cohorts (ADR 20261119102741 §3): saved queries whose members
// apid recomputes hourly and the gateway stamps on requests.

// ErrAnalyticsCohortLimit is returned when an environment already holds the
// maximum number of cohorts.
var ErrAnalyticsCohortLimit = errors.New("state: analytics cohort limit reached")

// Cohort kinds.
const (
	CohortDid    = "did"
	CohortDidNot = "did_not"
	CohortFunnel = "funnel"
)

// AnalyticsCohortDefinition is a cohort's query. did and did_not use Event,
// Where, WithinDays and (did) MinCount; funnel uses Steps, Window and
// WithinDays.
type AnalyticsCohortDefinition struct {
	Kind       string             `json:"kind"`
	Event      string             `json:"event,omitempty"`
	Where      map[string]string  `json:"where,omitempty"`
	WithinDays int                `json:"within_days"`
	MinCount   int                `json:"min_count,omitempty"`
	Steps      []flags.MetricStep `json:"steps,omitempty"`
	Window     string             `json:"window,omitempty"`
}

// AnalyticsCohort is one cohort and its last refresh.
type AnalyticsCohort struct {
	ID, AccountID, EnvironmentID, Key string
	Definition                        AnalyticsCohortDefinition
	Members                           int
	Truncated                         bool
	RefreshedAt                       *time.Time
	RefreshRequestedAt                time.Time
	RefreshError                      string
	CreatedBy                         string
	CreatedAt                         time.Time
	// Plan is set on cohorts listed for refresh.
	Plan string
}

// AnalyticsCohortStore keeps cohorts, computes their members and answers
// the gateway's membership lookups.
type AnalyticsCohortStore interface {
	CreateAnalyticsCohort(ctx context.Context, c AnalyticsCohort, maxCohorts int) (AnalyticsCohort, error)
	GetAnalyticsCohort(ctx context.Context, environmentID, key string) (AnalyticsCohort, error)
	ListAnalyticsCohorts(ctx context.Context, environmentID string) ([]AnalyticsCohort, error)
	DeleteAnalyticsCohort(ctx context.Context, environmentID, key string) (bool, error)
	RequestAnalyticsCohortRefresh(ctx context.Context, environmentID, key string, now time.Time) (AnalyticsCohort, error)
	// DueAnalyticsCohorts lists cohorts whose refresh was requested or is
	// older than staleBefore, with their account's plan.
	DueAnalyticsCohorts(ctx context.Context, staleBefore time.Time, limit int) ([]AnalyticsCohort, error)
	// ReplaceAnalyticsCohortMembers replaces the cohort's members with the
	// listed customers of its account and notifies the gateway.
	ReplaceAnalyticsCohortMembers(ctx context.Context, c AnalyticsCohort, customers []string, truncated bool, now time.Time) error
	// FailAnalyticsCohortRefresh records a failed refresh and keeps the
	// previous members.
	FailAnalyticsCohortRefresh(ctx context.Context, cohortID string, now time.Time, msg string) error
	// AnalyticsCohortDid lists customers with at least minCount events
	// matching m since m.From, in customer order, at most limit.
	AnalyticsCohortDid(ctx context.Context, m AnalyticsMatch, minCount, limit int) ([]string, error)
	// AnalyticsCohortDidNot lists customers with any event since
	// activeSince that sent no event matching m since m.From.
	AnalyticsCohortDidNot(ctx context.Context, m AnalyticsMatch, activeSince time.Time, limit int) ([]string, error)
	// AnalyticsCustomerTotals returns each listed customer's events matching
	// m in [m.From, m.Until): how many and the sum of their values.
	AnalyticsCustomerTotals(ctx context.Context, m AnalyticsMatch, customers []string) (map[string]AnalyticsTotal, error)
}

// AnalyticsTotal is one customer's matching events and their value sum.
type AnalyticsTotal struct {
	Events int64
	Value  float64
}

func analyticsCohortFromRow(r sqlc.AnalyticsCohort) AnalyticsCohort {
	c := AnalyticsCohort{
		ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), EnvironmentID: pgUUIDString(r.EnvironmentID), Key: r.Key,
		Members: int(r.Members), Truncated: r.Truncated, RefreshRequestedAt: r.RefreshRequestedAt.Time,
		RefreshError: r.RefreshError, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.Time,
	}
	_ = json.Unmarshal(r.Definition, &c.Definition)
	if r.RefreshedAt.Valid {
		t := r.RefreshedAt.Time
		c.RefreshedAt = &t
	}
	return c
}

func analyticsCohortErr(op string, err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case isUniqueViolation(err):
		return ErrConflict
	default:
		return fmt.Errorf("analytics cohort %s: %w", op, err)
	}
}

// CreateAnalyticsCohort implements AnalyticsCohortStore.
func (s *PgStore) CreateAnalyticsCohort(ctx context.Context, c AnalyticsCohort, maxCohorts int) (AnalyticsCohort, error) {
	def, err := json.Marshal(c.Definition)
	if err != nil {
		return AnalyticsCohort{}, fmt.Errorf("analytics cohort create: %w", err)
	}
	row, err := sqlc.New().CreateAnalyticsCohort(ctx, s.pool, sqlc.CreateAnalyticsCohortParams{
		AccountID: uuidToPgtype(c.AccountID), EnvironmentID: uuidToPgtype(c.EnvironmentID), Key: c.Key,
		Definition: def, CreatedBy: c.CreatedBy, MaxCohorts: int64(maxCohorts)})
	if errors.Is(err, pgx.ErrNoRows) {
		return AnalyticsCohort{}, ErrAnalyticsCohortLimit
	}
	if err != nil {
		return AnalyticsCohort{}, analyticsCohortErr("create", err)
	}
	return analyticsCohortFromRow(row), nil
}

// GetAnalyticsCohort implements AnalyticsCohortStore.
func (s *PgStore) GetAnalyticsCohort(ctx context.Context, environmentID, key string) (AnalyticsCohort, error) {
	row, err := sqlc.New().GetAnalyticsCohort(ctx, s.pool, sqlc.GetAnalyticsCohortParams{EnvironmentID: uuidToPgtype(environmentID), Key: key})
	if err != nil {
		return AnalyticsCohort{}, analyticsCohortErr("get", err)
	}
	return analyticsCohortFromRow(row), nil
}

// ListAnalyticsCohorts implements AnalyticsCohortStore.
func (s *PgStore) ListAnalyticsCohorts(ctx context.Context, environmentID string) ([]AnalyticsCohort, error) {
	rows, err := sqlc.New().ListAnalyticsCohorts(ctx, s.pool, uuidToPgtype(environmentID))
	if err != nil {
		return nil, analyticsCohortErr("list", err)
	}
	out := make([]AnalyticsCohort, 0, len(rows))
	for _, r := range rows {
		out = append(out, analyticsCohortFromRow(r))
	}
	return out, nil
}

// DeleteAnalyticsCohort implements AnalyticsCohortStore.
func (s *PgStore) DeleteAnalyticsCohort(ctx context.Context, environmentID, key string) (bool, error) {
	var n int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		var err error
		if n, err = q.DeleteAnalyticsCohort(ctx, tx, sqlc.DeleteAnalyticsCohortParams{EnvironmentID: uuidToPgtype(environmentID), Key: key}); err != nil || n == 0 {
			return err
		}
		return q.NotifyAnalyticsCohortsChanged(ctx, tx, environmentID)
	})
	if err != nil {
		return false, analyticsCohortErr("delete", err)
	}
	return n > 0, nil
}

// RequestAnalyticsCohortRefresh implements AnalyticsCohortStore.
func (s *PgStore) RequestAnalyticsCohortRefresh(ctx context.Context, environmentID, key string, now time.Time) (AnalyticsCohort, error) {
	row, err := sqlc.New().RequestAnalyticsCohortRefresh(ctx, s.pool, sqlc.RequestAnalyticsCohortRefreshParams{
		EnvironmentID: uuidToPgtype(environmentID), Key: key, Now: pgTimestamptzUTC(now)})
	if err != nil {
		return AnalyticsCohort{}, analyticsCohortErr("refresh", err)
	}
	return analyticsCohortFromRow(row), nil
}

// DueAnalyticsCohorts implements AnalyticsCohortStore.
func (s *PgStore) DueAnalyticsCohorts(ctx context.Context, staleBefore time.Time, limit int) ([]AnalyticsCohort, error) {
	rows, err := sqlc.New().DueAnalyticsCohorts(ctx, s.pool, sqlc.DueAnalyticsCohortsParams{StaleBefore: pgTimestamptzUTC(staleBefore), MaxRows: int32(limit)})
	if err != nil {
		return nil, analyticsCohortErr("due", err)
	}
	out := make([]AnalyticsCohort, 0, len(rows))
	for _, r := range rows {
		c := analyticsCohortFromRow(sqlc.AnalyticsCohort{ID: r.ID, AccountID: r.AccountID, EnvironmentID: r.EnvironmentID, Key: r.Key,
			Definition: r.Definition, Members: r.Members, Truncated: r.Truncated, RefreshedAt: r.RefreshedAt,
			RefreshRequestedAt: r.RefreshRequestedAt, RefreshError: r.RefreshError, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt})
		c.Plan = r.Plan
		out = append(out, c)
	}
	return out, nil
}

// ReplaceAnalyticsCohortMembers implements AnalyticsCohortStore. The
// members, the cohort row and the notification commit together.
func (s *PgStore) ReplaceAnalyticsCohortMembers(ctx context.Context, c AnalyticsCohort, customers []string, truncated bool, now time.Time) error {
	ids := make([]pgtype.UUID, 0, len(customers))
	for _, id := range customers {
		if _, err := uuid.Parse(id); err == nil {
			ids = append(ids, uuidToPgtype(id))
		}
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		if err := q.DeleteAnalyticsCohortMembers(ctx, tx, uuidToPgtype(c.ID)); err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := q.InsertAnalyticsCohortMembers(ctx, tx, sqlc.InsertAnalyticsCohortMembersParams{
				CohortID: uuidToPgtype(c.ID), CustomerIds: ids, AccountID: uuidToPgtype(c.AccountID)}); err != nil {
				return err
			}
		}
		if err := q.FinishAnalyticsCohortRefresh(ctx, tx, sqlc.FinishAnalyticsCohortRefreshParams{
			ID: uuidToPgtype(c.ID), Truncated: truncated, Now: pgTimestamptzUTC(now), ServedRequest: pgTimestamptzUTC(c.RefreshRequestedAt), RefreshError: ""}); err != nil {
			return err
		}
		return q.NotifyAnalyticsCohortsChanged(ctx, tx, c.EnvironmentID)
	})
	if err != nil {
		return fmt.Errorf("analytics cohort members: %w", err)
	}
	return nil
}

// FailAnalyticsCohortRefresh implements AnalyticsCohortStore.
func (s *PgStore) FailAnalyticsCohortRefresh(ctx context.Context, cohortID string, now time.Time, msg string) error {
	msg = safetext.Truncate(msg, 512)
	if err := sqlc.New().FailAnalyticsCohortRefresh(ctx, s.pool, sqlc.FailAnalyticsCohortRefreshParams{
		ID: uuidToPgtype(cohortID), Now: pgTimestamptzUTC(now), RefreshError: msg}); err != nil {
		return fmt.Errorf("analytics cohort refresh failure: %w", err)
	}
	return nil
}

// AnalyticsCohortDid implements AnalyticsCohortStore.
func (s *PgStore) AnalyticsCohortDid(ctx context.Context, m AnalyticsMatch, minCount, limit int) ([]string, error) {
	filter, err := analyticsFilter(m.Properties)
	if err != nil {
		return nil, fmt.Errorf("analytics cohort did: %w", err)
	}
	rows, err := sqlc.New().AnalyticsCohortDid(ctx, s.pool, sqlc.AnalyticsCohortDidParams{
		EnvironmentID: uuidToPgtype(m.EnvironmentID), Name: m.Name, Filter: filter, SinceAt: pgTimestamptzUTC(m.From),
		MinCount: int64(minCount), MaxRows: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("analytics cohort did: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, pgUUIDString(r))
	}
	return out, nil
}

// AnalyticsCohortDidNot implements AnalyticsCohortStore.
func (s *PgStore) AnalyticsCohortDidNot(ctx context.Context, m AnalyticsMatch, activeSince time.Time, limit int) ([]string, error) {
	filter, err := analyticsFilter(m.Properties)
	if err != nil {
		return nil, fmt.Errorf("analytics cohort did not: %w", err)
	}
	rows, err := sqlc.New().AnalyticsCohortDidNot(ctx, s.pool, sqlc.AnalyticsCohortDidNotParams{
		EnvironmentID: uuidToPgtype(m.EnvironmentID), ActiveSince: pgTimestamptzUTC(activeSince), Name: m.Name,
		Filter: filter, SinceAt: pgTimestamptzUTC(m.From), MaxRows: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("analytics cohort did not: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, pgUUIDString(r))
	}
	return out, nil
}

// AnalyticsCustomerTotals implements AnalyticsCohortStore.
func (s *PgStore) AnalyticsCustomerTotals(ctx context.Context, m AnalyticsMatch, customers []string) (map[string]AnalyticsTotal, error) {
	out := map[string]AnalyticsTotal{}
	if len(customers) == 0 {
		return out, nil
	}
	filter, err := analyticsFilter(m.Properties)
	if err != nil {
		return nil, fmt.Errorf("analytics customer totals: %w", err)
	}
	ids := make([]pgtype.UUID, 0, len(customers))
	for _, id := range customers {
		ids = append(ids, uuidToPgtype(id))
	}
	rows, err := sqlc.New().AnalyticsCustomerTotals(ctx, s.pool, sqlc.AnalyticsCustomerTotalsParams{
		EnvironmentID: uuidToPgtype(m.EnvironmentID), Name: m.Name, Filter: filter,
		FromAt: pgTimestamptzUTC(m.From), UntilAt: pgTimestamptzUTC(m.Until), CustomerIds: ids})
	if err != nil {
		return nil, fmt.Errorf("analytics customer totals: %w", err)
	}
	for _, r := range rows {
		out[pgUUIDString(r.CustomerID)] = AnalyticsTotal{Events: r.Events, Value: r.Total}
	}
	return out, nil
}

// AnalyticsCohortsForDeploymentCustomer returns the sorted keys of the
// cohorts of the deployment's environment that contain the customer, for
// the gateway's X-Faas-Flag-Cohorts (ADR 20261119102741 §4).
func (s *PgStore) AnalyticsCohortsForDeploymentCustomer(ctx context.Context, deploymentID, customerID string) ([]string, error) {
	if !validUUIDs(deploymentID, customerID) {
		// No project deployment or verified customer: no cohorts.
		return nil, nil
	}
	keys, err := sqlc.New().AnalyticsCohortsForDeploymentCustomer(ctx, s.pool, sqlc.AnalyticsCohortsForDeploymentCustomerParams{
		CustomerID: uuidToPgtype(customerID), DeploymentID: uuidToPgtype(deploymentID)})
	if err != nil {
		return nil, fmt.Errorf("analytics cohorts for customer: %w", err)
	}
	return keys, nil
}

// validUUIDs reports whether every id parses as a UUID.
func validUUIDs(ids ...string) bool {
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	return true
}
