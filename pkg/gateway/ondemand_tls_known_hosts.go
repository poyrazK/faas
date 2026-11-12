package gateway

import (
	"context"
	"hash/maphash"
	"slices"
	"strings"
	"time"

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
	// Once an inventory has loaded, refresh it in the background. Handshakes
	// can continue using the previous hints while a page read is slow; hints
	// never replace the authoritative ownership and admission checks.
	p.mu.Lock()
	hasHints := p.hints != nil
	if hasHints && (p.hintsRefreshing || p.now().Before(p.hintsNextLoad)) {
		p.mu.Unlock()
		return nil
	}
	if hasHints {
		p.hintsRefreshing = true
	}
	p.mu.Unlock()
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
			for _, host := range p.recentHosts {
				host = normalizeTLSHost(host)
				if !strings.HasPrefix(host, "*.") {
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
	if hasHints {
		go func() {
			<-result
			p.mu.Lock()
			p.hintsRefreshing = false
			p.mu.Unlock()
		}()
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case loaded := <-result:
		return loaded.Err
	}
}

// catchUpRecentTLSHosts repairs names admitted by another edge after the
// inventory cursor passed them. A single shared, rate-limited ledger read
// bounds the extra database work even under a flood of unknown names.
type recentTLSHostLoad struct {
	done    chan struct{}
	started time.Time
}

func (p *OnDemandTLSPolicy) catchUpRecentTLSHosts(ctx context.Context, host string) bool {
	reader, ok := p.store.(state.CustomDomainTLSRecentHostReader)
	if !ok {
		return false
	}
	arrived := time.Now()
	p.mu.Lock()
	if p.recentWaiters >= api.OnDemandTLSAskRecentHostWaiters {
		p.mu.Unlock()
		return false
	}
	p.recentWaiters++
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.recentWaiters--
		p.mu.Unlock()
	}()
	for range 2 {
		p.mu.Lock()
		p.clearRecentWildcardMiss(host)
		if _, denied := p.unknown[host]; !denied && p.hints.contains(host) {
			p.mu.Unlock()
			return true
		}
		loading := p.recentLoading
		if loading == nil {
			loading = &recentTLSHostLoad{done: make(chan struct{})}
			p.recentLoading = loading
			go p.loadRecentTLSHosts(context.WithoutCancel(ctx), reader, loading)
		}
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-loading.done:
		}
		p.mu.Lock()
		p.clearRecentWildcardMiss(host)
		known := p.hints.contains(host)
		started := loading.started
		p.mu.Unlock()
		if known || !started.Before(arrived) {
			return true
		}
	}
	return true
}

func (p *OnDemandTLSPolicy) loadRecentTLSHosts(ctx context.Context, reader state.CustomDomainTLSRecentHostReader, loading *recentTLSHostLoad) {
	ctx, cancel := context.WithTimeout(ctx, onDemandTLSLookupTimeout+api.OnDemandTLSAskRecentHostRetry)
	defer cancel()
	p.mu.Lock()
	delay := time.Until(p.recentNext)
	p.mu.Unlock()
	var err error
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-timer.C:
		}
	}
	var hosts []string
	if err == nil {
		p.mu.Lock()
		loading.started = time.Now()
		p.recentNext = loading.started.Add(api.OnDemandTLSAskRecentHostRetry)
		p.mu.Unlock()
		readCtx, stop := context.WithTimeout(ctx, onDemandTLSLookupTimeout)
		hosts, err = reader.ListRecentCustomDomainTLSHosts(readCtx, api.OnDemandTLSAskRecentHostRows)
		stop()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err == nil {
		p.recentHosts = slices.Clone(hosts)
		p.recentWildcards = make(map[string]struct{})
		if p.hints != nil {
			for _, host := range hosts {
				host = normalizeTLSHost(host)
				if strings.HasPrefix(host, "*.") {
					p.recentWildcards[host[2:]] = struct{}{}
					continue
				}
				p.hints.add(host)
				delete(p.unknown, host)
			}
		}
	} else {
		p.log.Warn("gateway: on-demand tls recent-host catch-up failed", "err", err)
	}
	p.recentLoading = nil
	close(loading.done)
}

// Caller holds p.mu. Removing a stale miss does not grant admission: the
// concrete name still spends the normal lookup budget and is authorized live.
func (p *OnDemandTLSPolicy) clearRecentWildcardMiss(host string) {
	if _, denied := p.unknown[host]; !denied {
		return
	}
	for suffix := host; ; {
		i := strings.IndexByte(suffix, '.')
		if i < 0 {
			return
		}
		suffix = suffix[i+1:]
		if _, ok := p.recentWildcards[suffix]; ok {
			delete(p.unknown, host)
			return
		}
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
