package runtimequotahost

// adr: 628
import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

const migrationToken = "0123456789abcdef0123456789abcdef"

func TestMigrationJournalTokenBounds(t *testing.T) {
	for _, token := range []string{"", "lease", "0123456789ABCDEF0123456789ABCDEF", "0123456789abcdef0123456789abcdeg", "0123456789abcdef0123456789abcdef00", uuid.NewString()} {
		if validMigrationToken(token) {
			t.Fatal("unsafe or noncanonical lease token accepted", token)
		}
	}
	if !validMigrationToken(migrationToken) {
		t.Fatal("existing migration token rejected")
	}
}

func captureMigration(t *testing.T, j *Journal, p runtimequota.Placement) func() {
	t.Helper()
	finish, err := j.BeginMigrationCapture(t.Context(), p.Permit.InstanceID, migrationToken, p, "snap/source/mem", "snap/source/vmstate")
	if err != nil {
		t.Fatal(err)
	}
	return finish
}

func TestMigrationJournalRetainsSourceAndExclusiveCapture(t *testing.T) {
	j, _, p := fixture(t)
	launch(t, j, p)()
	finish := captureMigration(t, j, p)
	defer finish()
	r, err := j.MigrationSource(p.Permit.InstanceID, migrationToken)
	if err != nil || !samePlacement(r.Source, p) || r.Completed {
		t.Fatal("source not retained", r, err)
	}
	r.Source.Seat.ID = uuid.NewString()
	copy, err := j.MigrationSource(p.Permit.InstanceID, migrationToken)
	if err != nil || !samePlacement(copy.Source, p) {
		t.Fatal("caller mutated original source", err)
	}
	if _, err := j.MigrationSource(uuid.NewString(), migrationToken); !errors.Is(err, ErrBinding) {
		t.Fatal("runtime key was rebound", err)
	}
	if _, err := j.Begin(t.Context(), p); !errors.Is(err, ErrBinding) {
		t.Fatal("source command bypassed pending capture", err)
	}
	if _, err := j.BeginMigrationCapture(t.Context(), p.Permit.InstanceID, "abcdef0123456789abcdef0123456789", p, "another/mem", "another/vmstate"); err == nil {
		t.Fatal("competing migration captured source")
	}
	next := p.Clone()
	next.Seat.ID = uuid.NewString()
	if _, err := j.BeginMigrationResume(t.Context(), p.Permit.InstanceID, migrationToken, next); !errors.Is(err, ErrBinding) {
		t.Fatal("resume overlapped native capture", err)
	}
	if _, err := j.BeginResume(t.Context(), next); !errors.Is(err, ErrBinding) {
		t.Fatal("warm resume bypassed migration binding", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := j.WaitSeat(ctx, *p.Seat); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("capture disappeared from old-seat barrier", err)
	}
}

func TestMigrationJournalExactResumeRestartAndCompletion(t *testing.T) {
	j, dir, p := fixture(t)
	launch(t, j, p)()
	captureMigration(t, j, p)()
	next := p.Clone()
	next.Seat.ID = uuid.NewString()
	if _, err := j.BeginMigrationResume(t.Context(), p.Permit.InstanceID, "abcdef0123456789abcdef0123456789", next); !errors.Is(err, ErrMigrationNotFound) {
		t.Fatal("unknown lease acknowledged", err)
	}
	if _, err := j.BeginMigrationResume(t.Context(), p.Permit.InstanceID, migrationToken, p); !errors.Is(err, ErrBinding) {
		t.Fatal("old seat reopened", err)
	}
	finish, err := j.BeginMigrationResume(t.Context(), p.Permit.InstanceID, migrationToken, next)
	if err != nil {
		t.Fatal(err)
	}
	finish() // Lost/uncertain native result must retain the exact proposed seat.
	id := j.Identity()
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, id.NodeID, id.BootID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	other := next.Clone()
	other.Seat.ID = uuid.NewString()
	if _, err := reopened.BeginMigrationResume(t.Context(), p.Permit.InstanceID, migrationToken, other); !errors.Is(err, ErrBinding) {
		t.Fatal("retry replaced retained seat", err)
	}
	finish, err = reopened.BeginMigrationResume(t.Context(), p.Permit.InstanceID, migrationToken, next)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.CompleteMigrationResume(t.Context(), p.Permit.InstanceID, migrationToken, next); err != nil {
		t.Fatal(err)
	}
	finish()
	if err := reopened.Check(next); err != nil {
		t.Fatal("completed source remained unavailable", err)
	}
	r, err := reopened.MigrationSource(p.Permit.InstanceID, migrationToken)
	if err != nil || !r.Completed || !samePlacement(*r.Resume, next) || !samePlacement(r.Source, p) {
		t.Fatal("completion lost original source", r, err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	rebooted, err := Open(dir, id.NodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rebooted.Close() }()
	if err := rebooted.Check(next); !errors.Is(err, ErrBinding) {
		t.Fatal("old kernel serving authority reopened", err)
	}
	if r, err := rebooted.MigrationSource(p.Permit.InstanceID, migrationToken); err != nil || !r.Completed {
		t.Fatal("reboot erased historical binding", err)
	}
	if rows, err := rebooted.MigrationInventory(); err != nil || len(rows) != 1 || !rows[0].Completed {
		t.Fatal("inventory lost collected source", err)
	}
}

func TestMigrationJournalCrashAfterSourceWriteRecoversFence(t *testing.T) {
	j, dir, p := fixture(t)
	launch(t, j, p)()
	failure := errors.New("injected directory fsync uncertainty")
	j.beforeSync = func() error { return failure }
	if _, err := j.BeginMigrationCapture(t.Context(), p.Permit.InstanceID, migrationToken, p, "snap/source/mem", "snap/source/vmstate"); !errors.Is(err, ErrStorage) {
		t.Fatal("uncertain source write allowed native capture", err)
	}
	id := j.Identity()
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, id.NodeID, id.BootID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	r, err := reopened.MigrationSource(p.Permit.InstanceID, migrationToken)
	if err != nil || reopened.seats[p.Seat.ID].FenceID != r.FenceID {
		t.Fatal("source recovered without irreversible fence", r, err)
	}
	if _, err := reopened.Begin(t.Context(), p); !errors.Is(err, ErrBinding) {
		t.Fatal("recovered source executed", err)
	}
}

func TestMigrationJournalUncertainCompletionPoisonsOwner(t *testing.T) {
	j, dir, p := fixture(t)
	launch(t, j, p)()
	captureMigration(t, j, p)()
	next := p.Clone()
	next.Seat.ID = uuid.NewString()
	finish, err := j.BeginMigrationResume(t.Context(), p.Permit.InstanceID, migrationToken, next)
	if err != nil {
		t.Fatal(err)
	}
	j.beforeSync = func() error { return errors.New("completion fsync uncertain") }
	if err := j.CompleteMigrationResume(t.Context(), p.Permit.InstanceID, migrationToken, next); !errors.Is(err, ErrStorage) {
		t.Fatal("uncertain completion acknowledged", err)
	}
	finish()
	if _, err := j.MigrationSource(p.Permit.InstanceID, migrationToken); !errors.Is(err, ErrStorage) {
		t.Fatal("poisoned owner advertised completion", err)
	}
	id := j.Identity()
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, id.NodeID, id.BootID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	// The rename was observed after restart. Only the previously completed
	// guarded native operation permits this retained completion record.
	r, err := reopened.MigrationSource(p.Permit.InstanceID, migrationToken)
	if err != nil || !r.Completed || !samePlacement(*r.Resume, next) {
		t.Fatal("known completion was replaced", err)
	}
}
