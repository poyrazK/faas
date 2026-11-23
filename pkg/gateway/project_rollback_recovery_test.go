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
		func(_ context.Context, started, _ time.Time) (bool, error) {
			observed <- started
			return true, nil
		})
	outgoing := time.Now()
	started := outgoing.Add(time.Nanosecond)
	backend.observeProjectRollbackResponse("app", "outgoing", outgoing, time.Now())
	backend.observeProjectRollbackResponse("app", "restored", before, time.Now())
	backend.observeProjectRollbackResponse("app", "restored", started, time.Now())
	select {
	case got := <-observed:
		if !got.Equal(started) {
			t.Fatalf("recorded request from before route installation: %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("the restored deployment did not record a successful response")
	}
}
