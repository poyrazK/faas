// cmd/apid/dns_probes_test.go — tests for the doctor probe
// engine (ADR-120, issue #961 follow-on).
//
// Coverage mirrors dns_verify_test.go (PR-3) — each probe
// has a positive + negative case via the package-level
// seams (aLookupFunc, aaaaLookupFunc, caaLookupFunc,
// appsDomainFunc), plus table-driven tests for the
// parseCAARecords helper and the runProbesParallel fan-out.

package apidserver

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// withSeams saves and restores the four test seams the
// probes depend on. Saves the boilerplate at every test.
//
// appsDomainFunc is ALWAYS assigned (not just when apps != "")
// because the production wire in newServerWithDeps
// (cmd/apid/server.go) sets appsDomainFunc to the server's
// domain — a previous test that booted a server leaves the
// seam pointing at a non-empty value, so a test that wants
// "apps domain unset" must explicitly set it to "".
func withSeams(t *testing.T, a, aaaa, caa func(ctx context.Context, domain string) ([]string, error), apps string) {
	t.Helper()
	prevA, prevAAAA, prevCAA, prevApps := aLookupFunc, aaaaLookupFunc, caaLookupFunc, appsDomainFunc
	t.Cleanup(func() {
		aLookupFunc, aaaaLookupFunc, caaLookupFunc, appsDomainFunc = prevA, prevAAAA, prevCAA, prevApps
	})
	if a != nil {
		aLookupFunc = a
	}
	if aaaa != nil {
		aaaaLookupFunc = aaaa
	}
	if caa != nil {
		caaLookupFunc = caa
	}
	appsDomainFunc = func() string { return apps }
}

func TestCheckApexA_AAAA_OK(t *testing.T) {
	withSeams(t,
		func(_ context.Context, _ string) ([]string, error) { return []string{"1.2.3.4"}, nil },
		func(_ context.Context, _ string) ([]string, error) { return nil, &net.DNSError{Err: "no such host"} },
		nil, "")
	res := checkApexA_AAAA(context.Background(), "api.example.com")
	if res.Status != probeOK {
		t.Fatalf("status = %q, want ok; detail=%q", res.Status, res.Detail)
	}
	if res.Observed != "1.2.3.4" {
		t.Errorf("observed = %q, want 1.2.3.4", res.Observed)
	}
}

func TestCheckApexA_AAAA_BothPresent(t *testing.T) {
	withSeams(t,
		func(_ context.Context, _ string) ([]string, error) { return []string{"1.2.3.4"}, nil },
		func(_ context.Context, _ string) ([]string, error) { return []string{"::1"}, nil },
		nil, "")
	res := checkApexA_AAAA(context.Background(), "api.example.com")
	if res.Status != probeOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	if !strings.Contains(res.Observed, "1.2.3.4") || !strings.Contains(res.Observed, "::1") {
		t.Errorf("observed = %q, want both A and AAAA", res.Observed)
	}
}

func TestCheckApexA_AAAA_NXDOMAIN(t *testing.T) {
	withSeams(t,
		func(_ context.Context, _ string) ([]string, error) {
			return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
		},
		func(_ context.Context, _ string) ([]string, error) {
			return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
		},
		nil, "")
	res := checkApexA_AAAA(context.Background(), "api.example.com")
	if res.Status != probeFail {
		t.Fatalf("status = %q, want fail", res.Status)
	}
	if res.Remediation == "" {
		t.Errorf("remediation empty; want a hint to publish A/AAAA")
	}
}

func TestCheckApexA_AAAA_Transient(t *testing.T) {
	withSeams(t,
		func(_ context.Context, _ string) ([]string, error) { return nil, &net.DNSError{Err: "timeout"} },
		func(_ context.Context, _ string) ([]string, error) { return nil, &net.DNSError{Err: "timeout"} },
		nil, "")
	res := checkApexA_AAAA(context.Background(), "api.example.com")
	if res.Status != probePending {
		t.Fatalf("status = %q, want pending (transient resolver error)", res.Status)
	}
}

