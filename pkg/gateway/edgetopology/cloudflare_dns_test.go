package edgetopology

// adr: 704

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const dnsTestToken = "private-cloudflare-test-token-never-in-output"

func dnsTestZone() CloudflareZoneRef {
	return CloudflareZoneRef{ID: strings.Repeat("a", 32), Name: "gregale.dev"}
}

func dnsZoneBody() map[string]any {
	z := dnsTestZone()
	return map[string]any{"id": z.ID, "name": z.Name, "status": "active", "type": "full", "paused": false, "name_servers": []string{"two.ns.cloudflare.com", "one.ns.cloudflare.com"}, "account": map[string]string{"name": "secret-account-metadata"}}
}

func dnsRow(i int) map[string]any {
	return map[string]any{"id": fmt.Sprintf("%032x", i+1), "name": fmt.Sprintf("app%d.gregale.dev", i), "type": "A", "content": "192.0.2.10", "proxied": true, "ttl": 1}
}

func dnsRows(n int) []map[string]any {
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = dnsRow(i)
	}
	return rows
}

func dnsEnvelope(result any) map[string]any {
	return map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": result}
}

func dnsPage(rows []map[string]any, page int) map[string]any {
	start := (page - 1) * api.RuntimeUpgradeDNSPageSize
	end := min(start+api.RuntimeUpgradeDNSPageSize, len(rows))
	result := dnsEnvelope(rows[start:end])
	result["result_info"] = map[string]any{"page": page, "per_page": api.RuntimeUpgradeDNSPageSize, "count": end - start, "total_count": len(rows), "total_pages": (len(rows) + api.RuntimeUpgradeDNSPageSize - 1) / api.RuntimeUpgradeDNSPageSize}
	return result
}

func dnsTestProbe(t *testing.T, handler http.HandlerFunc) *CloudflareDNSProbe {
	t.Helper()
	p, err := NewCloudflareDNSProbe(dnsTestZone(), dnsTestToken)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+dnsTestToken || r.Header.Get("Cache-Control") != "no-cache" {
			t.Error("unexpected provider method or private authentication")
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	p.baseURL = server.URL
	return p
}

func dnsServe(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(jsonConfig(t, body))
}

func dnsRequestPage(t *testing.T, r *http.Request) int {
	t.Helper()
	q := r.URL.Query()
	if len(q) != 5 || q.Get("order") != "name" || q.Get("direction") != "asc" || q.Get("include_shadow_metadata") != "true" || q.Get("per_page") != strconv.Itoa(api.RuntimeUpgradeDNSPageSize) || r.URL.Path != "/zones/"+dnsTestZone().ID+"/dns_records" {
		t.Error("record scan was filtered, discovered or redirected", r.URL)
	}
	page, err := strconv.Atoi(q.Get("page"))
	if err != nil || page < 1 {
		t.Fatal("bad requested page")
	}
	return page
}

func assertDNSFailure(t *testing.T, p *CloudflareDNSProbe) error {
	t.Helper()
	got, err := p.Collect(t.Context())
	if !errors.Is(err, ErrDNSUnverified) || got.ConfigSHA256 != "" || len(got.Records) != 0 || got.Zone.ID != "" || !got.CheckedAt.IsZero() {
		t.Fatal("failed collection produced partial success", got, err)
	}
	for _, secret := range []string{dnsTestToken, "secret-provider-body"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("provider error leaked private content")
		}
	}
	return err
}

