package runtimequotahost

// adr: 629
import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestPermitInventoryRetainsFencedAndHistoricalOriginalBindings(t *testing.T) {
	j, root, p := fixture(t)
	launch(t, j, p)()
	fence := uuid.NewString()
	if _, err := j.FencePermit(p.Permit, fence); err != nil {
		t.Fatal(err)
	}
	rows, err := j.PermitInventory()
	if err != nil || len(rows) != 1 || !rows[0].Registered || rows[0].FenceID != fence || rows[0].Permit != p.Permit {
		t.Fatal("inventory lost retained fenced ownership", err)
	}
	if err := j.CheckRetainedPlacement(p); err != nil {
		t.Fatal("fenced original source was lost", err)
	}
	changed := p.Clone()
	changed.Seat.ID = uuid.NewString()
	if err := j.CheckRetainedPlacement(changed); !errors.Is(err, ErrBinding) {
		t.Fatal("unissued initial seat borrowed retained physical binding", err)
	}
	rows[0].Permit.RunID = uuid.NewString()
	if again, err := j.PermitInventory(); err != nil || again[0].Permit != p.Permit {
		t.Fatal("inventory caller replaced original binding", err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PermitInventory(); !errors.Is(err, ErrStorage) {
		t.Fatal("closed owner advertised complete inventory", err)
	}
	rebooted, err := Open(root, p.Permit.NodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rebooted.Close() })
	if old, err := rebooted.PermitInventory(); err != nil || len(old) != 1 || old[0].Permit != p.Permit || old[0].FenceID != fence {
		t.Fatal("reboot erased historical source", err)
	}
	if err := rebooted.Check(p); !errors.Is(err, ErrBinding) {
		t.Fatal("historical inventory became current execution authority", err)
	}
	if err := rebooted.CheckRetainedPlacement(p); err != nil {
		t.Fatal("historical original provenance was lost", err)
	}
}

func TestPoisonedOwnerCannotAdvertisePermitInventory(t *testing.T) {
	j, _, p := fixture(t)
	j.beforeSync = func() error { return errors.New("uncertain durability") }
	if _, err := j.BeginLaunch(t.Context(), p); !errors.Is(err, ErrStorage) {
		t.Fatal(err)
	}
	if _, err := j.PermitInventory(); !errors.Is(err, ErrStorage) {
		t.Fatal("poisoned owner advertised complete physical inventory", err)
	}
}
