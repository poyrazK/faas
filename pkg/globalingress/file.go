package globalingress

// adr: 656
import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Start validates the first projection before serving and subsequently reads
// it off the request path. Cancellation joins the refresh worker.
func (g *Gateway) Start(ctx context.Context, path string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, ErrUnavailable)
	}
	refresh := func() error {
		raw, err := ReadManifest(path)
		if err != nil {
			g.unavailable()
			return err
		}
		return g.Refresh(raw)
	}
	if err := refresh(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(api.GlobalIngressRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = refresh()
			}
		}
	}()
	return func() {
		cancel()
		<-done
		if t, ok := g.transport.(interface{ CloseIdleConnections() }); ok {
			t.CloseIdleConnections()
		}
	}, nil
}
