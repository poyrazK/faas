// Package preview adds the API methods of preview and internal capabilities
// to the Go SDK (ADR-20261023115741).
//
// faas.Client exposes generally available and beta capabilities only. A
// preview.Client is a faas.Client plus the methods for realtime, operations,
// events, queues, object storage, managed Postgres, disposable runs, issues,
// commit, data repair and the other preview capabilities. The helpers built
// on them live here too: operation handlers and transactions, the event
// outbox, acknowledgment and consumer libraries, durable entities, data
// repair, commit, feature flags, issue reporting, and the object storage and
// Postgres binding runtimes. apid serves a preview capability only to the
// accounts it is released to; elsewhere its calls answer 404 no such API
// route.
package preview

import (
	faas "github.com/poyrazK/faas/sdk/go"
	"github.com/poyrazK/faas/sdk/go/internal/api"
)

// Client is a faas.Client with the preview capabilities' methods. Every
// faas.Client method resolves through the embedded faas.Client.
type Client struct {
	*faas.Client
	*api.PreviewClient
}

// NewClient builds a Client for baseURL with the given bearer token and the
// same options as faas.NewClient.
func NewClient(baseURL, token string, opts ...faas.Option) (*Client, error) {
	c, err := faas.NewClient(baseURL, token, opts...)
	if err != nil {
		return nil, err
	}
	return Wrap(c), nil
}

// Wrap adds the preview methods to an existing faas.Client; both share its
// transport, base URL and token.
func Wrap(c *faas.Client) *Client {
	return &Client{Client: c, PreviewClient: api.NewPreviewClient(c.Client)}
}
