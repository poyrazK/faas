package realtime

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Commands that can reach control-plane Postgres are bounded per connection;
// acks, presence and signals pass free (they have their own bounds).
func TestResumeCommandBudget(t *testing.T) {
	c := &connection{}
	for i := range api.RealtimeResumeCommandBurst {
		if !c.allowResumeCommand("subscribe") {
			t.Fatalf("command %d within the burst was refused", i)
		}
	}
	if c.allowResumeCommand("unsubscribe") {
		t.Fatal("a command past the burst was allowed")
	}
	for _, kind := range []string{"ack", "direct_message_ack", "inbox_ack", "presence", "signal"} {
		if !c.allowResumeCommand(kind) {
			t.Fatalf("%s was charged to the command budget", kind)
		}
	}
}

// A member writes its fleet presence row at most once per
// fleetPresenceWriteInterval; updates in between wait for a trailing write.
func TestFleetPresenceWritesAreCoalesced(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	key := channelKey{endpointID: "ep", channel: "room"}
	m.presenceMembers[key] = map[string]*presenceMember{"conn": {memberID: "member", announced: true}}
	now := time.Now()
	if write, _ := m.claimFleetPresenceWrite(key, "conn", now); !write {
		t.Fatal("the first update did not write")
	}
	write, after := m.claimFleetPresenceWrite(key, "conn", now.Add(300*time.Millisecond))
	if write || after <= 0 || after > fleetPresenceWriteInterval {
		t.Fatalf("an update 300 ms later = (%v, %s), want a trailing write within %s", write, after, fleetPresenceWriteInterval)
	}
	if write, _ := m.claimFleetPresenceWrite(key, "conn", now.Add(fleetPresenceWriteInterval)); !write {
		t.Fatal("an update after the interval did not write")
	}
	if write, _ := m.claimFleetPresenceWrite(key, "other", now); !write {
		t.Fatal("an unknown member did not write")
	}
}
