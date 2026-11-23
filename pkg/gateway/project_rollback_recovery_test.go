// adr: 20261122113000
package gateway

import (
	"context"
	"testing"
	"time"
)

func TestProjectRollbackWatchRequiresRestoredDeployment(t *testing.T) {
	backend := &PGBackend{}
	before := time.Now().Add(-time.Second)
	observed := make(chan time.Time, 2)
	backend.WatchProjectRollbackRoute("app", "restored", 42, time.Now().Add(time.Minute),
		func(ctx context.Context, started, _ time.Time) (bool, error) {
			if err := ctx.Err(); err != nil {
				t.Errorf("response evidence context was canceled: %v", err)
			}
			observed <- started
			return true, nil
		})
	requestCtx, cancel := context.WithCancel(t.Context())
	cancel()
	outgoing := time.Now()
	started := outgoing.Add(time.Nanosecond)
	backend.observeProjectRollbackResponse(requestCtx, "app", "outgoing", outgoing, time.Now())
	backend.observeProjectRollbackResponse(requestCtx, "app", "restored", before, time.Now())
	backend.observeProjectRollbackResponse(requestCtx, "app", "restored", started, time.Now())
	select {
	case got := <-observed:
		if !got.Equal(started) {
			t.Fatalf("recorded request from before route installation: %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("the restored deployment did not record a successful response")
	}
}
