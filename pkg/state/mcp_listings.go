package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// MCPListing is a sold MCP server's public listing (ADR-20261126175800):
// the snapshot the seller published. A row exists only while published.
type MCPListing struct {
	AccountID    string
	AppID        string
	Title        string
	Summary      string
	Endpoint     string
	Tools        []MCPListingTool
	RegistryName string
	ShowUsage    bool
	Version      int
	PublishedAt  time.Time
	UpdatedAt    time.Time
}

// MCPListingTool is one tool of the published snapshot.
type MCPListingTool struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// MCPListingStore keeps listings. Optional: a store without it serves no
// listings.
type MCPListingStore interface {
	GetMCPListing(ctx context.Context, accountID, appID string) (MCPListing, error)
	// PutMCPListing publishes or republishes, bumping Version.
	PutMCPListing(ctx context.Context, l MCPListing) (MCPListing, error)
	// DeleteMCPListing unpublishes; ErrNotFound when nothing was published.
	DeleteMCPListing(ctx context.Context, accountID, appID string) error
	// CountOpenMCPListingCustomers counts the app's production consumers
	// that are not revoked, for the listing's usage badge.
	CountOpenMCPListingCustomers(ctx context.Context, accountID, appID string) (int64, error)
}

var (
	_ MCPListingStore = (*MemStore)(nil)
	_ MCPListingStore = (*PgStore)(nil)
)

func (m *MemStore) GetMCPListing(_ context.Context, accountID, appID string) (MCPListing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.mcpListings[appID]
	if !ok || l.AccountID != accountID {
		return MCPListing{}, ErrNotFound
	}
	l.Tools = append([]MCPListingTool{}, l.Tools...)
	return l, nil
}

func (m *MemStore) PutMCPListing(_ context.Context, l MCPListing) (MCPListing, error) {
	if l.AccountID == "" || l.AppID == "" {
		return MCPListing{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mcpListings == nil {
		m.mcpListings = map[string]MCPListing{}
	}
	prev, ok := m.mcpListings[l.AppID]
	if ok && prev.AccountID != l.AccountID {
		return MCPListing{}, ErrNotFound
	}
	now := time.Now().UTC()
	l.Version, l.PublishedAt, l.UpdatedAt = prev.Version+1, now, now
	l.Tools = append([]MCPListingTool{}, l.Tools...)
	m.mcpListings[l.AppID] = l
	return l, nil
}

func (m *MemStore) DeleteMCPListing(_ context.Context, accountID, appID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.mcpListings[appID]
	if !ok || l.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.mcpListings, appID)
	return nil
}

func (m *MemStore) CountOpenMCPListingCustomers(_ context.Context, accountID, appID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, c := range m.apiConsumers {
		if c.AccountID == accountID && c.AppID == appID && c.EnvironmentID == "" && c.RevokedAt == nil {
			n++
		}
	}
	return n, nil
}

func fromMCPListing(row sqlc.McpListing) (MCPListing, error) {
	l := MCPListing{AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), Title: row.Title, Summary: row.Summary,
		Endpoint: row.Endpoint, RegistryName: row.RegistryName, ShowUsage: row.ShowUsage, Version: int(row.Version),
		PublishedAt: row.PublishedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC()}
	if err := json.Unmarshal(row.Tools, &l.Tools); err != nil {
		return MCPListing{}, fmt.Errorf("decode MCP listing tools: %w", err)
	}
	if l.Tools == nil {
		l.Tools = []MCPListingTool{}
	}
	return l, nil
}

func (s *PgStore) GetMCPListing(ctx context.Context, accountID, appID string) (MCPListing, error) {
	row, err := sqlc.New().GetMCPListing(ctx, s.pool, sqlc.GetMCPListingParams{AccountID: uuidToPgtype(accountID), AppID: uuidToPgtype(appID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPListing{}, ErrNotFound
	}
	if err != nil {
		return MCPListing{}, fmt.Errorf("read MCP listing: %w", err)
	}
	return fromMCPListing(row)
}

func (s *PgStore) PutMCPListing(ctx context.Context, l MCPListing) (MCPListing, error) {
	if l.Tools == nil {
		l.Tools = []MCPListingTool{}
	}
	tools, err := json.Marshal(l.Tools)
	if err != nil {
		return MCPListing{}, fmt.Errorf("encode MCP listing tools: %w", err)
	}
	row, err := sqlc.New().PutMCPListing(ctx, s.pool, sqlc.PutMCPListingParams{AppID: uuidToPgtype(l.AppID), AccountID: uuidToPgtype(l.AccountID),
		Title: l.Title, Summary: l.Summary, Endpoint: l.Endpoint, Tools: tools, RegistryName: l.RegistryName, ShowUsage: l.ShowUsage})
	if errors.Is(err, pgx.ErrNoRows) {
		// The app's row belongs to another account.
		return MCPListing{}, ErrNotFound
	}
	if err != nil {
		return MCPListing{}, fmt.Errorf("publish MCP listing: %w", err)
	}
	return fromMCPListing(row)
}

func (s *PgStore) DeleteMCPListing(ctx context.Context, accountID, appID string) error {
	n, err := sqlc.New().DeleteMCPListing(ctx, s.pool, sqlc.DeleteMCPListingParams{AccountID: uuidToPgtype(accountID), AppID: uuidToPgtype(appID)})
	if err != nil {
		return fmt.Errorf("unpublish MCP listing: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) CountOpenMCPListingCustomers(ctx context.Context, accountID, appID string) (int64, error) {
	n, err := sqlc.New().CountOpenMCPListingCustomers(ctx, s.pool, sqlc.CountOpenMCPListingCustomersParams{
		AccountID: uuidToPgtype(accountID), AppID: uuidToPgtype(appID)})
	if err != nil {
		return 0, fmt.Errorf("count MCP listing customers: %w", err)
	}
	return n, nil
}
