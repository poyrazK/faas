package ingress

// adr: 615

import (
	"math"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestWithdrawalClosesNewAndLateAdmissionWithoutDiscardingScopes(t *testing.T) {
	tk, g := NewActivityTracker(), testGeneration()
	a, _ := tk.Begin()
	if err := a.Bind(g); err != nil {
		t.Fatal(err)
	}
	b, _ := tk.Begin() // database authorization has not returned yet
	id := uuid.NewString()
	w, err := tk.Withdraw(id)
	if err != nil || !w.Closed || !w.Known || w.Active != 2 || w.FenceID == "" {
		t.Fatal(w, err)
	}
	if _, err := tk.Begin(); err == nil {
		t.Fatal("closed admission reopened")
	}
	if err := b.Bind(g); err == nil {
		t.Fatal("late authorization crossed fence")
	}
	if again, _ := tk.Withdraw(id); again != w {
		t.Fatal("rejected work changed fence", again, w)
	}
	if _, err := tk.Withdraw(uuid.NewString()); err == nil {
		t.Fatal("fence changed intent")
	}
	a.Finish()
	a.Finish()
	if got, _ := tk.Withdraw(id); got.Active != 1 || !got.Closed {
		t.Fatal("pending authorization lost", got)
	}
	b.Finish()
	zero, err := tk.Withdraw(id)
	if err != nil || !zero.Known || zero.Active != 0 || zero.FenceID != w.FenceID || zero.Version <= w.Version {
		t.Fatal(zero, err)
	}
	for range 8 {
		if _, err := tk.Begin(); err == nil {
			t.Fatal("sealed process accepted work")
		}
	}
	if got, _ := tk.Withdraw(id); got != zero {
		t.Fatal("sealed fence not stable", got, zero)
	}
}

func TestWithdrawalInvalidIntentAndUnknownCoverageCannotSeal(t *testing.T) {
	tk := NewActivityTracker()
	for _, id := range []string{"", "bad", uuid.Nil.String(), "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"} {
		if _, err := tk.Withdraw(id); err == nil {
			t.Fatal("invalid withdrawal accepted", id)
		}
	}
	a, err := tk.Begin()
	if err != nil {
		t.Fatal("invalid intent closed admission", err)
	}
	a.Finish()
	if _, err := new(ActivityTracker).Withdraw(uuid.NewString()); err == nil {
		t.Fatal("uninitialized tracker claimed coverage")
	}
	for _, version := range []int64{1, math.MaxInt64} {
		tk := NewActivityTracker()
		tk.version, tk.known = version, version != 1
		w, err := tk.Withdraw(uuid.NewString())
		if err != nil || !w.Closed || w.Known || w.Active != 0 || w.Version < 1 {
			t.Fatal("unknown zero recovered", w, err)
		}
		if _, err := tk.Begin(); err == nil {
			t.Fatal("unknown fence accepted admission")
		}
	}
}

func TestWithdrawalConcurrentBeginBindAndCompletionKeepKnownZeroPermanent(t *testing.T) {
	tk, g, id := NewActivityTracker(), testGeneration(), uuid.NewString()
	start, finish := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			a, err := tk.Begin()
			if err != nil {
				return
			}
			_ = a.Bind(g) // racing closure may reject a still-pending scope
			<-finish
			a.Finish()
			a.Finish()
		}()
	}
	close(start)
	w, err := tk.Withdraw(id)
	if err != nil || !w.Closed || !w.Known {
		t.Fatal(w, err)
	}
	for range 64 {
		if _, err := tk.Begin(); err == nil {
			t.Fatal("begin crossed acknowledged fence")
		}
	}
	close(finish)
	wg.Wait()
	zero, err := tk.Withdraw(id)
	if err != nil || !zero.Known || zero.Active != 0 {
		t.Fatal(zero, err)
	}
	if got, _ := tk.Withdraw(id); got != zero {
		t.Fatal("sealed zero mutated", got, zero)
	}
}
