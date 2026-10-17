package vmmdgrpc

import (
	"context"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bridgecompletion"
)

func awaitBridgeCompletion(ctx context.Context, client *http.Client, exchange string) error {
	ctx, cancel := context.WithTimeout(ctx, api.TrafficBridgeCompletionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/", nil)
	if err != nil {
		return fmt.Errorf("build completion request: %w", err)
	}
	req.Header.Set(bridgecompletion.WaitHeader, exchange)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("wait for completion: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("bridge completion status %d", resp.StatusCode)
	}
	return nil
}
