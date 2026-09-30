package udpd

import (
	"context"
	"errors"
	"net/netip"
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
