package fcvm

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
)

func TestMCPLogIntakeUsesHostAppIdentity(t *testing.T) {
	var ids []string
	v := NewJailerVMM("/tmp/unused-mcp-log-test", 0).WithLogCommitCallback(func(appID string, _ logbuf.Line) { ids = append(ids, appID) })
	ring := v.registerAppRing("instance-a", "host-app")
	_, _ = ring.Write("stdout", []byte("{\"app_id\":\"forged-app\"}\n"))
	_, _, cancel := ring.SnapshotAndSubscribe(1)
	defer cancel()
	if len(ids) != 1 || ids[0] != "host-app" {
		t.Fatalf("intake identity=%v", ids)
	}
	legacy := v.registerRing("instance-without-identity")
	_, _ = legacy.Write("stdout", []byte("hello\n"))
	if len(ids) != 1 {
		t.Fatal("missing identity observed")
	}
}

func TestMCPInstanceLogLifecycle(t *testing.T) {
	var identities []string
	commits, stops := 0, 0
	v := NewJailerVMM("/tmp/unused-mcp-log-test", 0).WithInstanceLogObserver(func(app, dep, instance string) (func(logbuf.Line), func()) {
		identities = append(identities, app+"/"+dep+"/"+instance)
		return func(logbuf.Line) { commits++ }, func() { stops++ }
	})
	ring := v.registerDeploymentRing("replica", "app", "deployment")
	_, _ = ring.Write("stdout", []byte("heartbeat\n"))
	_, _, cancel := ring.SnapshotAndSubscribe(1)
	cancel()
	if commits != 1 || identities[0] != "app/deployment/replica" {
		t.Fatal("identity or replay semantics")
	}
	v.registerDeploymentRing("replica", "app", "replacement")
	v.unregisterRing("replica")
	v.unregisterRing("replica")
	if stops != 2 {
		t.Fatalf("stops=%d", stops)
	}
}
