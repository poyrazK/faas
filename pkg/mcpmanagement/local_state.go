package mcpmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// NewLocalServerWithState authenticates the local account once and restores
// its expiring review handles. The caller owns the store and must drain
// handlers before closing it. Ordinary NewServer remains network-free.
func NewLocalServerWithState(ctx context.Context, client *api.Client, opts Options, store *StateStore) (*mcp.Server, error) {
	if client == nil || store == nil || opts.Environments != nil {
		return nil, errors.New("local state requires a journal and unrestricted local environment access")
	}
	account, err := client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, errors.New("local state requires an authenticated account identity")
	}
	identity, _ := json.Marshal([]string{opts.APIBase, opts.Profile, account.ID})
	digest := sha256.Sum256(identity)
	opts.planStore, opts.planNamespace = store, "local-"+hex.EncodeToString(digest[:])
	return NewServer(client, opts)
}
