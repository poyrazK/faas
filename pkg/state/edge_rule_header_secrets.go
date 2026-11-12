package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

// EdgeRuleHeaderSecret is one sealed edge-rule request header value
// (ADR-20261108110000). apid seals it to the edge seal domain and never reads
// it back; gatewayd-internal opens it when it compiles the rule. A row never
// changes after insert and is deleted with its app.
type EdgeRuleHeaderSecret struct {
	ID         string
	AccountID  string
	AppID      string
	Ciphertext []byte
	CreatedAt  time.Time
}

// EdgeRuleHeaderSecretStore is the ADR-20261108110000 capability; apid and
// gatewayd-internal type-assert it.
type EdgeRuleHeaderSecretStore interface {
	// CreateEdgeRuleHeaderSecret stores a sealed value for appID and returns
	// the row with its new id.
	CreateEdgeRuleHeaderSecret(ctx context.Context, accountID, appID string, ciphertext []byte) (EdgeRuleHeaderSecret, error)
	// EdgeRuleHeaderSecretsByID returns the rows of ids that exist and
	// belong to appID; ids of other apps are left out.
	EdgeRuleHeaderSecretsByID(ctx context.Context, appID string, ids []string) ([]EdgeRuleHeaderSecret, error)
}

var (
	_ EdgeRuleHeaderSecretStore = (*PgStore)(nil)
	_ EdgeRuleHeaderSecretStore = (*MemStore)(nil)
)

// EdgeSealedCredentialHeaderSecret is the edge re-seal kind of an
// edge_rule_header_secrets row.
const EdgeSealedCredentialHeaderSecret = "edge_rule_header"

func (s *PgStore) CreateEdgeRuleHeaderSecret(ctx context.Context, accountID, appID string, ciphertext []byte) (EdgeRuleHeaderSecret, error) {
	if len(ciphertext) == 0 {
		return EdgeRuleHeaderSecret{}, errors.New("state: edge rule header secret needs a ciphertext")
	}
	out := EdgeRuleHeaderSecret{AccountID: accountID, AppID: appID, Ciphertext: ciphertext}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO edge_rule_header_secrets (account_id, app_id, ciphertext)
		VALUES ($1::uuid, $2::uuid, $3)
		RETURNING id::text, created_at`, accountID, appID, ciphertext).Scan(&out.ID, &out.CreatedAt)
	if err != nil {
		return EdgeRuleHeaderSecret{}, fmt.Errorf("state: create edge rule header secret: %w", err)
	}
	return out, nil
}

func (s *PgStore) EdgeRuleHeaderSecretsByID(ctx context.Context, appID string, ids []string) ([]EdgeRuleHeaderSecret, error) {
	ids = validEdgeRuleHeaderSecretIDs(ids)
	if len(ids) == 0 || uuid.Validate(appID) != nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, account_id::text, app_id::text, ciphertext, created_at
		  FROM edge_rule_header_secrets
		 WHERE app_id = $1::uuid AND id = ANY($2::uuid[])
		 ORDER BY id`, appID, ids)
	if err != nil {
		return nil, fmt.Errorf("state: edge rule header secrets: %w", err)
	}
	defer rows.Close()
	var out []EdgeRuleHeaderSecret
	for rows.Next() {
		var row EdgeRuleHeaderSecret
		if err := rows.Scan(&row.ID, &row.AccountID, &row.AppID, &row.Ciphertext, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("state: scan edge rule header secret: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: edge rule header secrets: %w", err)
	}
	return out, nil
}

// validEdgeRuleHeaderSecretIDs keeps the well-formed ids, each once: a
// malformed id names no row, and would fail the uuid[] cast for the rest.
func validEdgeRuleHeaderSecretIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if uuid.Validate(id) == nil && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func (m *MemStore) CreateEdgeRuleHeaderSecret(_ context.Context, accountID, appID string, ciphertext []byte) (EdgeRuleHeaderSecret, error) {
	if len(ciphertext) == 0 {
		return EdgeRuleHeaderSecret{}, errors.New("state: edge rule header secret needs a ciphertext")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.edgeRuleHeaderSecrets == nil {
		m.edgeRuleHeaderSecrets = map[string]EdgeRuleHeaderSecret{}
	}
	row := EdgeRuleHeaderSecret{
		ID: uuid.NewString(), AccountID: accountID, AppID: appID,
		Ciphertext: slices.Clone(ciphertext), CreatedAt: time.Now().UTC(),
	}
	m.edgeRuleHeaderSecrets[row.ID] = row
	return row, nil
}

func (m *MemStore) EdgeRuleHeaderSecretsByID(_ context.Context, appID string, ids []string) ([]EdgeRuleHeaderSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRuleHeaderSecret
	for _, id := range validEdgeRuleHeaderSecretIDs(ids) {
		if row, ok := m.edgeRuleHeaderSecrets[id]; ok && row.AppID == appID {
			row.Ciphertext = slices.Clone(row.Ciphertext)
			out = append(out, row)
		}
	}
	slices.SortFunc(out, func(a, b EdgeRuleHeaderSecret) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
	return out, nil
}
