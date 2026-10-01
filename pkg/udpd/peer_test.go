package udpd

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPeerQueueBoundsAndPayloadOwnership(t *testing.T) {
	replies := make(chan Reply, 1)
	peer, err := NewPeer(context.Background(), netip.MustParseAddrPort("127.0.0.1:1234"), replies)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	payload := []byte("hello")
	if !peer.Enqueue(payload) {
		t.Fatal("first datagram dropped")
	}
	payload[0] = 'X'
	for i := 1; i < api.UDPPeerQueueDepth; i++ {
		if !peer.Enqueue(nil) {
			t.Fatal("empty datagram dropped before queue full")
		}
	}
	if peer.Enqueue([]byte("overflow")) {
		t.Fatal("full queue grew")
	}
	got, err := peer.Receive(context.Background())
	if err != nil || string(got) != "hello" {
		t.Fatalf("payload ownership: %q %v", got, err)
	}
	reply := []byte("reply")
	if err := peer.Send(context.Background(), reply); err != nil {
		t.Fatal(err)
	}
	reply[0] = 'X'
	queued := <-replies
	if queued.Address.String() != "127.0.0.1:1234" || string(queued.Payload) != "reply" {
		t.Fatalf("reply identity or payload changed: %+v", queued)
	}
	peer.Close()
	if queued.Context.Err() == nil {
		t.Fatal("queued reply remained live after peer close")
	}
	if peer.Enqueue(nil) {
		t.Fatal("closed peer accepted input")
	}
}
func TestPeerCancellationUnblocksSendAndReceive(t *testing.T) {
	for _, send := range []bool{false, true} {
		peer, err := NewPeer(context.Background(), netip.MustParseAddrPort("127.0.0.1:1234"), make(chan Reply))
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			if send {
				done <- peer.Send(context.Background(), nil)
			} else {
				_, err := peer.Receive(context.Background())
				done <- err
			}
		}()
		peer.Close()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("close error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("peer close left blocked I/O")
		}
	}
}
func TestPeerPoolGlobalAndAccountCaps(t *testing.T) {
	pool := NewPeerPool(2, 1)
	releaseA, ok := pool.Acquire("a")
	if !ok {
		t.Fatal("first account denied")
	}
	if _, ok := pool.Acquire("a"); ok {
		t.Fatal("account cap bypassed")
	}
	releaseB, ok := pool.Acquire("b")
	if !ok {
		t.Fatal("independent account denied")
	}
	if _, ok := pool.Acquire("c"); ok {
		t.Fatal("global cap bypassed")
	}
	releaseA()
	releaseA()
	releaseC, ok := pool.Acquire("c")
	if !ok {
		t.Fatal("released global slot not reusable")
	}
	if _, ok := pool.Acquire("d"); ok {
		t.Fatal("duplicate release created extra capacity")
	}
	releaseB()
	releaseC()
	if _, ok := pool.Acquire(""); ok {
		t.Fatal("missing account admitted")
	}
}

func TestPeerPreCanceledCallsLeaveQueuesUnchanged(t *testing.T) {
	for i := 0; i < 50; i++ {
		replies := make(chan Reply, 1)
		peer, err := NewPeer(context.Background(), netip.MustParseAddrPort("127.0.0.1:1234"), replies)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if !peer.Enqueue([]byte("retained")) {
			t.Fatal("live peer rejected input")
		}
		if _, err := peer.Receive(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled receive consumed data: %v", err)
		}
		if payload, err := peer.Receive(context.Background()); err != nil || string(payload) != "retained" {
			t.Fatalf("queue changed: %q/%v", payload, err)
		}
		if err := peer.Send(ctx, []byte("rejected")); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled send published data: %v", err)
		}
		if len(replies) != 0 {
			t.Fatal("canceled send changed reply queue")
		}
		peer.Close()
	}
}

func TestPeerRejectsCanceledParentBeforeCreation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	peer, err := NewPeer(ctx, netip.MustParseAddrPort("127.0.0.1:1234"), make(chan Reply, 1))
	if peer != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("peer=%v/error=%v", peer, err)
	}
}

func TestPeerPoolConcurrentAdmissionAndDuplicateRelease(t *testing.T) {
	pool := NewPeerPool(3, 2)
	type admission struct {
		account  string
		release  func()
		admitted bool
	}
	results := make(chan admission, 48)
	var wg sync.WaitGroup
	for i := 0; i < 48; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			account := fmt.Sprintf("account-%d", i%3)
			release, ok := pool.Acquire(account)
			results <- admission{account, release, ok}
		}(i)
	}
	wg.Wait()
	close(results)
	var accepted []admission
	counts := make(map[string]int)
	for r := range results {
		if r.admitted {
			accepted = append(accepted, r)
			counts[r.account]++
		}
	}
	if len(accepted) != 3 {
		t.Fatalf("global admission count=%d", len(accepted))
	}
	for account, count := range counts {
		if count > 2 {
			t.Fatalf("%s exceeded account cap: %d", account, count)
		}
	}
	for _, r := range accepted {
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(release func()) { defer wg.Done(); release() }(r.release)
		}
	}
	wg.Wait()
	var next []func()
	for i := 0; i < 3; i++ {
		release, ok := pool.Acquire(fmt.Sprintf("new-%d", i))
		if !ok {
			t.Fatal("released slot not reusable")
		}
		next = append(next, release)
	}
	for _, r := range accepted {
		r.release()
	}
	if _, ok := pool.Acquire("overflow"); ok {
		t.Fatal("old duplicate releases created capacity in a new admission generation")
	}
	for _, release := range next {
		release()
	}
}
