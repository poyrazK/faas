package journeys

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestGuestGuarantees checks, from inside a running app, the promises the
// platform makes about every microVM (CLAUDE.md security rules and §6.2
// invariant 5): tenant egress is refused to metadata, private ranges, the
// host link and mail ports; every wake restored from a snapshot reseeds its
// RNG and steps its clock; and a burst of requests to a parked app is served
// by one wake. It also measures wake latency.
func TestGuestGuarantees(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	slug, base := tg.deployProbe(t, "guest")
	s := newStory(t)

	s.check("egress_policy", func(t *testing.T) {
		r := get(base + "/egress")
		if r.status != http.StatusOK {
			t.Fatalf("GET /egress: %s", r)
		}
		doc := jsonBody(t, r)
		names := make([]string, 0, len(doc))
		for name := range doc {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			connected, _ := field(doc, name+".connected").(bool)
			t.Logf("egress %-14s %-32s connected=%-5v %s", name, str(doc, name+".target"), connected, str(doc, name+".error"))
			switch {
			case name == "public_https" && !connected:
				t.Errorf("public HTTPS from the guest failed (%s): deny results below prove nothing", str(doc, name+".error"))
			case name != "public_https" && connected:
				t.Errorf("tenant egress reached %s (%s); it must be refused", name, str(doc, name+".target"))
			}
		}
	})

	wakes := 3
	if tg.tier == tierFull {
		wakes = 10
	}
	s.check("restore_entropy_clock_latency", func(t *testing.T) {
		warm := make([]time.Duration, 0, 5)
		for range 5 {
			r := get(base + "/entropy")
			if r.status != http.StatusOK {
				t.Fatalf("warm request: %s", r)
			}
			warm = append(warm, r.elapsed)
		}
		warmMedian := percentile(warm, 50)
		seen := map[string]int{}
		pids := map[string]bool{}
		var firstByte []time.Duration
		var maxSkew time.Duration
		for i := range wakes {
			tg.ok(t, 2*time.Minute, "park", slug)
			waitParked(t, tg, slug)
			sent := time.Now()
			r := get(base + "/entropy")
			if r.status != http.StatusOK {
				t.Errorf("wake %d: %s", i+1, r)
				continue
			}
			doc := jsonBody(t, r)
			firstByte = append(firstByte, r.elapsed)
			if prev, dup := seen[str(doc, "random")]; dup {
				t.Errorf("wake %d returned the same random bytes as wake %d: the restored RNG was not reseeded", i+1, prev)
			}
			seen[str(doc, "random")] = i + 1
			pids[str(doc, "pid")] = true
			// The guest stamped now_ms while answering; compare with the
			// local clock at the midpoint of the request.
			mid := sent.Add(r.elapsed / 2)
			skew := time.Duration(int64(num(doc, "now_ms"))*int64(time.Millisecond) - mid.UnixNano())
			if skew < 0 {
				skew = -skew
			}
			maxSkew = max(maxSkew, skew)
		}
		if len(firstByte) == 0 {
			t.Fatal("no wake answered")
		}
		t.Logf("%d wakes served by %d distinct guest process(es); one process means restores from one snapshot", len(firstByte), len(pids))
		t.Logf("metric wake_p50_ms=%d", percentile(firstByte, 50).Milliseconds())
		t.Logf("metric wake_p95_ms=%d", percentile(firstByte, 95).Milliseconds())
		t.Logf("metric warm_request_p50_ms=%d", warmMedian.Milliseconds())
		t.Logf("metric wake_overhead_p50_ms=%d", (percentile(firstByte, 50) - warmMedian).Milliseconds())
		t.Logf("metric clock_skew_max_ms=%d", maxSkew.Milliseconds())
		// The resume hook steps the clock before readiness; allow for this
		// machine's own clock error and request jitter.
		if maxSkew > 3*time.Second {
			t.Errorf("a restored guest's clock was off by %s: the resume hook did not step it", maxSkew)
		}
	})

	s.check("burst_wake", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "park", slug)
		waitParked(t, tg, slug)
		const burst = 20
		var mu sync.Mutex
		codes := map[string]int{}
		var wg sync.WaitGroup
		for range burst {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := get(base + "/")
				code := "200"
				if r.status != http.StatusOK {
					code = failureCode(r)
				}
				mu.Lock()
				codes[code]++
				mu.Unlock()
			}()
		}
		wg.Wait()
		t.Logf("burst of %d requests to a parked app: %v", burst, codes)
		if codes["200"] != burst {
			t.Errorf("%d of %d requests to a parked app were not served: %v", burst-codes["200"], burst, codes)
		}
		rows := objects(t, tg.ok(t, time.Minute, "ps", slug).stdout)
		hosts := make([]string, 0, len(rows))
		for _, row := range rows {
			hosts = append(hosts, str(row, "host_ip"))
		}
		t.Logf("after the burst ps lists %d instance(s) on %s", len(rows), strings.Join(hosts, ", "))
		if len(rows) > 1 {
			t.Errorf("one burst woke %d instances of a max_concurrency=1 app", len(rows))
		}
	})
}

// percentile returns the p-th percentile (nearest rank) of ds.
func percentile(ds []time.Duration, p int) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := (p*len(sorted) + 99) / 100
	return sorted[min(max(rank, 1), len(sorted))-1]
}
