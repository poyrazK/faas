// Package udpd contains public UDP peer admission and session primitives.
package udpd

import (
	"context"
	"errors"
	"net/netip"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/udpwire"
)

// Reply retains the originating peer identity across a shared socket queue.
// A writer must check Context before sending an already queued reply.
type Reply struct {
	Context context.Context
	Address netip.AddrPort
	Payload []byte
}

// Peer owns a bounded inbound queue for one endpoint/client pair. The socket
// owner keeps the endpoint identity and only passes this peer its datagrams.
type Peer struct {
	ctx     context.Context
	cancel  context.CancelFunc
	address netip.AddrPort
	inbound chan []byte
	replies chan<- Reply
}

func NewPeer(parent context.Context, address netip.AddrPort, replies chan<- Reply) (*Peer, error) {
	if parent == nil || !address.IsValid() || address.Port() == 0 || replies == nil {
		return nil, errors.New("UDP peer requires context, client address and reply queue")
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	return &Peer{ctx: ctx, cancel: cancel, address: address, inbound: make(chan []byte, api.UDPPeerQueueDepth), replies: replies}, nil
}

// Enqueue transfers a copy into the peer queue; full or closed queues drop
// without blocking the listener. Empty datagrams retain their presence.
func (p *Peer) Enqueue(payload []byte) bool {
	if len(payload) > api.UDPDatagramMaxBytes || p.ctx.Err() != nil {
		return false
	}
	copied := append([]byte(nil), payload...)
	select {
	case p.inbound <- copied:
		return true
	case <-p.ctx.Done():
		return false
	default:
		return false
	}
}
func (p *Peer) Receive(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.ctx.Done():
		return nil, p.ctx.Err()
	case payload := <-p.inbound:
		return payload, nil
	}
}
func (p *Peer) Send(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(payload) > api.UDPDatagramMaxBytes {
		return udpwire.ErrDatagramTooLarge
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	reply := Reply{Context: p.ctx, Address: p.address, Payload: append([]byte(nil), payload...)}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return p.ctx.Err()
	case p.replies <- reply:
		return nil
	}
}
func (p *Peer) Close() { p.cancel() }

// PeerPool bounds total and account-scoped sessions across all UDP listeners
// using this pool. Admission must happen before allocating a peer queue.
type PeerPool struct {
	mu                 sync.Mutex
	total              int
	accounts           map[string]int
	max, maxPerAccount int
}

func NewPeerPool(max, maxPerAccount int) *PeerPool {
	if max <= 0 {
		max = api.UDPMaxPeersDefault
	}
	if maxPerAccount <= 0 {
		maxPerAccount = api.UDPMaxPeersPerAccountDefault
	}
	return &PeerPool{max: max, maxPerAccount: maxPerAccount, accounts: make(map[string]int)}
}
func (p *PeerPool) Acquire(account string) (func(), bool) {
	if p == nil || account == "" {
		return nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.total >= p.max || p.accounts[account] >= p.maxPerAccount {
		return nil, false
	}
	p.total++
	p.accounts[account]++
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.total--
			p.accounts[account]--
			if p.accounts[account] == 0 {
				delete(p.accounts, account)
			}
		})
	}, true
}
