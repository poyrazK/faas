package db

import "testing"

func TestParseEdgeRuleConvergencePayloads(t *testing.T) {
	changed, err := ParseEdgeRuleChangedPayload(`{"app_id":"app-1","rule_id":"rule-1","op":"updated","phase":"prepare","generation":42,"match_hosts":["api.example.com"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Generation != 42 || changed.Phase != "prepare" || len(changed.MatchHosts) != 1 {
		t.Fatalf("changed = %+v", changed)
	}
	ack, err := ParseEdgeRuleAckPayload(`{"generation":42,"phase":"prepare","node":"fsn-2.faas"}`)
	if err != nil {
		t.Fatal(err)
	}
	if ack.Node != "fsn-2.faas" || ack.Generation != changed.Generation {
		t.Fatalf("ack = %+v", ack)
	}
	if _, err := ParseEdgeRuleAckPayload(`{"generation":42,"phase":"prepare"}`); err == nil {
		t.Fatal("incomplete acknowledgement was accepted")
	}
}
