package mcphosting

import (
	"context"
	"errors"
	"net/http"
)

// ObserveCatalog initializes and discovers metadata only. It never invokes a tool,
// reads resource contents or renders prompts. A caller may supply a guarded transport.
func ObserveCatalog(ctx context.Context, endpoint, token, version string, httpClient *http.Client) ([]byte, error) {
	client, err := NewClient(endpoint, token, version)
	if err != nil {
		return nil, err
	}
	if httpClient != nil {
		client.HTTP = httpClient
	}
	if err = client.InitializeStateless(ctx); err != nil {
		return nil, err
	}
	catalog, exchange, err := client.DiscoverCatalog(ctx)
	if err != nil {
		return nil, err
	}
	if len(exchange.RejectedTools) != 0 {
		return nil, errors.New("incomplete catalog")
	}
	contract, err := NewCatalogContract(client.Version, catalog)
	if err != nil {
		return nil, err
	}
	return MarshalContract(contract)
}
