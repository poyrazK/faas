package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeBotResolver struct {
	ptr     map[string][]string
	fwd     map[string][]string
	ptrErr  error
	ptrHits atomic.Int32
	block   chan struct{}
}

func (f *fakeBotResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	f.ptrHits.Add(1)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.ptrErr != nil {
		return nil, f.ptrErr
	}
	names, ok := f.ptr[addr]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: addr, IsNotFound: true}
	}
	return names, nil
}

func (f *fakeBotResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	var out []net.IPAddr
	for _, a := range f.fwd[host] {
		out = append(out, net.IPAddr{IP: net.ParseIP(a)})
	}
	if len(out) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return out, nil
}

const googlebotUA = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"

func googlebotResolver() *fakeBotResolver {
	return &fakeBotResolver{
		ptr: map[string][]string{"66.249.66.1": {"crawl.googlebot.com."}},
		fwd: map[string][]string{"crawl.googlebot.com": {"66.249.66.1"}},
	}
}

// adr: 968 — a claimed crawler verifies only by forward-confirmed reverse
// DNS into its own domain; spoofed names, mismatched forward records and
// non-claims never verify.
func TestBotVerifierForwardConfirmedReverseDNS(t *testing.T) {
	r := &fakeBotResolver{
		ptr: map[string][]string{
			"66.249.66.1":  {"crawl-66-249-66-1.googlebot.com."},
			"203.0.113.5":  {"crawl.googlebot.com.attacker.example."},
			"203.0.113.6":  {"fake.googlebot.com."},
			"198.51.100.7": {"googlebot.com."},
		},
		fwd: map[string][]string{
			"crawl-66-249-66-1.googlebot.com": {"66.249.66.1"},
			"fake.googlebot.com":              {"66.249.66.99"},
			"googlebot.com":                   {"198.51.100.7"},
		},
	}
	v := NewBotVerifier(r)
	for _, tc := range []struct {
		name, ip, ua, want string
	}{
		{"verified", "66.249.66.1", googlebotUA, "googlebot"},
		{"no claim", "66.249.66.1", "curl/8.7.1", ""},
		{"wrong domain for claim", "66.249.66.1", "Mozilla/5.0 (compatible; bingbot/2.0)", ""},
		{"suffix spoof", "203.0.113.5", googlebotUA, ""},
		{"forward mismatch", "203.0.113.6", googlebotUA, ""},
		{"bare apex", "198.51.100.7", googlebotUA, ""},
		{"no PTR", "192.0.2.1", googlebotUA, ""},
	} {
		if got := v.VerifiedBot(context.Background(), net.ParseIP(tc.ip), tc.ua); got != tc.want {
			t.Errorf("%s: VerifiedBot(%s) = %q, want %q", tc.name, tc.ip, got, tc.want)
		}
	}
	if got := v.VerifiedBot(context.Background(), nil, googlebotUA); got != "" {
		t.Errorf("nil IP verified as %q", got)
	}
}

// adr: 968 — verdicts are cached per IP and bot until their TTL; transient
// DNS failures are not cached and fail closed.
func TestBotVerifierCaching(t *testing.T) {
	r := googlebotResolver()
	v := NewBotVerifier(r)
	now := time.Unix(1_700_000_000, 0)
	v.now = func() time.Time { return now }
	ip := net.ParseIP("66.249.66.1")
	for i := 0; i < 3; i++ {
		if v.VerifiedBot(context.Background(), ip, googlebotUA) != "googlebot" {
			t.Fatal("want verified")
		}
	}
	if n := r.ptrHits.Load(); n != 1 {
		t.Fatalf("PTR lookups = %d, want 1 (cached)", n)
	}
	now = now.Add(2 * time.Hour)
	v.VerifiedBot(context.Background(), ip, googlebotUA)
	if n := r.ptrHits.Load(); n != 2 {
		t.Fatalf("PTR lookups after expiry = %d, want 2", n)
	}

	flaky := &fakeBotResolver{ptrErr: &net.DNSError{Err: "server misbehaving", IsTemporary: true}}
	fv := NewBotVerifier(flaky)
	for i := 0; i < 2; i++ {
		if fv.VerifiedBot(context.Background(), ip, googlebotUA) != "" {
			t.Fatal("transient failure must not verify")
		}
	}
	if n := flaky.ptrHits.Load(); n != 2 {
		t.Fatalf("transient failure cached: PTR lookups = %d, want 2", n)
	}
	if NewBotVerifier(&fakeBotResolver{ptrErr: errors.New("boom")}).VerifiedBot(context.Background(), ip, googlebotUA) != "" {
		t.Fatal("resolver error must fail closed")
	}
}