func TestCloudflareDNSInventoryCollectsEveryPageAndRedactsOpaqueContent(t *testing.T) {
	rows := dnsRows(api.RuntimeUpgradeDNSPageSize + 8)
	rows[0]["name"] = "*.gregale.dev"
	rows[1]["name"], rows[1]["proxied"] = "origin.gregale.dev", false
	rows[2]["type"], rows[2]["content"] = "AAAA", "2001:db8::10"
	rows[3]["type"], rows[3]["content"] = "CNAME", "external.example.net."
	rows[4]["type"], rows[4]["content"], rows[4]["proxied"] = "NS", "ns.external.example.net", false
	for i, kind := range []string{"TXT", "HTTPS", "SVCB", "SRV", "FUTURE"} {
		row := rows[5+i]
		row["type"], row["content"], row["proxied"] = kind, "secret-opaque-record-content", false
		row["data"] = map[string]string{"target": "secret-opaque-service-target"}
	}
	rows[5]["name"] = "_faas-verify.gregale.dev"
	var zones, pages atomic.Int32
	p := dnsTestProbe(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zones/"+dnsTestZone().ID {
			zones.Add(1)
			dnsServe(t, w, dnsEnvelope(dnsZoneBody()))
			return
		}
		pages.Add(1)
		dnsServe(t, w, dnsPage(rows, dnsRequestPage(t, r)))
	})
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	var previous string
	for i := 0; i < 2; i++ {
		got, err := p.Collect(t.Context())
		if err != nil || len(got.Records) != len(rows) || got.Zone != dnsTestZone() || !canonicalDigest(got.ConfigSHA256) || got.CheckedAt.IsZero() || !slices.Equal(got.NameServers, []string{"one.ns.cloudflare.com", "two.ns.cloudflare.com"}) {
			t.Fatal("incomplete provider configuration inventory", got, err)
		}
		if previous != "" && got.ConfigSHA256 != previous {
			t.Fatal("same configuration changed digest")
		}
		previous = got.ConfigSHA256
		if got.Records[0].Name != "*.gregale.dev" || got.Records[1].Target != "192.0.2.10" || got.Records[1].Proxied == nil || *got.Records[1].Proxied || got.Records[2].Target != "2001:db8::10" || got.Records[3].Target != "external.example.net" || got.Records[4].Target != "ns.external.example.net" || got.Records[6].Type != "HTTPS" || got.Records[6].Target != "" {
			t.Fatal("lost wildcard, DNS-only origin, IPv6, alias, delegation or opaque service record")
		}
		encoded := string(jsonConfig(t, got))
		for _, secret := range []string{dnsTestToken, "secret-account-metadata", "secret-opaque-record-content", "secret-opaque-service-target"} {
			if strings.Contains(encoded, secret) {
				t.Fatal("inventory exposed private provider metadata or opaque content")
			}
		}
	}
	if zones.Load() != 4 || pages.Load() != 8 {
		t.Fatal("collector cached success or skipped complete scan", zones.Load(), pages.Load())
	}
}

func TestCloudflareDNSInventoryAcceptsSameCompleteSetInDifferentPageOrder(t *testing.T) {
	rows := dnsRows(api.RuntimeUpgradeDNSPageSize + 1)
	var scan int
	p := dnsTestProbe(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zones/"+dnsTestZone().ID {
			dnsServe(t, w, dnsEnvelope(dnsZoneBody()))
			return
		}
		page := dnsRequestPage(t, r)
		if page == 1 {
			scan++
		}
		ordered := slices.Clone(rows)
		if scan == 2 {
			slices.Reverse(ordered)
		}
		dnsServe(t, w, dnsPage(ordered, page))
	})
	if got, err := p.Collect(t.Context()); err != nil || len(got.Records) != len(rows) || got.Records[0].ID != rows[0]["id"] {
		t.Fatal("complete set changed only page order", got, err)
	}
}

func TestCloudflareDNSInventoryRejectsPaginationOmissionsAndAmbiguity(t *testing.T) {
	for _, mode := range []string{"duplicate-across-pages", "truncated-first", "truncated-last", "wrong-page", "wrong-size", "wrong-count", "wrong-pages", "missing-total", "null-total", "overflow", "changed-total"} {
		t.Run(mode, func(t *testing.T) {
			rows := dnsRows(api.RuntimeUpgradeDNSPageSize + 1)
			p := dnsTestProbe(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/zones/"+dnsTestZone().ID {
					dnsServe(t, w, dnsEnvelope(dnsZoneBody()))
					return
				}
				page := dnsRequestPage(t, r)
				body := dnsPage(rows, page)
				info := body["result_info"].(map[string]any)
				switch mode {
				case "duplicate-across-pages":
					if page == 2 {
						body["result"] = rows[:1]
					}
				case "truncated-first":
					if page == 1 {
						body["result"], info["count"] = rows[:1], 1
					}
				case "truncated-last":
					if page == 2 {
						body["result"], info["count"] = []any{}, 0
					}
				case "wrong-page":
					info["page"] = page + 1
				case "wrong-size":
					info["per_page"] = 1
				case "wrong-count":
					info["count"] = 0
				case "wrong-pages":
					info["total_pages"] = 1
				case "missing-total":
					delete(info, "total_count")
				case "null-total":
					info["total_count"] = nil
				case "overflow":
					info["total_count"] = api.RuntimeUpgradeDNSRecordLimit + 1
				case "changed-total":
					if page == 2 {
						info["total_count"] = len(rows) + 1
					}
				}
				dnsServe(t, w, body)
			})
			assertDNSFailure(t, p)
		})
	}
}

