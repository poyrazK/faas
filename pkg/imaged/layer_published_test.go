package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// noticeOrderReplicator records how many notifications imaged had sent when
// it replicated each layer.
type noticeOrderReplicator struct {
	notif *fakeNotifier
	sent  []int
}

func (r *noticeOrderReplicator) Replicate(context.Context, string) error {
	r.sent = append(r.sent, len(r.notif.calls))
	return nil
}

// assertLayerPublishedAfterReplication checks ADR-038 amendment 2026-11-17's
// order: layer_published names the deployment once its layer is replicated,
// and before snapshot_prime asks schedd to boot it.
func assertLayerPublishedAfterReplication(t *testing.T, notif *fakeNotifier, rep *noticeOrderReplicator, appID, depID string) {
	t.Helper()
	published, prime := -1, -1
	for i, call := range notif.calls {
		switch {
		case call.channel == db.NotifyLayerPublished && published < 0:
			published = i
		case call.channel == db.NotifySnapshotPrime && prime < 0:
			prime = i
		}
	}
	if published < 0 || prime < published {
		t.Fatalf("layer_published at %d, snapshot_prime at %d: want layer_published first", published, prime)
	}
	if len(rep.sent) == 0 || rep.sent[0] > published {
		t.Fatalf("replicated after %v notifications, layer_published is #%d: want replication first", rep.sent, published)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(notif.calls[published].payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["app_id"] != appID || payload["deployment_id"] != depID || len(payload) != 2 {
		t.Fatalf("layer_published payload = %v, want app %s and deployment %s", payload, appID, depID)
	}
}

type refusingNotifier struct{ calls int }

func (f *refusingNotifier) Notify(context.Context, string, string) error {
	f.calls++
	return errors.New("notify: connection closed")
}

func TestAnnounceLayerPublishedIsAdvisory(t *testing.T) {
	refusing := &refusingNotifier{}
	h := &Handler{notif: refusing, log: silentLogger()}
	h.announceLayerPublished(t.Context(), "app", state.Deployment{ID: "dep", RootfsKey: "apps/a/dep.ext4"})
	if refusing.calls != 1 {
		t.Fatalf("notify calls = %d, want 1", refusing.calls)
	}

	quiet := &fakeNotifier{}
	h = &Handler{notif: quiet, log: silentLogger()}
	h.announceLayerPublished(t.Context(), "app", state.Deployment{ID: "dep"})
	if len(quiet.calls) != 0 {
		t.Fatalf("announced a deployment without a layer: %v", quiet.calls)
	}
}
