package runtimequotahost

// adr: 498
import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestHostJournalExclusiveServingResume(t *testing.T) {
	j, _, original := fixture(t)
	launch(t, j, original)()
	next := original.Clone()
	next.Seat.ID = uuid.NewString()
	if _, err := j.BeginResume(t.Context(), next); !errors.Is(err, ErrBinding) {
		t.Fatal("unfenced old seat was replaced", err)
	}
	if _, err := j.FenceSeat(*original.Seat, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	physical := original.Clone()
	physical.Seat = nil
	active, err := j.Begin(t.Context(), physical)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.BeginResume(t.Context(), next); !errors.Is(err, ErrBusy) {
		t.Fatal("resume overlapped a physical operation", err)
	}
	if _, err := j.Begin(t.Context(), next); !errors.Is(err, ErrBusy) {
		t.Fatal("ordinary registration overlapped a physical operation", err)
	}
	active()
	finish, err := j.BeginResume(t.Context(), next)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"physical", "serving", "another-resume"} {
		placement := next
		if p == "physical" {
			placement.Seat = nil
		}
		var err error
		if p == "another-resume" {
			_, err = j.BeginResume(t.Context(), placement)
		} else {
			_, err = j.Begin(t.Context(), placement)
		}
		if !errors.Is(err, ErrBusy) {
			t.Fatal("operation bypassed exclusive resume", p, err)
		}
	}
	if err := j.Close(); !errors.Is(err, ErrBusy) {
		t.Fatal("owner closed during resume", err)
	}
	finish()
	finish()
	retry, err := j.BeginResume(t.Context(), next)
	if err != nil {
		t.Fatal("exact seat retry failed", err)
	}
	// Releasing an old operation twice cannot clear the new exclusive barrier.
	finish()
	if _, err := j.Begin(t.Context(), next); !errors.Is(err, ErrBusy) {
		t.Fatal("old release cleared a new resume", err)
	}
	retry()
	if _, err := j.BeginResume(t.Context(), original); !errors.Is(err, ErrFenced) {
		t.Fatal("resume reopened the old seat", err)
	}
	if _, err := j.BeginResume(t.Context(), physical); err == nil {
		t.Fatal("physical-only bookkeeping resumed serving")
	}
	if j.permits[original.Permit.ID].FenceID != "" {
		t.Fatal("seat replacement released or fenced physical capacity")
	}
}
