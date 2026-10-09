package edgetopology

// adr: 704

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCloudflareDNSInventoryConstructorFreezesExplicitZoneAndPinsProductionAPI(t *testing.T) {
	zone := dnsTestZone()
	p, err := NewCloudflareDNSProbe(zone, dnsTestToken)
	zone.Name = "elsewhere.example"
	if err != nil || p.zone != dnsTestZone() || p.baseURL != "https://api.cloudflare.com/client/v4" {
		t.Fatal("constructor did not freeze reviewed provider identity", p, err)
	}
	for _, invalid := range []CloudflareZoneRef{
		{}, {ID: strings.Repeat("A", 32), Name: "gregale.dev"}, {ID: strings.Repeat("0", 32), Name: "gregale.dev"},
		{ID: "../other", Name: "gregale.dev"}, {ID: dnsTestZone().ID, Name: "Gregale.dev"},
		{ID: dnsTestZone().ID, Name: "*.gregale.dev"}, {ID: dnsTestZone().ID, Name: "gregale.dev."},
		{ID: dnsTestZone().ID, Name: "_service.gregale.dev"},
	} {
		if _, err := NewCloudflareDNSProbe(invalid, dnsTestToken); !errors.Is(err, ErrDNSUnverified) {
			t.Fatal("noncanonical reviewed zone accepted", invalid, err)
		}
	}
	for _, token := range []string{"", "secret\r\nInjected: yes", "secret token", "secret\x00value", strings.Repeat("x", api.RuntimeUpgradeDNSAPITokenMaxBytes+1)} {
		if _, err := NewCloudflareDNSProbe(dnsTestZone(), token); !errors.Is(err, ErrDNSUnverified) || (token != "" && strings.Contains(err.Error(), token)) {
			t.Fatal("invalid private token accepted or exposed")
		}
	}
}

func TestCloudflareDNSInventoryRejectsWrongInactiveOrPartialZone(t *testing.T) {
	for _, mode := range []string{"id", "name", "status", "type", "paused", "missing-paused", "nameserver", "duplicate-nameserver", "missing-nameserver", "nameserver-bound"} {
		t.Run(mode, func(t *testing.T) {
			zone := dnsZoneBody()
			switch mode {
			case "id":
				zone["id"] = strings.Repeat("b", 32)
			case "name":
				zone["name"] = "elsewhere.example"
			case "status":
				zone["status"] = "pending"
			case "type":
				zone["type"] = "partial"
			case "paused":
				zone["paused"] = true
			case "missing-paused":
				delete(zone, "paused")
			case "nameserver":
				zone["name_servers"] = []string{"NS.example.net"}
			case "duplicate-nameserver":
				zone["name_servers"] = []string{"ns.example.net", "ns.example.net"}
			case "missing-nameserver":
				delete(zone, "name_servers")
			case "nameserver-bound":
				zone["name_servers"] = make([]string, api.RuntimeUpgradeDNSNameServerLimit+1)
			}
			p := dnsTestProbe(t, func(w http.ResponseWriter, r *http.Request) { dnsServe(t, w, dnsEnvelope(zone)) })
			assertDNSFailure(t, p)
		})
	}
}

