// adr: 1344
// boot_wait_test.go pins ADR-1344 §5 rule 6: under a Type=notify unit a
// daemon waits for an unreachable database (extending its start timeout)
// instead of exiting into the systemd restart limit, while configuration
// errors and non-systemd callers still fail fast.

package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func testBootWaiter(enabled bool, notes *[]string) bootWaiter {
	return bootWaiter{
		enabled:    enabled,
		notify:     func(s string) error { *notes = append(*notes, s); return nil },
		minBackoff: time.Millisecond,
		maxBackoff: 2 * time.Millisecond,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// failingPing fails the first n calls with err, then succeeds.
func failingPing(n int, err error) (func(context.Context) error, *int) {
	calls := 0
	return func(context.Context) error {
		calls++
		if calls <= n {
			return err
		}
		return nil
	}, &calls
}

// adr: 1344
func TestBootWaiterWaitsForUnreachableDatabase(t *testing.T) {
	var notes []string
	ping, calls := failingPing(3, syscall.ECONNREFUSED)
	if err := testBootWaiter(true, &notes).ping(context.Background(), ping); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if *calls != 4 {
		t.Fatalf("ping calls = %d, want 4", *calls)
	}
	extends := 0
	for _, n := range notes {
		if strings.HasPrefix(n, "EXTEND_TIMEOUT_USEC=") && strings.Contains(n, "STATUS=waiting for PostgreSQL") {
			extends++
		}
	}
	if extends != 3 {
		t.Fatalf("start-timeout extensions = %d, want one per failed attempt (3): %q", extends, notes)
	}
	if last := notes[len(notes)-1]; last != "STATUS=PostgreSQL reachable" {
		t.Fatalf("final status = %q", last)
	}
}

// adr: 1344
// TestBootWaitRecordReportsAWait: a daemon learns that it started against an
// unavailable database (ADR-1344 §5 rule 4), and only then.
func TestBootWaitRecordReportsAWait(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failures int
		want     bool
	}{
		{"reachable at once", 0, false},
		{"waited", 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var notes []string
			var rec BootWaitRecord
			ping, _ := failingPing(tc.failures, syscall.ECONNREFUSED)
			if err := testBootWaiter(true, &notes).ping(WithBootWaitRecord(context.Background(), &rec), ping); err != nil {
				t.Fatalf("ping: %v", err)
			}
			if rec.Waited() != tc.want {
				t.Fatalf("Waited() = %v, want %v", rec.Waited(), tc.want)
			}
		})
	}
	if (*BootWaitRecord)(nil).Waited() {
		t.Fatal("nil record reports a wait")
	}
}

// adr: 1344
func TestBootWaiterFailsFastOutsideSystemd(t *testing.T) {
	var notes []string
	ping, calls := failingPing(3, syscall.ECONNREFUSED)
	if err := testBootWaiter(false, &notes).ping(context.Background(), ping); err == nil {
		t.Fatal("disabled waiter hid the connection error")
	}
	if *calls != 1 || len(notes) != 0 {
		t.Fatalf("calls=%d notes=%q, want one attempt and no notifications", *calls, notes)
	}
}

// adr: 1344
func TestBootWaiterFailsFastOnConfigurationErrors(t *testing.T) {
	for _, code := range []string{"28P01", "3D000"} { // bad password, missing database
		var notes []string
		ping, calls := failingPing(3, &pgconn.PgError{Code: code})
		if err := testBootWaiter(true, &notes).ping(context.Background(), ping); err == nil {
			t.Fatalf("%s: waiter retried a configuration error to success", code)
		}
		if *calls != 1 {
			t.Fatalf("%s: ping calls = %d, want 1", code, *calls)
		}
	}
}

// adr: 1344
func TestBootWaiterStopsOnCancel(t *testing.T) {
	var notes []string
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	ping := func(context.Context) error {
		calls++
		if calls == 2 {
			cancel()
		}
		return syscall.ECONNREFUSED
	}
	if err := testBootWaiter(true, &notes).ping(ctx, ping); err == nil {
		t.Fatal("cancelled wait returned nil")
	}
	if calls > 3 {
		t.Fatalf("wait kept pinging after cancel: %d calls", calls)
	}
}

// adr: 1344
func TestRetryableBootError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{syscall.ECONNREFUSED, true},
		{context.DeadlineExceeded, true},
		{&pgconn.PgError{Code: "57P03"}, true}, // starting up
		{&pgconn.PgError{Code: "53300"}, true}, // too many connections
		{&pgconn.PgError{Code: "08006"}, true}, // connection failure
		{&pgconn.PgError{Code: "28P01"}, false},
		{&pgconn.PgError{Code: "3D000"}, false},
		{context.Canceled, false},
		{errors.New("dial unix /run/postgresql/.s.PGSQL.5432: connect: no such file or directory"), true},
	}
	for _, tc := range cases {
		if got := retryableBootError(tc.err); got != tc.want {
			t.Errorf("retryableBootError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
