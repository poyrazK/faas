package datapipeline

import (
	"context"
	"encoding/json"
	"math/big"
	"regexp"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

var decimalBound = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?$`)

func snapshotMode(p Pipeline) bool { return p.Operations != nil && p.Operations.Snapshot != nil }
func operationsJSON(c any) []byte {
	if c == nil {
		return []byte("{}")
	}
	b, _ := json.Marshal(c)
	if string(b) == "null" {
		return []byte("{}")
	}
	return b
}
func normalizedOperations(c *api.DataPipelineOperations) *api.DataPipelineOperations {
	if c == nil {
		return nil
	}
	var out api.DataPipelineOperations
	_ = json.Unmarshal(operationsJSON(c), &out)
	if out.Snapshot != nil {
		if out.Snapshot.MaxRows == 0 {
			out.Snapshot.MaxRows = api.DataPipelineSnapshotRowsMax
		}
		if out.Snapshot.MaxBytes == 0 {
			out.Snapshot.MaxBytes = api.DataPipelineSnapshotBytesMax
		}
	}
	return &out
}
func operationsStatus(p Pipeline) *api.DataPipelineOperationsStatus {
	var status api.DataPipelineOperationsStatus
	if p.OperationsStatus != nil {
		_ = json.Unmarshal(operationsJSON(p.OperationsStatus), &status)
	}
	return &status
}
func validOperations(c *api.DataPipelineOperations, tables []string) bool {
	if c == nil {
		return true
	}
	if c.Version != 1 {
		return false
	}
	validInterval := func(s int64) bool {
		return s >= api.DataPipelineScheduleMinSeconds && s <= api.DataPipelineScheduleMaxSeconds
	}
	if s := c.Snapshot; s != nil && (!validInterval(s.EverySeconds) || s.MaxRows < 1 || s.MaxRows > api.DataPipelineSnapshotRowsMax || s.MaxBytes < 1 || s.MaxBytes > api.DataPipelineSnapshotBytesMax) {
		return false
	}
	if w := c.Workload; w != nil {
		if w.QueryTimeoutMillis != 0 && (w.QueryTimeoutMillis < api.DataPipelineWorkQueryMinMillis || w.QueryTimeoutMillis > api.DataPipelineVerificationQueryTimeout.Milliseconds()) {
			return false
		}
		if w.BatchesPerTick < 0 || w.BatchesPerTick > api.DataPipelineVerificationChunksPerTick {
			return false
		}
		if v := w.Window; v != nil && (v.StartMinuteUTC < 0 || v.StartMinuteUTC >= api.DataPipelineMinutesPerDay || v.EndMinuteUTC < 0 || v.EndMinuteUTC >= api.DataPipelineMinutesPerDay || v.StartMinuteUTC == v.EndMinuteUTC) {
			return false
		}
	}
	if q := c.Quality; q != nil {
		if !validInterval(q.EverySeconds) || len(q.Rules) == 0 || len(q.Rules) > api.DataPipelineQualityRulesMax {
			return false
		}
		seen := map[string]bool{}
		for _, r := range q.Rules {
			if !identifier.MatchString(r.Name) || seen[r.Name] || !slices.Contains(tables, r.Table) || len(r.Columns) == 0 || len(r.Columns) > api.DataPipelineRelationshipKeysMax {
				return false
			}
			seen[r.Name] = true
			for _, cols := range [][]string{r.Columns, r.ReferenceColumns} {
				names := map[string]bool{}
				for _, col := range cols {
					if !validServingColumn(col) || names[col] {
						return false
					}
					names[col] = true
				}
			}
			if r.Kind != "reference" && (r.ReferenceTable != "" || len(r.ReferenceColumns) > 0) {
				return false
			}
			if r.Kind != "range" && (r.Minimum != "" || r.Maximum != "") {
				return false
			}
			switch r.Kind {
			case "required", "unique":
			case "range":
				if len(r.Columns) != 1 || (r.Minimum == "" && r.Maximum == "") {
					return false
				}
				var low, high *big.Rat
				for i, s := range []string{r.Minimum, r.Maximum} {
					if s == "" {
						continue
					}
					if len(s) > api.DataPipelineQualityBoundBytesMax || !decimalBound.MatchString(s) {
						return false
					}
					n, ok := new(big.Rat).SetString(s)
					if !ok {
						return false
					}
					if i == 0 {
						low = n
					} else {
						high = n
					}
				}
				if low != nil && high != nil && low.Cmp(high) > 0 {
					return false
				}
			case "reference":
				if !slices.Contains(tables, r.ReferenceTable) || len(r.Columns) != len(r.ReferenceColumns) {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

// Operations changes bounded maintenance intent, never the serving selection.
func (s *Service) Operations(ctx context.Context, account, id string, r api.DataPipelineOperationsRequest) error {
	if err := s.admit(ctx, account); err != nil {
		return err
	}
	if !validID(account) || !validID(id) || r.ExpectedRevision < 1 {
		return ErrInvalid
	}
	store, ok := s.Store.(interface {
		Operations(context.Context, string, string, api.DataPipelineOperationsRequest) error
	})
	if !ok {
		return ErrUnavailable
	}
	return store.Operations(ctx, account, id, r)
}
func (s *PostgresStore) Operations(ctx context.Context, account, id string, r api.DataPipelineOperationsRequest) error {
	p, err := s.Get(ctx, account, id)
	if err != nil {
		return err
	}
	if p.Revision != r.ExpectedRevision || p.DesiredState == "deleted" || p.ServingInvalid {
		return ErrConflict
	}
	c, status := p.Operations, operationsStatus(p)
	switch r.Action {
	case "configure":
		c = normalizedOperations(r.Config)
		if c == nil || !validOperations(c, p.Tables) {
			return ErrInvalid
		}
		if (c.Snapshot != nil) != snapshotMode(p) {
			return ErrConflict
		}
		status.Quality = nil
		if status.Snapshot != nil {
			status.Snapshot.State = "due"
			status.Snapshot.RunID = ""
			status.Snapshot.NextRunAt = ""
		}
	case "run_snapshot":
		if r.Config != nil || !snapshotMode(p) {
			return ErrInvalid
		}
		if status.Snapshot == nil {
			status.Snapshot = &api.DataPipelineSnapshotStatus{}
		}
		if status.Snapshot.State == "running" {
			return ErrConflict
		}
		status.Snapshot.State = "due"
		status.Snapshot.RunID = ""
		status.Snapshot.NextRunAt = ""
	case "run_quality":
		if r.Config != nil || c == nil || c.Quality == nil {
			return ErrInvalid
		}
		status.Quality = nil
	default:
		return ErrInvalid
	}
	if status.Snapshot != nil && status.Snapshot.State == "due" {
		scheduled := p
		scheduled.Operations = c
		status.Snapshot.NextRunAt = nextWorkTime(scheduled, time.Now().UTC()).Format(time.RFC3339Nano)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if r.Action == "configure" && c.Quality != nil {
		check, err := s.q.PipelineCheckForCreate(ctx, tx, sqlc.PipelineCheckForCreateParams{AccountID: account, ConnectionID: p.SourceConnectionID})
		if err != nil {
			return ErrCheckRequired
		}
		v, err := describeCheck(sqlc.PipelineCheckGetRow(check))
		if err != nil {
			return err
		}
		if err = validateQualityMetadata(p, c, v.DataConnectionCheck); err != nil {
			return err
		}
	}
	n, err := s.q.PipelineOperationsSave(ctx, tx, sqlc.PipelineOperationsSaveParams{AccountID: account, ID: id, Revision: r.ExpectedRevision, Config: operationsJSON(c), Status: operationsJSON(status)})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return storeError(tx.Commit(ctx))
}

func validateQualityMetadata(p Pipeline, c *api.DataPipelineOperations, check api.DataConnectionCheck) error {
	if (check.Mode == "snapshot") != snapshotMode(p) {
		return ErrCheckRequired
	}
	if err := ValidateServingSelection(check, p.Tables, p.Serving); err != nil {
		return err
	}
	if c == nil || c.Quality == nil {
		return nil
	}
	relations := []relation{}
	for _, t := range check.Tables {
		if !slices.Contains(p.Tables, t.Name) {
			continue
		}
		t, err := projectTable(t, p.Serving)
		if err != nil {
			return err
		}
		r := relation{Name: t.Name}
		for _, col := range t.Columns {
			r.Columns = append(r.Columns, column{Name: col.Name, Type: col.Type, Key: col.PrimaryKey})
		}
		relations = append(relations, r)
	}
	for _, r := range c.Quality.Rules {
		if _, _, err := qualityQuery(p, r, relations); err != nil {
			return err
		}
	}
	return nil
}

func workAllowed(p Pipeline, now time.Time) bool {
	if p.Operations == nil || p.Operations.Workload == nil || p.Operations.Workload.Window == nil {
		return true
	}
	w := p.Operations.Workload.Window
	now = now.UTC()
	minute := now.Hour()*60 + now.Minute()
	if w.StartMinuteUTC < w.EndMinuteUTC {
		return minute >= w.StartMinuteUTC && minute < w.EndMinuteUTC
	}
	return minute >= w.StartMinuteUTC || minute < w.EndMinuteUTC
}
func workBatches(p Pipeline) int {
	if p.Operations != nil && p.Operations.Workload != nil && p.Operations.Workload.BatchesPerTick > 0 {
		return p.Operations.Workload.BatchesPerTick
	}
	return api.DataPipelineVerificationChunksPerTick
}

type workContextKey struct{}

func workloadContext(ctx context.Context, p Pipeline) context.Context {
	budget := api.DataPipelineVerificationQueryTimeout.Milliseconds()
	if p.Operations != nil && p.Operations.Workload != nil && p.Operations.Workload.QueryTimeoutMillis > 0 {
		budget = p.Operations.Workload.QueryTimeoutMillis
	}
	return context.WithValue(ctx, workContextKey{}, budget)
}
func workQueryMillis(ctx context.Context) int64 {
	if n, ok := ctx.Value(workContextKey{}).(int64); ok {
		return n
	}
	return api.DataPipelineVerificationQueryTimeout.Milliseconds()
}
func runDue(at string, now time.Time) bool {
	t, err := time.Parse(time.RFC3339Nano, at)
	return err != nil || !t.After(now)
}

func nextWorkTime(p Pipeline, at time.Time) time.Time {
	at = at.UTC()
	if workAllowed(p, at) {
		return at
	}
	w := p.Operations.Workload.Window
	start := time.Date(at.Year(), at.Month(), at.Day(), w.StartMinuteUTC/60, w.StartMinuteUTC%60, 0, 0, time.UTC)
	if !start.After(at) {
		start = start.AddDate(0, 0, 1)
	}
	return start
}

func snapshotStale(p api.DataPipeline, now time.Time) bool {
	if p.Operations == nil || p.Operations.Snapshot == nil || p.OperationsStatus == nil || p.OperationsStatus.Snapshot == nil {
		return false
	}
	r := p.OperationsStatus.Snapshot
	if r.CapturedAt == "" {
		return false
	}
	if r.State == "failed" || r.Reason != "" {
		return true
	}
	due, err := time.Parse(time.RFC3339Nano, r.NextRunAt)
	return err == nil && now.After(due.Add(api.DataPipelineSnapshotOperationTimeout+api.DataPipelinePollInterval))
}
