// adr: 20261018083536 — log observers compose; a later consumer (ADR-746
// counts, runtime ledger projection) never displaces an earlier one (MCP
// protocol metrics).
package fcvm

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
)

func TestJailerVMM_LogObserversCompose(t *testing.T) {
	var order []string
	commit := func(name string) func(string, logbuf.Line) {
		return func(appID string, _ logbuf.Line) { order = append(order, name+":"+appID) }
	}
	stops := 0
	instance := func(name string) func(string, string, string) (func(logbuf.Line), func()) {
		return func(appID, deploymentID, id string) (func(logbuf.Line), func()) {
			return func(logbuf.Line) { order = append(order, name+":"+deploymentID+"/"+id) }, func() { stops++ }
		}
	}
	v := NewJailerVMM("/tmp/unused-log-observer-test", 0).
		WithLogCommitCallback(commit("protocol")).
		WithInstanceLogObserver(instance("coverage")).
		WithLogCommitCallback(nil).
		WithInstanceLogObserver(nil)
	v.WithLogCommitCallback(commit("counter"))
	v.WithInstanceLogObserver(instance("ledger"))

	ring := v.registerDeploymentRing("i-1", "app", "dep")
	if _, err := ring.Write("stderr", []byte("[error] boom\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	want := []string{"protocol:app", "counter:app", "coverage:dep/i-1", "ledger:dep/i-1"}
	if len(order) != len(want) {
		t.Fatalf("observers ran %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("observers ran %v, want %v", order, want)
		}
	}
	v.unregisterRing("i-1")
	if stops != 2 {
		t.Fatalf("stops = %d, want both instance observers stopped", stops)
	}
}