// adr: 968 — concurrent requests for one IP share a lookup, and a spent
// in-flight budget fails closed instead of queueing.
func TestBotVerifierConcurrency(t *testing.T) {
	r := googlebotResolver()
	r.block = make(chan struct{})
	v := NewBotVerifier(r)
	ip := net.ParseIP("66.249.66.1")
	var wg sync.WaitGroup
	results := make([]string, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = v.VerifiedBot(context.Background(), ip, googlebotUA)
		}(i)
	}
	deadline := time.Now().Add(2 * time.Second)
	for r.ptrHits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	// Fill the remaining budget so a different IP cannot start a lookup.
	for len(v.inFlight) < cap(v.inFlight) {
		v.inFlight <- struct{}{}
	}
	if got := v.VerifiedBot(context.Background(), net.ParseIP("66.249.66.2"), googlebotUA); got != "" {
		t.Fatalf("budget spent but verified %q", got)
	}
	for len(v.inFlight) > 1 {
		<-v.inFlight
	}
	close(r.block)
	wg.Wait()
	for i, got := range results {
		if got != "googlebot" {
			t.Errorf("caller %d = %q", i, got)
		}
	}
	if n := r.ptrHits.Load(); n != 1 {
		t.Fatalf("PTR lookups = %d, want 1 shared", n)
	}
}

// adr: 968 — the gateway resolves verified_bot only for rules that read it,
// at most once per request.
func TestApplicableEdgeRulesVerifiedBotCondition(t *testing.T) {
	r := googlebotResolver()
	h := (&Handler{}).WithBotVerifier(NewBotVerifier(r))
	account := func(r *EdgeRuleResolved) string { return r.AccountID }
	matches := func(rules []EdgeRuleResolved, ip, ua string) bool {
		req := httptest.NewRequest(http.MethodGet, "http://api.example.com/", nil)
		req.Header.Set("User-Agent", ua)
		m := NewEdgeRuleMatchContext(req, net.ParseIP(ip), nil, nil)
		m.SetBotVerifier(context.Background(), h.botVerifier)
		ctx := WithEdgeRuleMatchContext(WithEdgeRuleOwner(context.Background(), "acct"), m)
		first := len(ApplicableEdgeRules(ctx, rules, account, "/", http.MethodGet)) == 1
		if again := len(ApplicableEdgeRules(ctx, rules, account, "/", http.MethodGet)) == 1; again != first {
			t.Fatal("repeated evaluation disagrees")
		}
		return first
	}
	fakeBots := []EdgeRuleResolved{{ID: "fake-bots", AccountID: "acct", TargetAppSlug: "x",
		EdgeRuleCondition: conditionFromJSON(t, `{"all":[{"field":"ua_family","op":"eq","value":"bot"},{"field":"verified_bot","op":"missing"}]}`)}}
	if matches(fakeBots, "66.249.66.1", googlebotUA) {
		t.Fatal("real Googlebot must not match the fake-bot rule")
	}
	if !matches(fakeBots, "203.0.113.9", googlebotUA) {
		t.Fatal("spoofed Googlebot must match the fake-bot rule")
	}
	if n := r.ptrHits.Load(); n != 2 {
		t.Fatalf("PTR lookups = %d, want 2 (one per request)", n)
	}
	uaOnly := []EdgeRuleResolved{{ID: "bots", AccountID: "acct", TargetAppSlug: "x",
		EdgeRuleCondition: conditionFromJSON(t, `{"field":"ua_family","op":"eq","value":"bot"}`)}}
	if !matches(uaOnly, "203.0.113.10", googlebotUA) {
		t.Fatal("ua_family=bot must match a Googlebot UA")
	}
	if n := r.ptrHits.Load(); n != 2 {
		t.Fatal("a rule without verified_bot triggered DNS")
	}
}
