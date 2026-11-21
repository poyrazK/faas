package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

type IndexChange struct {
	api.DataPipelineIndexAdvice
	RequestID         string
	Generation        int64
	Filters           []string
	OrderBy           []api.DataPipelineIndexOrder
	TableOID          uint32
	SchemaChangeID    string
	ColumnsSignature  string
	StorageLimitBytes int64
}

func indexAdviceID(p Pipeline, c IndexChange) string {
	c.ID, c.State, c.CanApply = "", "", false
	b, _ := json.Marshal(struct {
		Account, Pipeline string
		Change            IndexChange
	}{p.AccountID, p.ID, c})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func indexShape(p Pipeline, r api.DataPipelineIndexRequest) ([]api.DataPipelineIndexKey, error) {
	if len(r.Filters) > api.DataPipelineIndexColumnsMax || len(r.OrderBy) > api.DataPipelineIndexColumnsMax || !slices.Contains(p.Tables, r.Table) || !identifier.MatchString(r.Table) || r.MaxIndexBytes < 0 || r.MaxIndexBytes > api.DataPipelineIndexBuildMaxBytes {
		return nil, ErrInvalid
	}
	var keys []api.DataPipelineIndexKey
	seen := map[string]bool{}
	if a := tenantAccess(p); a != nil {
		keys = append(keys, api.DataPipelineIndexKey{Column: a.Column, TenantExpression: true})
		seen[a.Column] = true
	}
	filters := slices.Clone(r.Filters)
	slices.Sort(filters)
	for _, name := range filters {
		if !validServingColumn(name) {
			return nil, ErrInvalid
		}
		if seen[name] {
			if a := tenantAccess(p); a != nil && name == a.Column {
				continue
			}
			return nil, ErrInvalid
		}
		seen[name] = true
		keys = append(keys, api.DataPipelineIndexKey{Column: name})
	}
	for _, order := range r.OrderBy {
		if !validServingColumn(order.Column) || seen[order.Column] {
			return nil, ErrInvalid
		}
		seen[order.Column] = true
		keys = append(keys, api.DataPipelineIndexKey{Column: order.Column, Descending: order.Descending})
	}
	if len(keys) == 0 || len(keys) > api.DataPipelineIndexColumnsMax {
		return nil, ErrInvalid
	}
	return keys, nil
}

func stableIndexState(p Pipeline) bool {
	return !p.IndexWorkBlocked && p.Initialized && !p.ServingInvalid && (p.State == "live" && p.DesiredState == "running" || p.State == "paused" && p.DesiredState == "paused") && !p.VerifyRequested && !tableWorkflow(p) &&
		(p.SchemaChange == nil || p.SchemaChange.State == "complete" || p.SchemaChange.State == "withdrawn") && (p.ReaderChange == nil || p.ReaderChange.State != "approved")
}

func recentIndexBaseline(p Pipeline, c *IndexChange, now time.Time) bool {
	at, err := time.Parse(time.RFC3339Nano, p.ObservedAt)
	return c != nil && c.Generation == p.Generation && c.PreviewRevision == p.Revision && p.ObservedRevision >= c.PreviewRevision-1 && err == nil && !at.After(now) && now.Sub(at) < api.DataPipelineObservationMaxAge && stableIndexState(p)
}

func (s *Service) Indexes(ctx context.Context, account, id string, r api.DataPipelineIndexRequest) error {
	if s == nil || s.Store == nil || s.Allowed == nil || !s.Allowed(ctx, account) {
		return ErrUnavailable
	}
	if !validID(account) || !validID(id) || r.ExpectedRevision < 1 {
		return ErrInvalid
	}
	if r.Action != "discard" {
		if err := s.admit(ctx, account); err != nil {
			return err
		}
	}
	store, ok := s.Store.(interface {
		Indexes(context.Context, string, string, api.DataPipelineIndexRequest) error
	})
	if !ok {
		return ErrUnavailable
	}
	return store.Indexes(ctx, account, id, r)
}

func (s *PostgresStore) Indexes(ctx context.Context, account, id string, r api.DataPipelineIndexRequest) error {
	p, err := s.Get(ctx, account, id)
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if p.Revision != r.ExpectedRevision {
		return ErrConflict
	}
	c := p.IndexChange
	switch r.Action {
	case "preview":
		if !stableIndexState(p) || !p.ObservationFresh || c != nil && (c.State == "requested" || c.State == "approved") {
			return ErrConflict
		}
		keys, err := indexShape(p, r)
		if err != nil {
			return err
		}
		budget := r.MaxIndexBytes
		if budget == 0 {
			budget = api.DataPipelineIndexBuildMaxBytes
		}
		c = &IndexChange{DataPipelineIndexAdvice: api.DataPipelineIndexAdvice{State: "requested", PreviewRevision: p.Revision + 1, Table: r.Table, Keys: keys, StorageBudgetBytes: budget, Message: "The worker is inspecting the owned mirror. No index has been created."}, RequestID: uuid.NewString(), Generation: p.Generation, SchemaChangeID: indexSchemaChangeID(p), Filters: slices.Clone(r.Filters), OrderBy: slices.Clone(r.OrderBy)}
	case "approve":
		if c == nil || c.State != "preview" || !c.CanApply || r.AdviceID != c.ID || c.ID != indexAdviceID(p, *c) || !recentIndexBaseline(p, c, time.Now()) {
			return ErrConflict
		}
		if r.Table != "" || len(r.Filters) > 0 || len(r.OrderBy) > 0 || r.MaxIndexBytes != 0 {
			return ErrInvalid
		}
		copy := *c
		copy.State = "approved"
		copy.CanApply = false
		c = &copy
	case "discard":
		if c == nil || c.State == "approved" {
			return ErrConflict
		}
		c = nil
	default:
		return ErrInvalid
	}
	raw := []byte("{}")
	if c != nil {
		raw, _ = json.Marshal(c)
	}
	n, err := s.q.PipelineIndexIntent(ctx, s.pool, sqlc.PipelineIndexIntentParams{AccountID: account, ID: id, Revision: r.ExpectedRevision, IndexChange: raw})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PostgresStore) finishIndex(ctx context.Context, p Pipeline, c *IndexChange) error {
	if c == nil {
		return ErrConflict
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return ErrInvalid
	}
	n, err := s.q.PipelineFinishIndex(ctx, s.pool, sqlc.PipelineFinishIndexParams{AccountID: p.AccountID, ID: p.ID, Revision: p.Revision, LeaseToken: p.LeaseToken, RequestID: c.RequestID, IndexChange: raw})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func indexSchemaChangeID(p Pipeline) string {
	if p.SchemaChange != nil {
		return p.SchemaChange.ID
	}
	return ""
}
func indexAdviceView(p Pipeline, now time.Time) *api.DataPipelineIndexAdvice {
	c := p.IndexChange
	if c == nil {
		return nil
	}
	view := c.DataPipelineIndexAdvice
	replaced := c.Generation != p.Generation || c.State == "applied" && c.SchemaChangeID != indexSchemaChangeID(p) && slices.Contains(CopiedTables(p.SchemaChange), c.Table)
	if replaced {
		view.State, view.CanApply, view.Reason, view.Message = "blocked", false, "index_mirror_replaced", "Mirror replacement invalidated this index recommendation. Request new advice after recovery completes."
	} else if view.State == "preview" && !recentIndexBaseline(p, c, now) {
		view.CanApply, view.Reason = false, "index_preview_stale"
	}
	return &view
}
