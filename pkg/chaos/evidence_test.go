package chaos

import "testing"

func TestTCPAttemptIDSeparatesCountersAndScope(t *testing.T) {
	rule := Rule{From: "app", To: "database", Kind: KindTCPTimeout, Port: 5432, Percent: 100}
	id := TCPAttemptID(rule)
	if len(id) != 64 || id == RuleID(rule) || id != TCPAttemptID(rule) {
		t.Fatal("counter identity is not stable and distinct")
	}
	for _, modified := range []Rule{{From: "worker", To: "database", Kind: KindTCPTimeout, Port: 5432, Percent: 100}, {From: "app", To: "database", Kind: KindTCPTimeout, Port: 5433, Percent: 100}} {
		if TCPAttemptID(modified) == id {
			t.Fatal("attempt scopes collided")
		}
	}
}
