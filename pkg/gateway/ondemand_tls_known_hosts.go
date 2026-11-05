package gateway

import (
	"context"
	"hash/maphash"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// tlsKnownHostHints is a bounded membership hint, never an authorization
// cache. False positives only cost the normal authoritative store reads.
// A process-local hash seed prevents clients from predicting collisions.
type tlsKnownHostHints struct {
	seed maphash.Seed
	bits []uint64
}

// A scan is private to the single-flight loader until every page is read.
// Retaining its cursor lets bounded retries finish a slow inventory scan.
type tlsKnownHostScan struct {
	hints *tlsKnownHostHints
	after string
}

func newTLSKnownHostHints() *tlsKnownHostHints {
	return &tlsKnownHostHints{seed: maphash.MakeSeed(), bits: make([]uint64, api.OnDemandTLSAskKnownHostHintBytes/8)}
}

func (h *tlsKnownHostHints) add(host string) {
	hash := maphash.String(h.seed, host)
	step := hash>>32 | 1
	for range 7 {
		bit := hash % (uint64(len(h.bits)) * 64)
		h.bits[bit/64] |= uint64(1) << (bit % 64)
		hash += step
	}
}

func (h *tlsKnownHostHints) contains(host string) bool {
	if h == nil {
		return false
	}
	hash := maphash.String(h.seed, host)
	step := hash>>32 | 1
	for range 7 {
		bit := hash % (uint64(len(h.bits)) * 64)
		if h.bits[bit/64]&(uint64(1)<<(bit%64)) == 0 {
			return false
		}
		hash += step
	}
	return true
}

// refreshKnownHostHints restores durable names before any lookup bucket can
// deny them. Paging bounds temporary allocations, and the filter's memory is
// independent of inventory size. Refresh failure retains a previous filter;
// an initial failure denies until a bounded retry succeeds.
func (p *OnDemandTLSPolicy) refreshKnownHostHints(ctx context.Context) error {
	reader, ok := p.store.(state.CustomDomainTLSKnownHostReader)
	if !ok {
		return nil // legacy test adapters; production stores assert this seam
	}
	if ready, err := p.knownHostHintsReady(); ready {
		return err
	}
	result := p.hintsLoad.DoChan("known", func() (any, error) {
		if ready, err := p.knownHostHintsReady(); ready {
			return nil, err
		}
		// One canceled handshake must not cancel another's shared warm-up.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), onDemandTLSLookupTimeout)
		defer cancel()
		p.mu.Lock()
		scan := p.hintsScan
		if scan == nil {
			scan = &tlsKnownHostScan{}
		}
		p.mu.Unlock()
		hints, err := loadTLSKnownHostHints(readCtx, reader, scan)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.hintsScan = scan
		p.hintsErr = err
		p.hintsNextLoad = p.now().Add(api.OnDemandTLSAskKnownHostRetry)
		if err == nil {
			// Admissions made locally while the scan ran must survive the swap.
			for host, until := range p.known {
				if p.now().Before(until) {
					hints.add(host)
				}
			}
			p.hints = hints
			p.hintsScan = nil
			p.hintsNextLoad = p.now().Add(api.OnDemandTLSAskKnownHostRefresh)
		} else {
			p.log.Warn("gateway: on-demand tls known-host hints refresh failed", "err", err)
		}
		if p.hints != nil {
			return nil, nil
		}
		return nil, err
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case loaded := <-result:
		return loaded.Err
	}
}

func (p *OnDemandTLSPolicy) knownHostHintsReady() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.now().Before(p.hintsNextLoad) {
		return false, nil
	}
	if p.hints != nil {
		return true, nil
	}
	return true, p.hintsErr
}

func loadTLSKnownHostHints(ctx context.Context, reader state.CustomDomainTLSKnownHostReader, scan *tlsKnownHostScan) (*tlsKnownHostHints, error) {
	if scan.hints == nil {
		scan.hints = newTLSKnownHostHints()
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hosts, err := reader.ListCustomDomainTLSKnownHosts(ctx, scan.after, api.OnDemandTLSAskKnownHostPageSize)
		if err != nil {
			return nil, err
		}
		for _, host := range hosts {
			scan.hints.add(normalizeTLSHost(host))
		}
		if len(hosts) < api.OnDemandTLSAskKnownHostPageSize {
			return scan.hints, nil
		}
		scan.after = hosts[len(hosts)-1]
	}
}
