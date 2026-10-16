package functionlifetime

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestClientLostAcknowledgementAndCleanupFence(t *testing.T) {
	var mu sync.Mutex
	active := map[uint64]bool{}
	begins, finishes := 0, 0
	client := NewClient(func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			defer func() { _ = b.Close() }()
			r, err := ReadRequest(b)
			if err != nil {
				return
			}
			mu.Lock()
			if r.Kind == Begin {
				begins++
				active[r.Sequence] = true
			} else {
				finishes++
				delete(active, r.Sequence)
			}
			drop := r.Kind == Begin && begins == 1
			mu.Unlock()
			if drop {
				return
			} // host accepted; first acknowledgement was lost
			_ = Write(b, Reply{Version: Version, Status: OK, Plan: api.PlanFree})
		}()
		return a, nil
	})
	lease, plan, err := client.Acquire(t.Context(), 30*time.Second, "")
	if err != nil || plan != api.PlanFree {
		t.Fatalf("acquire: plan %s, %v", plan, err)
	}
	mu.Lock()
	count, calls := len(active), begins
	mu.Unlock()
	if count != 1 || calls != 2 {
		t.Fatal("uncertain registration was not retried under one ID")
	}
	cleanup := make(chan struct{})
	lease.ReleaseAfter(t.Context(), cleanup)
	lease.ReleaseAfter(t.Context(), cleanup)
	short, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if lease.Wait(short) == nil {
		t.Fatal("released ownership before cleanup")
	}
	mu.Lock()
	early := finishes
	mu.Unlock()
	if early != 0 {
		t.Fatal("completion sent while interpreter cleanup was running")
	}
	close(cleanup)
	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err := lease.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(active) != 0 || finishes != 1 {
		t.Fatal("terminal ownership was not released exactly once")
	}
}

func TestProtocolRejectsMalformedAndUnboundedFrames(t *testing.T) {
	var badSize [4]byte
	binary.BigEndian.PutUint32(badSize[:], ^uint32(0))
	if _, err := ReadRequest(bytes.NewReader(badSize[:])); err == nil {
		t.Fatal("accepted unbounded allocation")
	}
	for _, req := range []Request{
		{Version: 2, Kind: Begin, Sequence: 1, BudgetMS: 1},
		{Version: Version, Kind: Begin, Sequence: 0, BudgetMS: 1},
		{Version: Version, Kind: Begin, Sequence: 1, BudgetMS: api.RequestBudgetPlanMax.Milliseconds() + 1},
		{Version: Version, Kind: Finish, Sequence: 1, Hook: "before_checkpoint"},
		{Version: Version, Kind: Retire, Sequence: 1},
		{Version: Version, Kind: "complete-on-eof", Sequence: 1},
	} {
		var encoded bytes.Buffer
		if err := Write(&encoded, req); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRequest(&encoded); err == nil {
			t.Fatalf("accepted invalid request %+v", req)
		}
	}
	for _, body := range []string{`{"version":1,"kind":"finish","sequence":1,"unknown":"secret"}`, `{"version":1,"kind":"finish","sequence":1} {}`} {
		var frame bytes.Buffer
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(body)))
		frame.Write(size[:])
		frame.WriteString(body)
		if _, err := ReadRequest(&frame); err == nil {
			t.Fatal("accepted unknown or trailing input")
		}
	}
}

func TestClientRefusalCannotExecuteAndTombstonesReservation(t *testing.T) {
	finished := make(chan struct{})
	client := NewClient(func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			defer func() { _ = b.Close() }()
			r, err := ReadRequest(b)
			if err != nil {
				return
			}
			status := Busy
			if r.Kind == Finish {
				status = OK
				close(finished)
			}
			_ = Write(b, Reply{Version: Version, Status: status, Plan: api.PlanPro})
		}()
		return a, nil
	})
	if lease, _, err := client.Acquire(t.Context(), time.Second, ""); err == nil || lease != nil {
		t.Fatal("refused reservation became executable")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("failed reservation not tombstoned")
	}
}
