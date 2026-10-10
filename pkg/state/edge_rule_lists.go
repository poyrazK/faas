package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-963 reusable edge-rule lists.

// EdgeRuleList is one account-scoped list. Items are stored canonicalized
// (api.NormalizeEdgeRuleListItems) by apid.
type EdgeRuleList struct {
	ID          string
	AccountID   string
	Name        string
	Kind        string
	Description string
	Items       []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// EdgeRuleListRef is a rule whose match condition references a list.
type EdgeRuleListRef struct {
	RuleID string
	AppID  string
}

// CreateEdgeRuleListParams is the insert shape.
type CreateEdgeRuleListParams struct {
	AccountID   string
	Name        string
	Kind        string
	Description string
	Items       []string
}

// UpdateEdgeRuleListParams changes the non-nil fields.
type UpdateEdgeRuleListParams struct {
	Description *string
	Items       *[]string
}

// ErrEdgeRuleListQuota is returned when an account is at its list cap.
var ErrEdgeRuleListQuota = errors.New("state: edge rule list quota exceeded")

// EdgeRuleListInUseError is returned when deleting a list that rules
// reference.
type EdgeRuleListInUseError struct {
	Name string
	Refs []EdgeRuleListRef
}

func (e *EdgeRuleListInUseError) Error() string {
	return fmt.Sprintf("state: edge rule list %q is referenced by %d rule(s)", e.Name, len(e.Refs))
}

// EdgeRuleListStore is the ADR-963 capability; apid and gatewayd
// type-assert it.
type EdgeRuleListStore interface {
	// ListEdgeRuleLists returns the account's lists, by name.
	ListEdgeRuleLists(ctx context.Context, accountID string) ([]EdgeRuleList, error)
	// EdgeRuleListsByName returns the named lists that exist, by name.
	EdgeRuleListsByName(ctx context.Context, accountID string, names []string) ([]EdgeRuleList, error)
	// EdgeRuleListReferences maps each list name to the rules referencing it.
	EdgeRuleListReferences(ctx context.Context, accountID string) (map[string][]EdgeRuleListRef, error)
	// CreateEdgeRuleList inserts a list unless the account already holds
	// maxLists (ErrEdgeRuleListQuota) or the name is taken (ErrConflict).
	CreateEdgeRuleList(ctx context.Context, p CreateEdgeRuleListParams, maxLists int) (EdgeRuleList, error)
	// UpdateEdgeRuleList changes a list and, in the same transaction,
	// touches every rule referencing it so gateways recompile those rules.
	UpdateEdgeRuleList(ctx context.Context, accountID, name string, p UpdateEdgeRuleListParams) (EdgeRuleList, []EdgeRuleListRef, error)
	// DeleteEdgeRuleList removes an unreferenced list
	// (*EdgeRuleListInUseError otherwise).
	DeleteEdgeRuleList(ctx context.Context, accountID, name string) error
}

var (
	_ EdgeRuleListStore = (*PgStore)(nil)
	_ EdgeRuleListStore = (*MemStore)(nil)
)

const edgeRuleListCols = `id::text, account_id::text, name, kind, description, items, created_at, updated_at`

func scanEdgeRuleList(row pgx.Row) (EdgeRuleList, error) {
	var l EdgeRuleList
	err := row.Scan(&l.ID, &l.AccountID, &l.Name, &l.Kind, &l.Description, &l.Items, &l.CreatedAt, &l.UpdatedAt)
	return l, err
}

func collectEdgeRuleLists(rows pgx.Rows) ([]EdgeRuleList, error) {
	defer rows.Close()
	var out []EdgeRuleList
	for rows.Next() {
		l, err := scanEdgeRuleList(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *PgStore) ListEdgeRuleLists(ctx context.Context, accountID string) ([]EdgeRuleList, error) {
	rows, err := s.pool.Query(ctx, `select `+edgeRuleListCols+` from edge_rule_lists where account_id = $1 order by name`, accountID)
	if err != nil {
		return nil, fmt.Errorf("state: list edge rule lists: %w", err)
	}
	out, err := collectEdgeRuleLists(rows)
	if err != nil {
		return nil, fmt.Errorf("state: scan edge rule lists: %w", err)
	}
	return out, nil
}

func (s *PgStore) EdgeRuleListsByName(ctx context.Context, accountID string, names []string) ([]EdgeRuleList, error) {
	if len(names) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `select `+edgeRuleListCols+` from edge_rule_lists where account_id = $1 and name = any($2) order by name`, accountID, names)
	if err != nil {
		return nil, fmt.Errorf("state: edge rule lists by name: %w", err)
	}
	out, err := collectEdgeRuleLists(rows)
	if err != nil {
		return nil, fmt.Errorf("state: scan edge rule lists: %w", err)
	}
	return out, nil
}

// pgQuerier is the read surface shared by the pool and a transaction.
type pgQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// edgeRuleListRefsPG walks every conditioned rule in the account. Conditions
// are bounded (ADR-962) and per-account rule counts are plan-capped, so the
// walk is cheaper than a jsonpath scan and shares the reference rule with
// MemStore.
func edgeRuleListRefsPG(ctx context.Context, q pgQuerier, accountID string) (map[string][]EdgeRuleListRef, error) {
	rows, err := q.Query(ctx, `select id::text, app_id::text, match_expr from edge_rules where account_id = $1 and match_expr is not null order by id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("state: edge rule list references: %w", err)
	}
	defer rows.Close()
	out := map[string][]EdgeRuleListRef{}
	for rows.Next() {
		var ref EdgeRuleListRef
		var raw []byte
		if err := rows.Scan(&ref.RuleID, &ref.AppID, &raw); err != nil {
			return nil, fmt.Errorf("state: scan edge rule list reference: %w", err)
		}
		var expr api.EdgeRuleMatchExpr
		if json.Unmarshal(raw, &expr) != nil {
			continue
		}
		for _, name := range api.EdgeRuleMatchListRefs(&expr) {
			out[name] = append(out[name], ref)
		}
	}
	return out, rows.Err()
}

func (s *PgStore) EdgeRuleListReferences(ctx context.Context, accountID string) (map[string][]EdgeRuleListRef, error) {
	return edgeRuleListRefsPG(ctx, s.pool, accountID)
}

func (s *PgStore) CreateEdgeRuleList(ctx context.Context, p CreateEdgeRuleListParams, maxLists int) (EdgeRuleList, error) {
	var out EdgeRuleList
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('edge_rule_lists:' || $1::text))`, p.AccountID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from edge_rule_lists where account_id = $1`, p.AccountID).Scan(&n); err != nil {
			return err
		}
		if n >= maxLists {
			return ErrEdgeRuleListQuota
		}
		l, err := scanEdgeRuleList(tx.QueryRow(ctx, `
			insert into edge_rule_lists (account_id, name, kind, description, items)
			values ($1, $2, $3, $4, $5)
			returning `+edgeRuleListCols, p.AccountID, p.Name, p.Kind, p.Description, nonNilStrings(p.Items)))
		if isUniqueViolation(err) {
			return ErrConflict
		}
		out = l
		return err
	})
	if err != nil {
		if errors.Is(err, ErrEdgeRuleListQuota) || errors.Is(err, ErrConflict) {
			return EdgeRuleList{}, err
		}
		return EdgeRuleList{}, fmt.Errorf("state: create edge rule list: %w", err)
	}
	return out, nil
}

func (s *PgStore) UpdateEdgeRuleList(ctx context.Context, accountID, name string, p UpdateEdgeRuleListParams) (EdgeRuleList, []EdgeRuleListRef, error) {
	var (
		out  EdgeRuleList
		refs []EdgeRuleListRef
	)
	var itemsArg any
	if p.Items != nil {
		itemsArg = nonNilStrings(*p.Items)
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := scanEdgeRuleList(tx.QueryRow(ctx, `
			update edge_rule_lists set
				description = coalesce($3, description),
				items       = coalesce($4, items),
				updated_at  = now()
			where account_id = $1 and name = $2
			returning `+edgeRuleListCols, accountID, name, p.Description, itemsArg))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out = l
		all, err := edgeRuleListRefsPG(ctx, tx, accountID)
		if err != nil {
			return err
		}
		refs = all[name]
		if len(refs) == 0 {
			return nil
		}
		ids := make([]string, len(refs))
		for i, r := range refs {
			ids[i] = r.RuleID
		}
		// The change-log trigger turns this touch into a scoped gateway
		// invalidation; the rule-set snapshot excludes updated_at, so no
		// rule-set version is recorded (ADR-963 §5).
		_, err = tx.Exec(ctx, `update edge_rules set updated_at = now() where id = any($1::uuid[])`, ids)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return EdgeRuleList{}, nil, err
		}
		return EdgeRuleList{}, nil, fmt.Errorf("state: update edge rule list: %w", err)
	}
	return out, refs, nil
}

func (s *PgStore) DeleteEdgeRuleList(ctx context.Context, accountID, name string) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `select id::text from edge_rule_lists where account_id = $1 and name = $2 for update`, accountID, name).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		all, err := edgeRuleListRefsPG(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if refs := all[name]; len(refs) > 0 {
			return &EdgeRuleListInUseError{Name: name, Refs: refs}
		}
		_, err = tx.Exec(ctx, `delete from edge_rule_lists where id = $1`, id)
		return err
	})
	var inUse *EdgeRuleListInUseError
	if err == nil || errors.Is(err, ErrNotFound) || errors.As(err, &inUse) {
		return err
	}
	return fmt.Errorf("state: delete edge rule list: %w", err)
}

// --- MemStore ---

func (m *MemStore) memEdgeRuleListRefs(accountID string) map[string][]EdgeRuleListRef {
	out := map[string][]EdgeRuleListRef{}
	for _, r := range m.edgeRules {
		if r.AccountID != accountID || r.Match == nil {
			continue
		}
		for _, name := range api.EdgeRuleMatchListRefs(r.Match) {
			out[name] = append(out[name], EdgeRuleListRef{RuleID: r.ID, AppID: r.AppID})
		}
	}
	for name := range out {
		sort.Slice(out[name], func(i, j int) bool { return out[name][i].RuleID < out[name][j].RuleID })
	}
	return out
}

func (m *MemStore) memEdgeRuleList(accountID, name string) (string, bool) {
	for id, l := range m.edgeRuleLists {
		if l.AccountID == accountID && l.Name == name {
			return id, true
		}
	}
	return "", false
}

func cloneEdgeRuleList(l EdgeRuleList) EdgeRuleList {
	l.Items = append([]string{}, l.Items...)
	return l
}

func (m *MemStore) ListEdgeRuleLists(_ context.Context, accountID string) ([]EdgeRuleList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRuleList
	for _, l := range m.edgeRuleLists {
		if l.AccountID == accountID {
			out = append(out, cloneEdgeRuleList(l))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemStore) EdgeRuleListsByName(ctx context.Context, accountID string, names []string) ([]EdgeRuleList, error) {
	all, _ := m.ListEdgeRuleLists(ctx, accountID)
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []EdgeRuleList
	for _, l := range all {
		if want[l.Name] {
			out = append(out, l)
		}
	}
	return out, nil
}

func (m *MemStore) EdgeRuleListReferences(_ context.Context, accountID string) (map[string][]EdgeRuleListRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.memEdgeRuleListRefs(accountID), nil
}

func (m *MemStore) CreateEdgeRuleList(_ context.Context, p CreateEdgeRuleListParams, maxLists int) (EdgeRuleList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, l := range m.edgeRuleLists {
		if l.AccountID == p.AccountID {
			n++
		}
	}
	if n >= maxLists {
		return EdgeRuleList{}, ErrEdgeRuleListQuota
	}
	if _, taken := m.memEdgeRuleList(p.AccountID, p.Name); taken {
		return EdgeRuleList{}, ErrConflict
	}
	if m.edgeRuleLists == nil {
		m.edgeRuleLists = map[string]EdgeRuleList{}
	}
	now := time.Now().UTC()
	l := EdgeRuleList{
		ID: uuid.NewString(), AccountID: p.AccountID, Name: p.Name, Kind: p.Kind,
		Description: p.Description, Items: append([]string{}, p.Items...),
		CreatedAt: now, UpdatedAt: now,
	}
	m.edgeRuleLists[l.ID] = l
	return cloneEdgeRuleList(l), nil
}

func (m *MemStore) UpdateEdgeRuleList(_ context.Context, accountID, name string, p UpdateEdgeRuleListParams) (EdgeRuleList, []EdgeRuleListRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.memEdgeRuleList(accountID, name)
	if !ok {
		return EdgeRuleList{}, nil, ErrNotFound
	}
	l := m.edgeRuleLists[id]
	if p.Description != nil {
		l.Description = *p.Description
	}
	if p.Items != nil {
		l.Items = append([]string{}, (*p.Items)...)
	}
	now := time.Now().UTC()
	l.UpdatedAt = now
	m.edgeRuleLists[id] = l
	refs := m.memEdgeRuleListRefs(accountID)[name]
	for _, ref := range refs {
		r := m.edgeRules[ref.RuleID]
		r.UpdatedAt = now
		m.edgeRules[ref.RuleID] = r
	}
	return cloneEdgeRuleList(l), refs, nil
}

func (m *MemStore) DeleteEdgeRuleList(_ context.Context, accountID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.memEdgeRuleList(accountID, name)
	if !ok {
		return ErrNotFound
	}
	if refs := m.memEdgeRuleListRefs(accountID)[name]; len(refs) > 0 {
		return &EdgeRuleListInUseError{Name: name, Refs: refs}
	}
	delete(m.edgeRuleLists, id)
	return nil
}