func TestCloudflareDNSInventoryRejectsAmbiguousOrOutOfZoneRecords(t *testing.T) {
	for _, mode := range []string{"record-id", "foreign-name", "false-suffix", "foreign-zone-id", "foreign-zone-name", "mixed-case", "type", "missing-ttl", "null-ttl", "ttl-bound", "wrong-address-family", "mapped-address", "address-zone", "missing-proxy", "bad-alias", "proxied-delegation", "null-record", "nested-duplicate", "nested-case-duplicate"} {
		t.Run(mode, func(t *testing.T) {
			row := dnsRow(0)
			switch mode {
			case "record-id":
				row["id"] = "invalid"
			case "foreign-name":
				row["name"] = "other.example"
			case "false-suffix":
				row["name"] = "evilgregale.dev"
			case "foreign-zone-id":
				row["zone_id"] = strings.Repeat("b", 32)
			case "foreign-zone-name":
				row["zone_name"] = "other.example"
			case "mixed-case":
				row["name"] = "App.gregale.dev"
			case "type":
				row["type"] = "a"
			case "missing-ttl":
				delete(row, "ttl")
			case "null-ttl":
				row["ttl"] = nil
			case "ttl-bound":
				row["ttl"] = api.RuntimeUpgradeDNSRecordTTLMax + 1
			case "wrong-address-family":
				row["content"] = "2001:db8::1"
			case "mapped-address":
				row["type"], row["content"] = "AAAA", "::ffff:192.0.2.1"
			case "address-zone":
				row["type"], row["content"] = "AAAA", "fe80::1%lo0"
			case "missing-proxy":
				delete(row, "proxied")
			case "bad-alias":
				row["type"], row["content"] = "CNAME", "{placeholder}.example"
			case "proxied-delegation":
				row["type"], row["content"] = "NS", "ns.example.net"
			}
			raw := jsonConfig(t, row)
			if mode == "null-record" {
				raw = []byte(`null`)
			}
			if mode == "nested-duplicate" || mode == "nested-case-duplicate" {
				key := "secret"
				if mode == "nested-case-duplicate" {
					key = "SECRET"
				}
				raw = []byte(`{"opaque":{"secret":1,"` + key + `":2},` + string(raw[1:]))
			}
			if got, err := parseDNSRecord(raw, dnsTestZone()); !errors.Is(err, ErrDNSUnverified) || got.ID != "" {
				t.Fatal("invalid record returned configuration", got, err)
			}
		})
	}
}

func TestCloudflareDNSInventoryBoundsResponsesCollectionAndOpaqueDepth(t *testing.T) {
	for _, mode := range []string{"streaming-response", "collection", "depth"} {
		t.Run(mode, func(t *testing.T) {
			rows := dnsRows(api.RuntimeUpgradeDNSPageSize * 12)
			p := dnsTestProbe(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "streaming-response" {
					w.(http.Flusher).Flush()
					_, _ = w.Write([]byte(strings.Repeat(" ", api.RuntimeUpgradeDNSResponseMaxBytes+1)))
					return
				}
				if mode == "depth" {
					_, _ = w.Write([]byte(`{"opaque":` + strings.Repeat("[", api.RuntimeUpgradeDNSJSONDepthLimit+1) + `0` + strings.Repeat("]", api.RuntimeUpgradeDNSJSONDepthLimit+1) + `}`))
					return
				}
				if r.URL.Path == "/zones/"+dnsTestZone().ID {
					dnsServe(t, w, dnsEnvelope(dnsZoneBody()))
					return
				}
				body := dnsPage(rows, dnsRequestPage(t, r))
				body["opaque"] = strings.Repeat("x", api.RuntimeUpgradeDNSResponseMaxBytes*3/4)
				dnsServe(t, w, body)
			})
			err := assertDNSFailure(t, p)
			if mode == "collection" && !strings.Contains(err.Error(), "response budget") && !strings.Contains(err.Error(), "bounded provider response") {
				t.Fatal("collection did not fail on its response byte budget", err)
			}
		})
	}
}

func TestCloudflareDNSInventoryCanonicalOwnerLabelsAndOpaqueKinds(t *testing.T) {
	for _, name := range []string{"gregale.dev", "*.gregale.dev", "_service._tcp.gregale.dev", "xn--bcher-kva.gregale.dev"} {
		row := dnsRow(0)
		row["name"] = name
		if _, err := parseDNSRecord(jsonConfig(t, row), dnsTestZone()); err != nil {
			t.Fatal(name, err)
		}
	}
	for _, name := range []string{"a..gregale.dev", "gregale.dev.", "-a.gregale.dev", "a-.gregale.dev", strings.Repeat("x", api.TCPListenerTLSDNSLabelMaxBytes+1) + ".gregale.dev"} {
		row := dnsRow(0)
		row["name"] = name
		if _, err := parseDNSRecord(jsonConfig(t, row), dnsTestZone()); err == nil {
			t.Fatal("noncanonical owner accepted", name)
		}
	}
	row := dnsRow(0)
	row["type"], row["content"] = "FUTURE", "secret-provider-body"
	delete(row, "proxied")
	got, err := parseDNSRecord(jsonConfig(t, row), dnsTestZone())
	if err != nil || got.Type != "FUTURE" || got.Target != "" || got.Proxied != nil || !canonicalDigest(got.ConfigSHA256) {
		t.Fatal("opaque kind lost or treated as interpreted target", got, err)
	}
}