func TestCloudflareDNSInventoryRejectsChangesInOpaqueRecordsAndZoneMetadata(t *testing.T) {
	for _, mode := range []string{"origin-address", "proxy-state", "opaque-content", "record-metadata", "zone-metadata", "record-deleted"} {
		t.Run(mode, func(t *testing.T) {
			rows := dnsRows(api.RuntimeUpgradeDNSPageSize + 1)
			rows[len(rows)-1]["type"], rows[len(rows)-1]["content"] = "TXT", "secret-before"
			var scans, zones int
			p := dnsTestProbe(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/zones/"+dnsTestZone().ID {
					zones++
					zone := dnsZoneBody()
					if mode == "zone-metadata" && zones == 2 {
						zone["opaque"] = "changed"
					}
					dnsServe(t, w, dnsEnvelope(zone))
					return
				}
				page := dnsRequestPage(t, r)
				if page == 1 {
					scans++
					if scans == 2 {
						switch mode {
						case "origin-address":
							rows[0]["content"] = "192.0.2.11"
						case "proxy-state":
							rows[0]["proxied"] = false
						case "opaque-content":
							rows[len(rows)-1]["content"] = "secret-after"
						case "record-metadata":
							rows[len(rows)-1]["unknown"] = "changed"
						case "record-deleted":
							rows = rows[:len(rows)-1]
						}
					}
				}
				dnsServe(t, w, dnsPage(rows, page))
			})
			assertDNSFailure(t, p)
		})
	}
}

func TestCloudflareDNSInventoryRejectsProviderFailuresWithoutLeakingBodies(t *testing.T) {
	for _, mode := range []string{"rate-limit", "redirect", "false-success", "errors", "null-result", "duplicate-key", "case-fold-duplicate", "trailing", "later-page", "second-scan"} {
		t.Run(mode, func(t *testing.T) {
			rows := dnsRows(api.RuntimeUpgradeDNSPageSize + 1)
			var scans int
			p := dnsTestProbe(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/zones/"+dnsTestZone().ID {
					dnsServe(t, w, dnsEnvelope(dnsZoneBody()))
					return
				}
				page := dnsRequestPage(t, r)
				if page == 1 {
					scans++
				}
				body := dnsPage(rows, page)
				switch mode {
				case "rate-limit", "redirect":
					w.Header().Set("Location", "http://127.0.0.1:1/private")
					status := http.StatusTooManyRequests
					if mode == "redirect" {
						status = http.StatusTemporaryRedirect
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte("secret-provider-body " + dnsTestToken))
					return
				case "false-success":
					body["success"] = false
				case "errors":
					body["errors"] = []any{map[string]string{"message": "secret-provider-body " + dnsTestToken}}
				case "null-result":
					body["result"] = nil
				case "duplicate-key", "case-fold-duplicate", "trailing":
					raw := string(jsonConfig(t, body))
					if mode == "trailing" {
						raw += ` {}`
					} else {
						key := "success"
						if mode == "case-fold-duplicate" {
							key = "SUCCESS"
						}
						raw = `{"` + key + `":true,` + raw[1:]
					}
					_, _ = w.Write([]byte(raw))
					return
				case "later-page":
					if page == 2 {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
				case "second-scan":
					if scans == 2 {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
				}
				dnsServe(t, w, body)
			})
			assertDNSFailure(t, p)
		})
	}
}

func TestCloudflareDNSInventoryCancellationReturnsNoPartialSuccess(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	p := dnsTestProbe(t, func(http.ResponseWriter, *http.Request) { close(started); <-release })
	defer close(release)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		got, err := p.Collect(ctx)
		if got.ConfigSHA256 != "" {
			t.Error("cancelled collection returned data")
		}
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrDNSUnverified) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider collection did not cancel")
	}
}

func TestCloudflareDNSInventoryEmptyZoneAndNoAliasDiscovery(t *testing.T) {
	for _, n := range []int{0, 1} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			rows := dnsRows(n)
			if n == 1 {
				rows[0]["type"], rows[0]["content"] = "CNAME", "outside.example.net"
			}
			var requests atomic.Int32
			p := dnsTestProbe(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/zones/"+dnsTestZone().ID {
					dnsServe(t, w, dnsEnvelope(dnsZoneBody()))
					return
				}
				dnsServe(t, w, dnsPage(rows, dnsRequestPage(t, r)))
			})
			got, err := p.Collect(t.Context())
			if err != nil || len(got.Records) != n || requests.Load() != 4 {
				t.Fatal(got, err, requests.Load())
			}
		})
	}
}
