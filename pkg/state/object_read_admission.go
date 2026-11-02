package state

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ObjectReadAdmission is what admits one bucket's reads under gateway-safety
// accounting, read without row locks (ADR-20261030075301). Err is nil when
// reads are admitted; otherwise it is the error AdmitObjectURL returns for a
// read. The counts are the month's so far, and the maxima are the
// plan-tightened ceilings they are checked against.
type ObjectReadAdmission struct {
	Err error
	// DownloadErr refuses object bodies, not HEAD or listing, while the
	// account stores more than its plan's ceiling (ADR-20261101214152).
	DownloadErr       error
	Period            time.Time
	Requests          int64
	EgressBytes       int64
	Authorizations    int64
	MaxRequests       int64
	MaxEgressBytes    int64
	MaxAuthorizations int64
}

// ObjectReadUsage is reads a gateway admitted from a cached decision and has
// not written yet: one bucket's totals for one month.
type ObjectReadUsage struct {
	BucketID       string
	Period         time.Time
	Requests       int64
	Authorizations int64
	EgressBytes    int64
}

var (
	_ ObjectReadAdmissionStore = (*MemStore)(nil)
	_ ObjectReadAdmissionStore = (*PgStore)(nil)
)

// ObjectReadAdmissionStore serves cached read admission (ADR-20261030075301).
type ObjectReadAdmissionStore interface {
	// ObjectReadAdmission reads bucket's read decision with plain reads. Its
	// error is an infrastructure failure; a refusal is the decision's Err.
	ObjectReadAdmission(ctx context.Context, account, bucket string, at time.Time, p api.ObjectStoragePolicy) (ObjectReadAdmission, error)
	// RecordObjectReadUsage adds batched read counts in one transaction,
	// without owner locks. Counts of deleted buckets are dropped.
	RecordObjectReadUsage(ctx context.Context, usage []ObjectReadUsage) error
}

// objectReadAdmission is AdmitObjectURL's read check followed by
// ReserveObjectStorageGatewayRequest's, as one decision.
func objectReadAdmission(s ObjectUsageSnapshot, bucket string, at time.Time, p api.ObjectStoragePolicy) ObjectReadAdmission {
	out := ObjectReadAdmission{Period: ObjectStoragePeriod(at)}
	if _, _, err := checkObjectAdmission(s, bucket, 0, 0, false, false, p, at); err != nil {
		out.Err = err
		return out
	}
	if err := checkGatewayRequest(s, bucket, at, p); err != nil {
		out.Err = err
		return out
	}
	p = s.planPolicy(p)
	u, _ := summarizeObjectUsage(s, p, at)
	out.DownloadErr = storedOverPlanCeiling(s, u)
	out.Requests, out.EgressBytes, out.Authorizations = u.RequestCount, u.EgressBytes, u.Authorizations
	out.MaxRequests, out.MaxEgressBytes, out.MaxAuthorizations = p.MaxMonthlyRequests, p.MaxMonthlyEgressBytes, p.MaxMonthlyAuthorizations
	return out
}

func validObjectReadUsage(usage []ObjectReadUsage) bool {
	for _, u := range usage {
		if u.BucketID == "" || u.Period.IsZero() || !u.Period.Equal(ObjectStoragePeriod(u.Period)) || u.Requests < 0 || u.Authorizations < 0 || u.EgressBytes < 0 ||
			u.Requests > api.MaxObjectStoragePolicyValue || u.Authorizations > api.MaxObjectStoragePolicyValue || u.EgressBytes > api.MaxObjectStoragePolicyValue {
			return false
		}
	}
	return true
}

func (m *MemStore) ObjectReadAdmission(ctx context.Context, account, bucket string, at time.Time, p api.ObjectStoragePolicy) (ObjectReadAdmission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ObjectReadAdmission{}, err
	}
	owned, ok := m.objectBuckets[bucket]
	if !ok || owned.AccountID != account {
		return ObjectReadAdmission{Period: ObjectStoragePeriod(at), Err: ErrNotFound}, nil
	}
	return objectReadAdmission(m.objectUsageLocked(m.objectBillingSubjectLocked(owned), at), bucket, at, p), nil
}