func TestCheckPointsToGregale_OK(t *testing.T) {
	withSeams(t, nil, nil, nil, "edge.gregale.dev")
	prevCNAME := cnameLookupFunc
	t.Cleanup(func() { cnameLookupFunc = prevCNAME })
	cnameLookupFunc = func(_ context.Context, _ string) (string, error) {
		return "edge.gregale.dev.", nil
	}
	res := checkPointsToGregale(context.Background(), "api.example.com")
	if res.Status != probeOK {
		t.Fatalf("status = %q, want ok; detail=%q", res.Status, res.Detail)
	}
}

// adr: 20261026100000 — a custom domain whose CNAME names the apex of an
// earlier apps domain (still owned, still routed) is not drifted.
func TestCheckPointsToGregale_OwnedApexAfterMove(t *testing.T) {
	withSeams(t, nil, nil, nil, "gregale.app")
	prevCNAME, prevOwned := cnameLookupFunc, ownedDomainsFunc
	t.Cleanup(func() { cnameLookupFunc, ownedDomainsFunc = prevCNAME, prevOwned })
	ownedDomainsFunc = func() []string { return []string{"gregale.dev", "gregale.app"} }
	for cname, want := range map[string]probeStatus{
		"gregale.dev.":       probeOK,
		"Gregale.App.":       probeOK,
		"shop.gregale.dev.":  probeFail,
		"gregale.dev.evil.":  probeFail,
		"wrong.example.com.": probeFail,
	} {
		cnameLookupFunc = func(context.Context, string) (string, error) { return cname, nil }
		if res := checkPointsToGregale(context.Background(), "www.customer.example"); res.Status != want {
			t.Errorf("CNAME %s: status = %q, want %q (%s)", cname, res.Status, want, res.Detail)
		}
	}
}

func TestCheckPointsToGregale_Mismatch(t *testing.T) {
	withSeams(t, nil, nil, nil, "edge.gregale.dev")
	prevCNAME := cnameLookupFunc
	t.Cleanup(func() { cnameLookupFunc = prevCNAME })
	cnameLookupFunc = func(_ context.Context, _ string) (string, error) {
		return "wrong.example.com.", nil
	}
	res := checkPointsToGregale(context.Background(), "api.example.com")
	if res.Status != probeFail {
		t.Fatalf("status = %q, want fail", res.Status)
	}
	if !strings.Contains(res.Remediation, "Set CNAME") {
		t.Errorf("remediation = %q, want 'Set CNAME ... → edge.gregale.dev'", res.Remediation)
	}
}

func TestCheckPointsToGregale_NoCNAME(t *testing.T) {
	withSeams(t, nil, nil, nil, "edge.gregale.dev")
	prevCNAME := cnameLookupFunc
	t.Cleanup(func() { cnameLookupFunc = prevCNAME })
	cnameLookupFunc = func(_ context.Context, _ string) (string, error) {
		return "", &net.DNSError{Err: "no such host", IsNotFound: true}
	}
	res := checkPointsToGregale(context.Background(), "api.example.com")
	if res.Status != probeNA {
		t.Fatalf("status = %q, want na", res.Status)
	}
}

func TestCheckPointsToGregale_AppsDomainUnset(t *testing.T) {
	withSeams(t, nil, nil, nil, "")
	res := checkPointsToGregale(context.Background(), "api.example.com")
	if res.Status != probePending {
		t.Fatalf("status = %q, want pending", res.Status)
	}
}

func TestCheckCAA_NoCAA(t *testing.T) {
	withSeams(t, nil, nil,
		func(_ context.Context, _ string) ([]string, error) {
			return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
		},
		"")
	res := checkCAA(context.Background(), "api.example.com")
	if res.Status != probeOK {
		t.Fatalf("status = %q, want ok (no CAA = permitted by default)", res.Status)
	}
	if res.Detail == "" {
		t.Errorf("detail empty; want 'no CAA published' explanation")
	}
}

func TestCheckCAA_Permits(t *testing.T) {
	withSeams(t, nil, nil,
		func(_ context.Context, _ string) ([]string, error) { return []string{`0 issue "letsencrypt.org"`}, nil },
		"")
	res := checkCAA(context.Background(), "api.example.com")
	if res.Status != probeOK {
		t.Fatalf("status = %q, want ok; detail=%q", res.Status, res.Detail)
	}
}

