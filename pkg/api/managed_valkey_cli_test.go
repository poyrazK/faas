package api

import "testing"

// `gregale redis cli` sends one command through the app's network; the task
// admits only a managed cache address and a command that answers once.
func TestAppTaskRedisCommandShape(t *testing.T) {
	ok := ManagedValkeyCLICommand("sessions", []string{"GET", "user:1"})
	for _, tc := range []struct {
		name        string
		command     []string
		shell       bool
		interactive bool
		want        bool
	}{
		{"get", ok, false, false, true},
		{"lower-case command", ManagedValkeyCLICommand("sessions", []string{"ping"}), false, false, true},
		{"no command", ManagedValkeyCLICommand("sessions", nil), false, false, false},
		{"blank command", ManagedValkeyCLICommand("sessions", []string{" "}), false, false, false},
		{"shell", ok, true, false, false},
		{"interactive", ok, false, true, false},
		{"subscribe", ManagedValkeyCLICommand("sessions", []string{"subscribe", "c"}), false, false, false},
		{"monitor", ManagedValkeyCLICommand("sessions", []string{"MONITOR"}), false, false, false},
		{"other host", []string{AppTaskRedisCommand, "10.0.0.5:6379", "PING"}, false, false, false},
		{"other port", []string{AppTaskRedisCommand, "sessions.svc.gregale:6380", "PING"}, false, false, false},
		{"other domain", []string{AppTaskRedisCommand, "sessions.internal:6379", "PING"}, false, false, false},
		{"bad cache name", []string{AppTaskRedisCommand, "Bad_Name.svc.gregale:6379", "PING"}, false, false, false},
	} {
		if got := ValidAppTaskRedisCommand(tc.command, tc.shell, tc.interactive); got != tc.want {
			t.Errorf("%s: valid = %v, want %v", tc.name, got, tc.want)
		}
	}
	if _, problem := (CreateAppTaskRequest{Command: ok}).Resolve(); problem != nil {
		t.Fatalf("valid redis task refused: %s", problem.Detail)
	}
	bad := CreateAppTaskRequest{Command: []string{AppTaskRedisCommand, "10.0.0.5:6379", "PING"}}
	if _, problem := bad.Resolve(); problem == nil {
		t.Fatal("redis task to a non-cache address admitted")
	}
	if _, problem := (CreateAppTaskRequest{Command: ok, Interactive: true}).Resolve(); problem == nil {
		t.Fatal("interactive redis task admitted")
	}
}