func (m *MemStore) RecordObjectReadUsage(ctx context.Context, usage []ObjectReadUsage) error {
	if !validObjectReadUsage(usage) {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	for _, u := range usage {
		b, ok := m.objectBuckets[u.BucketID]
		if !ok {
			continue
		}
		if m.objectProviderRequests == nil {
			m.objectProviderRequests = map[string]int64{}
		}
		key := objectProviderRequestKey(u.BucketID, u.Period)
		m.objectProviderRequests[key] += u.Requests
		m.objectProviderRequests[key+"\x00egress"] += u.EgressBytes
		if u.Authorizations > 0 {
			if m.objectAuthorizations == nil {
				m.objectAuthorizations = map[string]int64{}
			}
			m.objectAuthorizations[m.objectBillingSubjectLocked(b)+u.Period.String()] += u.Authorizations
		}
	}
	return nil
}

func (s *pgCore) ObjectReadAdmission(ctx context.Context, account, bucket string, at time.Time, p api.ObjectStoragePolicy) (ObjectReadAdmission, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return ObjectReadAdmission{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	subject, err := sqlc.New().ObjectStorageReadOwner(ctx, tx, sqlc.ObjectStorageReadOwnerParams{BucketID: mustPgUUID(bucket), AccountID: mustPgUUID(account)})
	if errors.Is(err, pgx.ErrNoRows) {
		// AdmitObjectURL's owner lock finds no row for an inactive owner too.
		return ObjectReadAdmission{Period: ObjectStoragePeriod(at), Err: ErrNotFound}, nil
	}
	if err != nil {
		return ObjectReadAdmission{}, err
	}
	snapshot, err := readObjectUsage(ctx, tx, pgUUIDString(subject), at)
	if err != nil {
		return ObjectReadAdmission{}, err
	}
	return objectReadAdmission(snapshot, bucket, at, p), nil
}

// RecordObjectReadUsage writes counter rows in key order, so concurrent
// flushes from several gateways never deadlock.
func (s *pgCore) RecordObjectReadUsage(ctx context.Context, usage []ObjectReadUsage) error {
	if !validObjectReadUsage(usage) {
		return ErrConflict
	}
	usage = slices.Clone(usage)
	slices.SortFunc(usage, func(a, b ObjectReadUsage) int {
		if c := strings.Compare(a.BucketID, b.BucketID); c != 0 {
			return c
		}
		return a.Period.Compare(b.Period)
	})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	type authorization struct {
		subject pgtype.UUID
		period  time.Time
	}
	authorizations := map[authorization]int64{}
	periods := map[time.Time][]pgtype.UUID{}
	pending := map[string]int64{}
	for _, u := range usage {
		if u.Requests > 0 || u.EgressBytes > 0 {
			if err = q.ObjectReadUsageRecordMetrics(ctx, tx, sqlc.ObjectReadUsageRecordMetricsParams{BucketID: mustPgUUID(u.BucketID), PeriodStart: objectUsageTime(u.Period), Requests: u.Requests, EgressBytes: u.EgressBytes}); err != nil {
				return mapErr(err)
			}
		}
		if u.Authorizations > 0 {
			periods[u.Period] = append(periods[u.Period], mustPgUUID(u.BucketID))
			pending[u.BucketID+"\x00"+u.Period.String()] += u.Authorizations
		}
	}
	for period, buckets := range periods {
		rows, err := q.ObjectReadUsageSubjects(ctx, tx, sqlc.ObjectReadUsageSubjectsParams{PeriodStart: objectUsageTime(period), BucketIds: buckets})
		if err != nil {
			return mapErr(err)
		}
		for _, row := range rows {
			authorizations[authorization{row.Subject, period}] += pending[pgUUIDString(row.ID)+"\x00"+period.String()]
		}
	}
	keys := make([]authorization, 0, len(authorizations))
	for k := range authorizations {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b authorization) int {
		if c := strings.Compare(pgUUIDString(a.subject), pgUUIDString(b.subject)); c != 0 {
			return c
		}
		return a.period.Compare(b.period)
	})
	for _, k := range keys {
		if err = q.ObjectUsageAuthorizeBy(ctx, tx, sqlc.ObjectUsageAuthorizeByParams{AccountID: k.subject, PeriodStart: objectUsageTime(k.period), Count: authorizations[k]}); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit(ctx)
}