func TestCheckCAA_DenyAll(t *testing.T) {
	withSeams(t, nil, nil,
		func(_ context.Context, _ string) ([]string, error) { return []string{`0 issue ";"`}, nil },
		"")
	res := checkCAA(context.Background(), "api.example.com")
	if res.Status != probeFail {
		t.Fatalf("status = %q, want fail", res.Status)
	}
	if !strings.Contains(res.Remediation, "Update CAA") {
		t.Errorf("remediation = %q, want 'Update CAA ...'", res.Remediation)
	}
}

func TestCheckAAAAConflict_OK(t *testing.T) {
	withSeams(t, nil,
		func(_ context.Context, _ string) ([]string, error) {
			return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
		},
		nil, "")
	res := checkAAAAConflict(context.Background(), "api.example.com")
	if res.Status != probeOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
}

func TestCheckAAAAConflict_StrayAAAA(t *testing.T) {
	withSeams(t, nil,
		func(_ context.Context, _ string) ([]string, error) { return []string{"2001:db8::1"}, nil },
		nil, "")
	res := checkAAAAConflict(context.Background(), "api.example.com")
	if res.Status != probeFail {
		t.Fatalf("status = %q, want fail", res.Status)
	}
	if res.Observed != "2001:db8::1" {
		t.Errorf("observed = %q, want 2001:db8::1", res.Observed)
	}
	if !strings.Contains(res.Remediation, "Remove AAAA") {
		t.Errorf("remediation = %q, want 'Remove AAAA record at ...'", res.Remediation)
	}
}

func TestParseCAARecords(t *testing.T) {
	cases := []struct {
		name     string
		records  []string
		permits  bool
		hasObsrv string
	}{
		{"letsencrypt permitted", []string{`0 issue "letsencrypt.org"`}, true, `0 issue "letsencrypt.org"`},
		{"deny all wins", []string{`0 issue ";"`}, false, `0 issue ";"`},
		{"issuewild permitted", []string{`0 issuewild "letsencrypt.org"`}, true, `0 issuewild "letsencrypt.org"`},
		{"empty value is no-op", []string{`0 issue ""`}, false, `0 issue ""`},
		{"non-recognised tag", []string{`0 iodef "mailto:sec@example.com"`}, false, `0 iodef "mailto:sec@example.com"`},
		{"quoted with whitespace", []string{`  0   issue    "letsencrypt.org"  `}, true, `0   issue    "letsencrypt.org"`},
		{"unrelated CAA wins on empty value", []string{`0 issue ";"`, `0 issuewild "letsencrypt.org"`}, true, ""}, // issuewild permits despite issue deny
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			permits, observed := parseCAARecords(c.records)
			if permits != c.permits {
				t.Errorf("permits = %v, want %v", permits, c.permits)
			}
			if c.hasObsrv != "" {
				found := false
				for _, o := range observed {
					if strings.Contains(o, c.hasObsrv) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("observed = %v, want one containing %q", observed, c.hasObsrv)
				}
			}
		})
	}
}

func TestRunProbesParallel_FanOut(t *testing.T) {
	withSeams(t,
		func(_ context.Context, _ string) ([]string, error) { return []string{"1.2.3.4"}, nil },
		func(_ context.Context, _ string) ([]string, error) {
			return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
		},
		func(_ context.Context, _ string) ([]string, error) {
			return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
		},
		"edge.gregale.dev")
	prevCNAME := cnameLookupFunc
	t.Cleanup(func() { cnameLookupFunc = prevCNAME })
	cnameLookupFunc = func(_ context.Context, _ string) (string, error) {
		return "edge.gregale.dev.", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dnsFound, pointsToG, caa, aaaa := runProbesParallel(ctx, "api.example.com")
	if dnsFound.Status != probeOK {
		t.Errorf("dnsFound.status = %q, want ok", dnsFound.Status)
	}
	if pointsToG.Status != probeOK {
		t.Errorf("pointsToG.status = %q, want ok", pointsToG.Status)
	}
	if caa.Status != probeOK {
		t.Errorf("caa.status = %q, want ok", caa.Status)
	}
	if aaaa.Status != probeOK {
		t.Errorf("aaaa.status = %q, want ok", aaaa.Status)
	}
}
