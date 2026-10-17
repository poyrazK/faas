package functionmigration

// adr: 1578
// This process protocol test is portable Linux fixture validation, not native
// scheduler, VM, PostgreSQL or service-restart acceptance.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCoordinatorProcessPeer(t *testing.T) {
	if os.Getenv("FAAS_COORDINATOR_PROTOCOL_TEST") != "1" {
		return
	}
	events := os.NewFile(3, "coordinator-events")
	defer events.Close()
	if err := json.NewEncoder(events).Encode(CoordinatorEvent{Phase: "ready"}); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil || line != "continue\n" {
		t.Fatal("missing explicit boundary release")
	}
	if err := json.NewEncoder(events).Encode(CoordinatorEvent{Phase: "released"}); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestCoordinatorProcessCrashProtocol(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var previous CoordinatorIdentity
	for i := range 2 {
		child, err := StartCoordinator(ctx, binary, []string{"-test.run=^TestCoordinatorProcessPeer$"}, append(os.Environ(), "FAAS_COORDINATOR_PROTOCOL_TEST=1"), filepath.Join(t.TempDir(), "child.log"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = child.Close() })
		if _, err := child.Event(ctx, "ready"); err != nil {
			t.Fatal(err)
		}
		if child.Identity.PID <= 0 || !processStart(child.Identity.Start) || !hexBytes(child.Identity.BinarySHA, 32) {
			t.Fatal("missing observed process identity")
		}
		if i == 1 && (previous.PID == child.Identity.PID && previous.Start == child.Identity.Start || previous.BinarySHA != child.Identity.BinarySHA) {
			t.Fatal("replacement did not start the same executable in a new process")
		}
		if err := child.Release(); err != nil {
			t.Fatal(err)
		}
		if _, err := child.Event(ctx, "released"); err != nil {
			t.Fatal(err)
		}
		if err := child.Kill(ctx); err != nil {
			t.Fatal(err)
		}
		previous = child.Identity
	}
}
