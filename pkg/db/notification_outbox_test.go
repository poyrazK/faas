package db

import "testing"

func TestDurableNotificationChannelSet(t *testing.T) {
	for _, channel := range []string{NotifyAppWake, NotifyRuntimeConfigRestart, NotifyPrivateNetworkAttachmentChanged, NotifyPrivateNetworkChanged, NotifySnapshotPrime, NotifySnapshotBoot, NotifySnapshotWritten, NotifyDeploymentReady, NotifyAppTaskChanged} {
		if !IsDurableNotificationChannel(channel) {
			t.Fatalf("%q is not marked durable", channel)
		}
	}
	for _, channel := range []string{NotifyAppChanged, NotifyDeploymentChanged, NotifyBuildQueued} {
		if IsDurableNotificationChannel(channel) {
			t.Fatalf("%q should remain advisory", channel)
		}
	}
}

func TestParseAppTaskChangedPayload(t *testing.T) {
	payload, err := ParseAppTaskChangedPayload(`{"account_id":"account","app_id":"app","deployment_id":"deployment","task_id":"task","kind":"release","status":"succeeded"}`)
	if err != nil {
		t.Fatalf("ParseAppTaskChangedPayload: %v", err)
	}
	if payload.TaskID != "task" || payload.DeploymentID != "deployment" || payload.Kind != "release" || payload.Status != "succeeded" {
		t.Fatalf("payload = %+v", payload)
	}
	if _, err := ParseAppTaskChangedPayload(`{"task_id":"task"}`); err == nil {
		t.Fatal("incomplete app task payload accepted")
	}
}

func TestNotificationEnvelopeRoundTrip(t *testing.T) {
	const payload = `{"app_id":"a","deployment_id":"d"}`
	wire := wrapNotificationPayload(42, payload)
	n := decodeNotification(NotifySnapshotPrime, wire)
	if n.OutboxID != 42 || n.Payload != payload {
		t.Fatalf("decoded notification = %#v, want id=42 payload=%q", n, payload)
	}
}

func TestNotificationEnvelopeLeavesLegacyPayloadsUntouched(t *testing.T) {
	const payload = `{"app_id":"a"}`
	legacy := decodeNotification(NotifySnapshotPrime, payload)
	if legacy.OutboxID != 0 || legacy.Payload != payload {
		t.Fatalf("legacy notification = %#v, want untouched payload", legacy)
	}
	nonDurable := decodeNotification(NotifyAppChanged, wrapNotificationPayload(42, payload))
	if nonDurable.OutboxID != 0 || nonDurable.Payload != wrapNotificationPayload(42, payload) {
		t.Fatalf("non-durable notification was unwrapped: %#v", nonDurable)
	}
}
