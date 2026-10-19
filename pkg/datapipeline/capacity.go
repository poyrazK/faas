package datapipeline

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

type capacityQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type CapacityObserver struct {
	Store          *connectioncapacity.Store
	Qualifications []connectioncapacity.Qualification
	Target         func(context.Context, string, string) (connectioncapacity.Target, error)
}

func validateQualifications(qualifications []connectioncapacity.Qualification, now time.Time) error {
	if len(qualifications) > api.DataPipelineQualificationsMax {
		return ErrInvalid
	}
	endpoints, databases := map[string]bool{}, map[string]bool{}
	scopes := map[string]connectioncapacity.Qualification{}
	for _, q := range qualifications {
		// Expiration closes admission, not worker startup: paused/deleting
		// pipelines must remain recoverable with an expired qualification.
		if !q.Valid(q.QualifiedAt) || q.QualifiedAt.After(now) || ((len(q.Endpoints) > 0) == (q.DatabaseID != "")) {
			return ErrInvalid
		}
		if q.DatabaseID != "" && (!validID(q.DatabaseID) || q.DatabaseGeneration < 1 || q.BackendFingerprint == "" || q.DataResourceID == "") {
			return ErrInvalid
		}
		if databases[q.DatabaseID] && q.DatabaseID != "" {
			return ErrInvalid
		}
		databases[q.DatabaseID] = true
		for _, ep := range q.Endpoints {
			host, port, err := net.SplitHostPort(ep)
			if err != nil || host == "" || port == "" || ep != strings.ToLower(ep) || endpoints[ep] {
				return ErrInvalid
			}
			endpoints[ep] = true
		}
		if old, ok := scopes[q.Scope]; ok && (old.Placement != q.Placement || old.Generation != q.Generation || old.Allocation != q.Allocation || old.External != q.External || old.ReportSHA256 != q.ReportSHA256 || !old.QualifiedAt.Equal(q.QualifiedAt) || !old.ExpiresAt.Equal(q.ExpiresAt)) {
			return ErrInvalid
		}
		scopes[q.Scope] = q
	}
	return nil
}

