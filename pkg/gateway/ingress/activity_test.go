package ingress

// adr: 700

import (
	"math"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func testGeneration() Generation { return Generation{uuid.NewString(), uuid.NewString()} }

func TestAdmissionGenerationsRetainPendingAndLateOldBindings(t *testing.T) {
	tk := NewActivityTracker()
	old, next, third := testGeneration(), testGeneration(), testGeneration()
	a, err := tk.Begin()
	if err != nil {
		t.Fatal(err)
	}
	pending := tk.Snapshot(next)
	if !pending.Known || pending.Pending != 1 || pending.Previous != 0 {
		t.Fatal(pending)
	}
	// The database returned old before a transition, but the caller binds later.
	if err := a.Bind(old); err != nil {
		t.Fatal(err)
	}
	b, _ := tk.Begin()
	if err := b.Bind(next); err != nil {
		t.Fatal(err)
	}
	got := tk.Snapshot(next)
	if got.Pending != 0 || got.Previous != 1 || got.Current != 1 || got.Version <= pending.Version {
		t.Fatal(got)
	}
	if got := tk.Snapshot(third); got.Previous != 2 || got.Current != 0 {
		t.Fatal(got)
	}
	a.Finish()
	a.Finish()
	if got := tk.Snapshot(next); got.Previous != 0 || got.Current != 1 {
		t.Fatal(got)
	}
	if err := a.Bind(next); err == nil {
		t.Fatal("completed admission rebound")
	}
	if err := b.Bind(third); err == nil {
		t.Fatal("bound admission changed generation")
	}
	b.Finish()
	if got := tk.Snapshot(next); !got.Known || got.Pending+got.Current+got.Previous != 0 {
		t.Fatal(got)
	}
	bad, _ := tk.Begin()
	if err := bad.Bind(Generation{PublicRevision: "bad"}); err == nil {
		t.Fatal("invalid generation accepted")
	}
	if got := tk.Snapshot(next); got.Pending != 1 {
		t.Fatal("invalid bind lost scope", got)
	}
	bad.Finish()
	if got := tk.Snapshot(Generation{}); got.Known {
		t.Fatal("invalid observation claimed coverage")
	}
}

func TestAdmissionGenerationCapacityAndVersionExhaustionStayUnknown(t *testing.T) {
	g := testGeneration()
	for _, kind := range []string{"forwards", "generations", "version"} {
		t.Run(kind, func(t *testing.T) {
			tk := NewActivityTracker()
			var scopes []*Admission
			switch kind {
			case "forwards":
				for range api.RuntimeUpgradeActivityForwardLimit {
					a, err := tk.Begin()
					if err != nil {
						t.Fatal(err)
					}
					scopes = append(scopes, a)
				}
				if _, err := tk.Begin(); err == nil {
					t.Fatal("capacity exceeded")
				}
			case "generations":
				for range api.RuntimeUpgradeActivityKeyLimit {
					a, _ := tk.Begin()
					if err := a.Bind(testGeneration()); err != nil {
						t.Fatal(err)
					}
					scopes = append(scopes, a)
				}
				a, _ := tk.Begin()
				scopes = append(scopes, a)
				if err := a.Bind(g); err == nil {
					t.Fatal("generation capacity exceeded")
				}
			case "version":
				tk.version = math.MaxInt64
				a, _ := tk.Begin()
				scopes = append(scopes, a)
				if got := tk.Snapshot(g); got.Version != math.MaxInt64 {
					t.Fatal("version wrapped", got)
				}
			}
			for _, a := range scopes {
				a.Finish()
			}
			if got := tk.Snapshot(g); got.Known || got.Pending+got.Current+got.Previous != 0 {
				t.Fatal("completion recovered unknown coverage", got)
			}
		})
	}
	if _, err := new(ActivityTracker).Begin(); err == nil {
		t.Fatal("uninitialized tracker accepted admission")
	}
}

func TestAdmissionGenerationConcurrentSnapshotsAndCompletions(t *testing.T) {
	tk, g := NewActivityTracker(), testGeneration()
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 32 {
				a, err := tk.Begin()
				if err != nil {
					t.Error(err)
					return
				}
				if err := a.Bind(g); err != nil {
					t.Error(err)
				}
				if got := tk.Snapshot(g); !got.Known || got.Previous != 0 || got.Current < 1 {
					t.Error(got)
				}
				a.Finish()
				a.Finish()
			}
		}()
	}
	wg.Wait()
	if got := tk.Snapshot(g); !got.Known || got.Pending+got.Current+got.Previous != 0 {
		t.Fatal(got)
	}
}
