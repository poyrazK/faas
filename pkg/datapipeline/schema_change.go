package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
)

// SchemaChange is metadata only. Approval binds the exact generation, baseline
// and source definition; it never grants permission for arbitrary DDL.
type SchemaChange struct {
	SelectionID                      string   `json:",omitempty"`
	AddedTables, RequestedTables     []string `json:",omitempty"`
	ID, BeforeHash, AfterHash, State string
	Supported                        bool
	Before, After                    []relation
	Changes                          []SchemaColumnChange
	Refreshes                        []SchemaRefresh
	Retry                            int
	Recovery                         *TableRecovery      `json:",omitempty"`
	Copies                           []TableCopyProgress `json:",omitempty"`
	Cancellation                     *TableCancellation  `json:",omitempty"`
}
type SchemaColumnChange struct {
	Table, Column, Kind, BeforeType, AfterType, Reason string
	Safe                                               bool
}
type SchemaRefresh struct{ AppID, Slug, WakeID, Status string }

// Keep the original structural hash stable when upgrading existing receipts.
// New baselines also retain relation identity, ordinals and default metadata.
func relationHash(relations []relation) string {
	legacy := make([]relation, len(relations))
	for i, r := range relations {
		legacy[i] = relation{Name: r.Name, Columns: slices.Clone(r.Columns)}
		for j := range legacy[i].Columns {
			c := &legacy[i].Columns[j]
			c.Ordinal = 0
			c.Default = false
			c.Missing = false
			c.Identity = false
			c.Unsupported = false
		}
	}
	b, _ := json.Marshal(legacy)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func schemaPlan(p Pipeline, before, after []relation) SchemaChange {
	c := SchemaChange{State: "pending", Supported: true, Before: before, After: after, BeforeHash: relationHash(before), AfterHash: relationHash(after), Changes: []SchemaColumnChange{}}
	add := func(table string, old, next *column, kind, reason string, safe bool) {
		v := SchemaColumnChange{Table: table, Kind: kind, Reason: reason, Safe: safe}
		if old != nil {
			v.Column = old.Name
			v.BeforeType = old.Type
		}
		if next != nil {
			v.Column = next.Name
			v.AfterType = next.Type
		}
		c.Changes = append(c.Changes, v)
		c.Supported = c.Supported && safe
	}
	if len(before) == 0 || len(before) != len(after) {
		add("", nil, nil, "baseline_unavailable", "A recorded baseline is required; use an explicit resync.", false)
	}
	for i, old := range before {
		if i >= len(after) {
			break
		}
		next := after[i]
		if old.Name != next.Name || old.OID != next.OID {
			add(old.Name, nil, nil, "table_changed", "The source table identity changed; use an explicit resync.", false)
			continue
		}
		if next.Unsupported {
			add(old.Name, nil, nil, "table_unsupported", "The table no longer satisfies source replication requirements.", false)
		}
		byName := map[string]column{}
		for _, col := range next.Columns {
			byName[col.Name] = col
		}
		previous := map[string]bool{}
		for _, col := range old.Columns {
			previous[col.Name] = true
			found, ok := byName[col.Name]
			if !ok {
				copy := col
				add(old.Name, &copy, nil, "removed", "Removing or renaming a column requires an explicit resync.", false)
			} else if !reflect.DeepEqual(col, found) {
				copy := col
				add(old.Name, &copy, &found, "changed", changedColumnReason(col, found), false)
			}
		}
		for _, col := range next.Columns {
			if previous[col.Name] {
				continue
			}
			copy := col
			safe := !col.Unsupported && !col.Key && !col.Identity && (col.Modifier == -1 || col.Type == "text" || len(col.Type) >= 9 && col.Type[:9] == "character")
			reason := "Backfill actual source values into this new column, then verify all values. Source defaults are not executed on the mirror."
			if col.NotNull {
				reason += " Enforce NOT NULL before refreshing the API."
			}
			if !safe {
				reason = "Only supported columns without identity or key changes can be added; some type modifiers require resync."
			}
			add(old.Name, nil, &copy, "added", reason, safe)
		}
	}
	b, _ := json.Marshal(struct {
		Pipeline      string
		Generation    int64
		Before, After []relation
	}{p.ID, p.Generation, before, after})
	sum := sha256.Sum256(b)
	c.ID = hex.EncodeToString(sum[:])
	if !c.Supported {
		c.State = "unsupported"
	}
	return c
}

func changedColumnReason(before, after column) string {
	changes := []string{}
	if before.Type != after.Type || before.Modifier != after.Modifier {
		changes = append(changes, "Type changed")
	}
	if before.NotNull != after.NotNull {
		if after.NotNull {
			changes = append(changes, "Column became required")
		} else {
			changes = append(changes, "Column became nullable")
		}
	}
	if before.Key != after.Key {
		changes = append(changes, "Primary key membership changed")
	}
	if before.Default != after.Default || before.Missing != after.Missing || before.Identity != after.Identity {
		changes = append(changes, "Default, stored missing-value or identity metadata changed")
	}
	if before.Ordinal != after.Ordinal {
		changes = append(changes, "Column identity or position changed")
	}
	if before.Unsupported != after.Unsupported {
		changes = append(changes, "Column compatibility changed")
	}
	return strings.Join(changes, "; ") + ". Use an explicit resync."
}

func (s *Service) ApproveSchema(ctx context.Context, account, id string, revision int64, change string, retry bool) error {
	if err := s.admit(ctx, account); err != nil {
		return err
	}
	store, ok := s.Store.(interface {
		ApproveSchema(context.Context, string, string, int64, string, bool) error
	})
	if !ok {
		return ErrUnavailable
	}
	if revision < 1 || len(change) != 64 {
		return ErrInvalid
	}
	if _, err := hex.DecodeString(change); err != nil {
		return ErrInvalid
	}
	p, err := s.Store.Get(ctx, account, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) && validID(account) && validID(id) {
			return ErrConflict
		}
		return err
	}
	if activeTableCancellation(p) {
		return ErrConflict
	}
	if tableWorkflow(p) {
		if err = s.tableCapacity(ctx, account, id); err != nil {
			return err
		}
	}
	return store.ApproveSchema(ctx, account, id, revision, change, retry)
}