// observe measures actual settings but requires a separately qualified usable
// allocation and a bounded allowance for every external consumer. Settings
// alone never establish provider entitlement or native replication support.
func observeCapacity(ctx context.Context, db capacityQuery, q connectioncapacity.Qualification) (connectioncapacity.Evidence, error) {
	now := time.Now().UTC()
	if !q.Valid(now) {
		return connectioncapacity.Evidence{}, connectioncapacity.ErrUnknown
	}
	var measured connectioncapacity.Limits
	err := db.QueryRow(ctx, `SELECT current_setting('max_connections')::bigint-current_setting('superuser_reserved_connections')::bigint-current_setting('reserved_connections')::bigint,
 current_setting('max_replication_slots')::bigint,current_setting('max_wal_senders')::bigint,
 least(current_setting('max_logical_replication_workers')::bigint,current_setting('max_worker_processes')::bigint)`).Scan(&measured.Connections, &measured.Slots, &measured.Senders, &measured.Workers)
	if err != nil {
		return connectioncapacity.Evidence{}, connectioncapacity.ErrUnknown
	}
	limits := connectioncapacity.Limits{Connections: max(0, min(q.Allocation.Connections, measured.Connections-q.External.Connections)), Slots: max(0, min(q.Allocation.Slots, measured.Slots-q.External.Slots)), Senders: max(0, min(q.Allocation.Senders, measured.Senders-q.External.Senders)), Workers: max(0, min(q.Allocation.Workers, measured.Workers-q.External.Workers))}
	return connectioncapacity.Evidence{Key: connectioncapacity.Fingerprint(q.Scope), Placement: q.Placement, Generation: q.Generation, ReportSHA256: q.ReportSHA256,
		Limits: limits, ObservedAt: now, ExpiresAt: minTime(now.Add(api.DataPipelineCapacityMaxAge), q.ExpiresAt)}, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Compatibility metadata lives longer than capacity receipts. Never keep a
// stored green allocation visible after its shorter evidence window expires.
func expireCheckCapacity(check *api.DataConnectionCheck, now time.Time) {
	if check.Capacity == nil {
		return
	}
	for _, entry := range []struct {
		name     string
		endpoint *api.DataEndpointCapacity
	}{{"source", check.Capacity.Source}, {"mirror", check.Capacity.Destination}} {
		r := entry.endpoint
		if r == nil {
			continue
		}
		observed, first := time.Parse(time.RFC3339Nano, r.ObservedAt)
		expires, second := time.Parse(time.RFC3339Nano, r.ExpiresAt)
		if r.State != "needs_qualification" && (first != nil || second != nil || now.Before(observed) || !now.Before(expires) || !expires.After(observed) || expires.Sub(observed) > api.DataPipelineCapacityMaxAge) {
			r.State = "needs_qualification"
			r.LimitingResource = ""
			for i, issue := range check.Checks {
				if strings.HasPrefix(issue.Code, entry.name+"_capacity_") {
					check.Checks[i] = capacityIssue(entry.name, r)
				}
			}
		}
	}
	r := check.Capacity
	r.State = "needs_qualification"
	if r.Source != nil && r.Destination != nil && r.Source.State == "ready" && r.Destination.State == "ready" {
		r.State = "ready"
	}
	if (r.Source != nil && r.Source.State == "insufficient_capacity") || (r.Destination != nil && r.Destination.State == "insufficient_capacity") {
		r.State = "insufficient_capacity"
	}
}

func (o *CapacityObserver) source(ctx context.Context, job ConnectionCheck, raw string, db capacityQuery) (connectioncapacity.Snapshot, error) {
	if o == nil || o.Store == nil {
		return connectioncapacity.Snapshot{}, connectioncapacity.ErrUnknown
	}
	u, err := url.Parse(raw)
	if err != nil {
		return connectioncapacity.Snapshot{}, connectioncapacity.ErrUnknown
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	endpoint := net.JoinHostPort(strings.ToLower(u.Hostname()), port)
	for _, q := range o.Qualifications {
		if slices.Contains(q.Endpoints, endpoint) {
			e, err := observeCapacity(ctx, db, q)
			if err != nil {
				return connectioncapacity.Snapshot{}, err
			}
			return o.Store.RecordSource(ctx, job.AccountID, job.ConnectionID, job.ConnectionRevision, e)
		}
	}
	return connectioncapacity.Snapshot{}, connectioncapacity.ErrUnknown
}

func (o *CapacityObserver) target(ctx context.Context, account, id string, db capacityQuery) (connectioncapacity.Snapshot, error) {
	if o == nil || o.Store == nil || o.Target == nil {
		return connectioncapacity.Snapshot{}, connectioncapacity.ErrUnknown
	}
	t, err := o.Target(ctx, account, id)
	if err != nil {
		return connectioncapacity.Snapshot{}, connectioncapacity.ErrUnknown
	}
	for _, q := range o.Qualifications {
		if q.DatabaseID == id && q.DatabaseGeneration == t.Generation && q.BackendFingerprint == t.BackendFingerprint && q.DataResourceID == t.DataResourceID {
			e, err := observeCapacity(ctx, db, q)
			if err != nil {
				return connectioncapacity.Snapshot{}, err
			}
			return o.Store.RecordTarget(ctx, t, e)
		}
	}
	return connectioncapacity.Snapshot{}, connectioncapacity.ErrUnknown
}

func capacityReport(s connectioncapacity.Snapshot, d connectioncapacity.Limits, err error) *api.DataEndpointCapacity {
	r := &api.DataEndpointCapacity{State: "needs_qualification", RequiredConnections: d.Connections}
	if err != nil || !s.Fresh(time.Now()) {
		return r
	}
	r.State = "ready"
	r.UsableConnections = s.Connections
	r.ReservedConnections = s.Reserved.Connections
	r.ObservedAt = s.ObservedAt.UTC().Format(time.RFC3339Nano)
	r.ExpiresAt = s.ExpiresAt.UTC().Format(time.RFC3339Nano)
	for _, limit := range []struct {
		name                     string
		used, required, capacity int64
	}{{"connections", s.Reserved.Connections, d.Connections, s.Connections}, {"replication_slots", s.Reserved.Slots, d.Slots, s.Slots}, {"wal_senders", s.Reserved.Senders, d.Senders, s.Senders}, {"logical_workers", s.Reserved.Workers, d.Workers, s.Workers}} {
		if limit.used+limit.required > limit.capacity {
			r.State = "insufficient_capacity"
			r.LimitingResource = limit.name
			break
		}
	}
	return r
}

func capacityIssue(endpoint string, r *api.DataEndpointCapacity) api.DataConnectionIssue {
	i := api.DataConnectionIssue{Code: endpoint + "_capacity_" + r.State, State: "unknown", Message: endpoint + " capacity needs qualification", Remediation: "Ask the operator to qualify this exact database allocation, its shared server aliases, and external consumers, then run a new check."}
	if r.State == "ready" {
		i.State = "passed"
		i.Message = fmt.Sprintf("%s capacity ready: %d required, %d reserved, %d usable connections", endpoint, r.RequiredConnections, r.ReservedConnections, r.UsableConnections)
		i.Remediation = ""
	}
	if r.State == "insufficient_capacity" {
		i.State = "blocked"
		i.Message = endpoint + " has insufficient " + r.LimitingResource
		i.Remediation = "Release unused pipelines or bindings, or ask the operator to qualify a larger allocation, then run a new check."
	}
	return i
}

func (s *PostgresStore) AdmitCapacity(ctx context.Context, p Pipeline) error {
	admitted, err := s.q.PipelineAdmitCapacity(ctx, s.pool, sqlc.PipelineAdmitCapacityParams{ID: p.ID, AccountID: p.AccountID, Revision: p.Revision, LeaseToken: p.LeaseToken})
	if err != nil {
		return connectioncapacity.Classify(err)
	}
	if !admitted {
		return ErrConflict
	}
	return nil
}

func capacityBlock(err error) error {
	if errors.Is(err, connectioncapacity.ErrInsufficient) {
		return &blocked{code: "insufficient_capacity"}
	}
	return &blocked{code: "capacity_needs_qualification"}
}

func PipelineCapacityStatus(ctx context.Context, store *connectioncapacity.Store, account, id string) (*api.DataPipelineCapacity, error) {
	holds, err := store.Pipeline(ctx, account, id)
	if err != nil {
		return nil, err
	}
	source, sourceOK := holds["source"]
	target, targetOK := holds["target"]
	var sourceErr, targetErr error
	if !sourceOK {
		sourceErr = connectioncapacity.ErrUnknown
	}
	if !targetOK {
		targetErr = connectioncapacity.ErrUnknown
	}
	r := &api.DataPipelineCapacity{State: "needs_qualification", Source: capacityReport(source, connectioncapacity.Limits{}, sourceErr), Destination: capacityReport(target, connectioncapacity.Limits{}, targetErr)}
	if r.Source.State == "ready" && r.Destination.State == "ready" {
		r.State = "ready"
	}
	if r.Source.State == "insufficient_capacity" || r.Destination.State == "insufficient_capacity" {
		r.State = "insufficient_capacity"
	}
	return r, nil
}
