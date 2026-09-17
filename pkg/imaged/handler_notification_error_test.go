package imaged

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHandleNotificationReturnsDeploymentFailureToDurableConsumer(t *testing.T) {
	h := newTestHarness(t, state.DeploymentKindImage, api.Plan("hobby"), "")
	h.bld.buildErr = errors.New("mkfs: ENOSPC")
	handler := New(h.store, h.notif, fakePuller{
		digest: "sha256:abc",
		cfg:    oci.ImageConfig{Cmd: []string{"./app"}},
	}, h.bld, "./init", h.appsR, silentLogger())

	err := handler.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + h.app.ID + `","to":"` + h.dep.ID + `","kind":"image","image_digest":"sha256:abc"}`,
	})
	if err == nil {
		t.Fatal("HandleNotification returned nil after build failure; durable consumer would acknowledge the failed row")
	}
	if !strings.Contains(err.Error(), "handle deployment") || !strings.Contains(err.Error(), "build app layer") {
		t.Fatalf("HandleNotification error = %q, want deployment and build context", err)
	}
}

func TestHandleNotificationReturnsMalformedDurablePayloadError(t *testing.T) {
	h := &Handler{log: silentLogger()}
	err := h.HandleNotification(context.Background(), db.Notification{
		Channel: db.NotifySnapshotWritten,
		Payload: "{not-json",
	})
	if err == nil {
		t.Fatal("HandleNotification returned nil for malformed durable payload; outbox row would be acknowledged")
	}
	if !strings.Contains(err.Error(), "decode snapshot_written payload") {
		t.Fatalf("HandleNotification error = %q, want decode context", err)
	}
}
