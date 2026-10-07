package edgetopology

// adr: 617

import (
	"context"
	"encoding/json"
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

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

func jsonConfig(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func proxyNode(address string) map[string]any {
	return map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": address}}, "headers": map[string]any{"request": map[string]any{"set": map[string][]string{"Authorization": {"secret-header-token"}}}}}
}

func routeNode(handler any) map[string]any { return map[string]any{"handle": []any{handler}} }

func caddyFixture(t *testing.T, binding Binding, withS3 bool) []byte {
	t.Helper()
	subroute := map[string]any{"handler": "subroute", "routes": []any{
		map[string]any{"match": []any{map[string]any{"not": []any{map[string]any{"remote_ip": map[string]any{"ranges": []string{"192.0.2.0/24"}}}}}}, "handle": []any{map[string]any{"handler": "static_response", "abort": true}}},
		routeNode(proxyNode(binding.Address)),
	}, "errors": map[string]any{"routes": []any{routeNode(proxyNode(binding.Address))}}}
	routes := []any{
		map[string]any{"match": []any{map[string]any{"host": []string{"gregale.dev", "*.gregale.dev"}}}, "group": "platform", "terminal": true, "handle": []any{subroute}},
		routeNode(proxyNode(binding.Address)), // customer-domain catch-all remains explicit.
	}
	if withS3 {
		s3 := proxyNode("127.0.0.1:8084")
		s3["transport"] = map[string]any{"protocol": "http", "response_header_timeout": 1805000000000, "read_timeout": 1805000000000, "write_timeout": 1805000000000}
		s3["load_balancing"] = map[string]any{"try_duration": 0}
		s3Route := routeNode(s3)
		s3Route["match"] = []any{map[string]any{"host": []string{"s3.gregale.dev"}}}
		routes = append(routes, s3Route)
	}
	return jsonConfig(t, map[string]any{"admin": map[string]any{"listen": "localhost:2019"}, "apps": map[string]any{
		"tls": map[string]any{"certificates": map[string]any{"load_pem": []any{map[string]any{"key": "secret-key-material", "certificate": "secret-certificate-material"}}}},
		"http": map[string]any{"servers": map[string]any{
			"srv0": map[string]any{"listen": []string{":443"}, "protocols": []string{"h1", "h2", "h3"}, "routes": routes, "errors": map[string]any{"routes": []any{routeNode(proxyNode(binding.Address))}}},
			"srv1": map[string]any{"listen": []string{":80"}, "routes": []any{routeNode(map[string]any{"handler": "static_response", "status_code": 308, "body": "secret-static-body", "headers": map[string][]string{"Location": {"https://{http.request.host}{http.request.uri}"}}})}},
		}},
	}})
}

func wholeProbe(t *testing.T, handler http.HandlerFunc) *CaddyInventoryProbe {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p, err := NewCaddyInventoryProbe(server.URL, edgeToken)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func staticWholeProbe(t *testing.T, body []byte, gets *atomic.Int32) *CaddyInventoryProbe {
	t.Helper()
	return wholeProbe(t, func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/config/" || r.Header.Get(ingress.TokenHeader) != "" {
			t.Error("unexpected whole-config request", r.Method, r.URL.Path)
		}
		w.Header().Set("ETag", `"/config/ one"`)
		_, _ = w.Write(body)
	})
}

func fullReview(t *testing.T, body []byte, binding Binding) CaddyInventoryReview {
	t.Helper()
	graph, err := parseCaddyInventory(body)
	if err != nil {
		t.Fatal(err)
	}
	review := CaddyInventoryReview{ConfigSHA256: configDigest(body)}
	for _, h := range graph.inventory.Handlers {
		if h.Module == "reverse_proxy" {
			review.Proxies = append(review.Proxies, CaddyProxyReview{Path: h.Path, Bindings: []Binding{binding}})
		}
	}
	return review
}

func TestCaddyInventoryCollectIncludesWholeDeclaredGraphAndRedactsSecrets(t *testing.T) {
	b := edgeBinding(t)
	body := caddyFixture(t, b, true)
	var gets atomic.Int32
	p := staticWholeProbe(t, body, &gets)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	got, err := p.Collect(t.Context())
	if err != nil || gets.Load() != 2 || len(got.Servers) != 2 || got.ConfigSHA256 != configDigest(body) || got.CheckedAt.IsZero() {
		t.Fatal(got, err, gets.Load())
	}
	if got.Servers[0].Name != "srv0" || got.Servers[1].Name != "srv1" || !slices.Equal(got.Servers[0].Listen, []string{":443"}) {
		t.Fatal(got.Servers)
	}
	var errors, catchAll int
	for _, r := range got.Routes {
		if r.ErrorRoute {
			errors++
		}
		if len(r.HostMatchers) == 0 {
			catchAll++
		}
		if r.Path == "/config/apps/http/servers/srv0/routes/0" && (!r.Terminal || r.Group != "platform" || len(r.HostMatchers) != 1 || !slices.Equal(r.HostMatchers[0], []string{"gregale.dev", "*.gregale.dev"})) {
			t.Fatal(r)
		}
	}
	var proxies, s3 int
	for _, h := range got.Handlers {
		if h.Module == "reverse_proxy" {
			proxies++
			if slices.Contains(h.Upstreams, "127.0.0.1:8084") {
				s3++
			}
		}
	}
	if errors != 2 || catchAll < 1 || proxies != 5 || s3 != 1 {
		t.Fatal("omitted declared forwarding", errors, catchAll, proxies, s3)
	}
	encoded := string(jsonConfig(t, got))
	for _, secret := range []string{"secret-header-token", "secret-key-material", "secret-certificate-material", "secret-static-body"} {
		if strings.Contains(encoded, secret) {
			t.Fatal("inventory disclosed opaque secret")
		}
	}
}

func TestCaddyInventoryBindingReviewsEveryProxyAndProbesSharedEndpointOnce(t *testing.T) {
	id := ingress.PublicEdgeIdentity{SlotID: uuid.NewString(), SessionID: uuid.NewString(), ConfigSHA256: strings.Repeat("a", 64)}
	h, err := ingress.NewPublicIdentityHandler(edgeToken, id)
	if err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { probes.Add(1); h.ServeHTTP(w, r) }))
	defer edge.Close()
	b := Binding{Address: edge.Listener.Addr().String(), Edge: id}
	body := caddyFixture(t, b, false)
	review := fullReview(t, body, b)
	if len(review.Proxies) != 4 {
		t.Fatal(review)
	}
	before := string(jsonConfig(t, review))
	var gets atomic.Int32
	p := staticWholeProbe(t, body, &gets)
	for i := 0; i < 2; i++ {
		got, err := p.ObserveBindings(t.Context(), review)
		if err != nil || got.ConfigSHA256 != review.ConfigSHA256 || probes.Load() != int32(i+1) || len(got.VerifiedProxies) != 4 || got.VerifiedProxies[0].Bindings[0] != b {
			t.Fatal(got, err, probes.Load())
		}
	}
	if gets.Load() != 4 || string(jsonConfig(t, review)) != before {
		t.Fatal("input mutated or snapshots omitted", gets.Load())
	}
}

func TestCaddyInventoryRejectsPartialExtraConflictingAndStaleReviews(t *testing.T) {
	b := edgeBinding(t)
	body := caddyFixture(t, b, false)
	for _, mode := range []string{"missing error proxy", "extra proxy", "duplicate proxy", "stale full digest", "conflicting backend", "replaced session"} {
		t.Run(mode, func(t *testing.T) {
			review := fullReview(t, body, b)
			switch mode {
			case "missing error proxy":
				review.Proxies = review.Proxies[:len(review.Proxies)-1]
			case "extra proxy":
				review.Proxies = append(review.Proxies, CaddyProxyReview{Path: "/config/apps/http/servers/srv2/routes/0/handle/0", Bindings: []Binding{b}})
			case "duplicate proxy":
				review.Proxies[1] = review.Proxies[0]
			case "stale full digest":
				review.ConfigSHA256 = strings.Repeat("b", 64)
			case "conflicting backend":
				review.Proxies[1].Bindings[0].Edge.SessionID = uuid.NewString()
			case "replaced session":
				for i := range review.Proxies {
					review.Proxies[i].Bindings[0].Edge.SessionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
				}
			}
			var gets atomic.Int32
			got, err := staticWholeProbe(t, body, &gets).ObserveBindings(t.Context(), review)
			if !errors.Is(err, ErrUnverified) || got.ConfigSHA256 != "" || len(got.Handlers) != 0 {
				t.Fatal("bad review returned partial success", got, err)
			}
		})
	}
}

func TestCaddyInventoryCannotExcludeSeparateS3ServiceFromBindingProof(t *testing.T) {
	b := edgeBinding(t)
	body := caddyFixture(t, b, true)
	review := fullReview(t, body, b)
	var gets atomic.Int32
	p := staticWholeProbe(t, body, &gets)
	if got, err := p.Collect(t.Context()); err != nil || len(got.Handlers) == 0 {
		t.Fatal("S3 not inventoried", got, err)
	}
	if got, err := p.ObserveBindings(t.Context(), review); !errors.Is(err, ErrUnverified) || got.ConfigSHA256 != "" {
		t.Fatal("S3 silently borrowed public identity", got, err)
	}
	for i, proxy := range review.Proxies {
		if proxy.Path == "/config/apps/http/servers/srv0/routes/2/handle/0" {
			review.Proxies = append(review.Proxies[:i:i], review.Proxies[i+1:]...)
			break
		}
	}
	if got, err := p.ObserveBindings(t.Context(), review); !errors.Is(err, ErrUnverified) || got.ConfigSHA256 != "" {
		t.Fatal("S3 excluded from whole-config proof", got, err)
	}
}

func TestCaddyInventoryRejectsChangesOutsideSelectedHandler(t *testing.T) {
	b := edgeBinding(t)
	body := caddyFixture(t, b, false)
	review := fullReview(t, body, b)
	changed := []byte(strings.Replace(string(body), "secret-key-material", "changed-key-material", 1))
	var gets atomic.Int32
	got, err := staticWholeProbe(t, changed, &gets).ObserveBindings(t.Context(), review)
	if !errors.Is(err, ErrUnverified) || got.ConfigSHA256 != "" || gets.Load() != 1 {
		t.Fatal("full-config change retained selected-proxy review", got, err)
	}
}

func TestCaddyInventoryBookendsRejectChangedConfigAndETag(t *testing.T) {
	body := caddyFixture(t, edgeBinding(t), false)
	for _, mode := range []string{"body", "etag"} {
		t.Run(mode, func(t *testing.T) {
			var gets atomic.Int32
			p := wholeProbe(t, func(w http.ResponseWriter, r *http.Request) {
				value, etag := body, `"whole one"`
				if gets.Add(1) == 2 {
					if mode == "body" {
						value = append(slices.Clone(body), ' ')
					} else {
						etag = `"whole two"`
					}
				}
				w.Header().Set("ETag", etag)
				_, _ = w.Write(value)
			})
			got, err := p.Collect(t.Context())
			if !errors.Is(err, ErrUnverified) || got.ConfigSHA256 != "" || gets.Load() != 2 {
				t.Fatal("changed whole config returned inventory", got, err)
			}
		})
	}
}

func minimalConfig(t *testing.T, servers any) []byte {
	return jsonConfig(t, map[string]any{"apps": map[string]any{"http": map[string]any{"servers": servers}}})
}

func minimalServer(handler any) map[string]any {
	return map[string]any{"listen": []string{":443"}, "routes": []any{routeNode(handler)}}
}

func TestCaddyInventoryRejectsUnsupportedHiddenRoutingAndAmbiguousJSON(t *testing.T) {
	base := minimalConfig(t, map[string]any{"srv0": minimalServer(proxyNode("127.0.0.1:8080"))})
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"named routes", func(s map[string]any) {
			s["named_routes"] = map[string]any{"hidden": routeNode(proxyNode("127.0.0.1:1"))}
		}},
		{"listener wrapper", func(s map[string]any) { s["listener_wrappers"] = []any{map[string]any{"wrapper": "custom"}} }},
		{"unknown handler", func(s map[string]any) {
			s["routes"] = []any{routeNode(map[string]any{"handler": "custom", "routes": []any{routeNode(proxyNode("127.0.0.1:1"))}})}
		}},
		{"proxy response forwarding", func(s map[string]any) {
			p := proxyNode("127.0.0.1:8080")
			p["handle_response"] = []any{map[string]any{"routes": []any{routeNode(proxyNode("127.0.0.1:1"))}}}
			s["routes"] = []any{routeNode(p)}
		}},
		{"dynamic upstream", func(s map[string]any) {
			p := proxyNode("127.0.0.1:8080")
			p["dynamic_upstreams"] = map[string]any{"source": "srv"}
			s["routes"] = []any{routeNode(p)}
		}},
		{"unknown matcher", func(s map[string]any) {
			r := routeNode(proxyNode("127.0.0.1:8080"))
			r["match"] = []any{map[string]any{"custom": map[string]any{}}}
			s["routes"] = []any{r}
		}},
		{"unknown negative matcher", func(s map[string]any) {
			r := routeNode(proxyNode("127.0.0.1:8080"))
			r["match"] = []any{map[string]any{"not": []any{map[string]any{"custom": true}}}}
			s["routes"] = []any{r}
		}},
		{"TLS upstream", func(s map[string]any) {
			p := proxyNode("127.0.0.1:8080")
			p["transport"] = map[string]any{"protocol": "http", "tls": map[string]any{}}
			s["routes"] = []any{routeNode(p)}
		}},
		{"null route", func(s map[string]any) { s["routes"] = []any{nil} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := minimalServer(proxyNode("127.0.0.1:8080"))
			tc.change(s)
			if got, err := parseCaddyInventory(minimalConfig(t, map[string]any{"srv0": s})); !errors.Is(err, ErrUnverified) || got != nil {
				t.Fatal("hidden routing accepted", err)
			}
		})
	}
	for _, body := range [][]byte{
		[]byte(strings.Replace(string(base), `"apps":`, `"apps":{},"apps":`, 1)),
		[]byte(strings.Replace(string(base), `"listen":`, `"listen":[":443"],"listen":`, 1)),
		append(slices.Clone(base), []byte(` {}`)...),
		[]byte(strings.Replace(string(base), `"http":`, `"layer4":{},"http":`, 1)),
		minimalConfig(t, map[string]any{"srv0": minimalServer(proxyNode("127.0.0.1:8080")), "srv1": minimalServer(proxyNode("127.0.0.1:8080"))}),
	} {
		if got, err := parseCaddyInventory(body); !errors.Is(err, ErrUnverified) || got != nil {
			t.Fatal("ambiguous or out-of-scope config accepted", err)
		}
	}
}

func TestCaddyInventoryBoundsWholeGraphWithoutTruncating(t *testing.T) {
	for _, mode := range []string{"servers", "listeners", "routes", "handlers", "matchers", "hosts", "depth"} {
		t.Run(mode, func(t *testing.T) {
			s := minimalServer(map[string]any{"handler": "static_response", "status_code": 404})
			servers := map[string]any{"srv0": s}
			switch mode {
			case "servers":
				for i := 0; i <= api.RuntimeUpgradePublicEdgeCaddyServerLimit; i++ {
					servers["server"+strconv.Itoa(i)] = map[string]any{"listen": []string{":" + strconv.Itoa(8000+i)}}
				}
			case "listeners":
				v := []string{}
				for i := 0; i <= api.RuntimeUpgradePublicEdgeCaddyListenerLimit; i++ {
					v = append(v, ":"+strconv.Itoa(8000+i))
				}
				s["listen"] = v
			case "routes":
				v := []any{}
				for i := 0; i <= api.RuntimeUpgradePublicEdgeCaddyRouteLimit; i++ {
					v = append(v, routeNode(map[string]any{"handler": "static_response"}))
				}
				s["routes"] = v
			case "handlers":
				v := []any{}
				for i := 0; i <= api.RuntimeUpgradePublicEdgeCaddyHandlerLimit; i++ {
					v = append(v, map[string]any{"handler": "static_response"})
				}
				s["routes"] = []any{map[string]any{"handle": v}}
			case "matchers":
				v := []any{}
				for i := 0; i <= api.RuntimeUpgradePublicEdgeCaddyMatcherLimit; i++ {
					v = append(v, map[string]any{})
				}
				s["routes"] = []any{map[string]any{"match": v}}
			case "hosts":
				v := []string{}
				for i := 0; i <= api.RuntimeUpgradePublicEdgeCaddyHostLimit; i++ {
					v = append(v, fmt.Sprintf("host%d.example", i))
				}
				s["routes"] = []any{map[string]any{"match": []any{map[string]any{"host": v}}}}
			case "depth":
				r := routeNode(map[string]any{"handler": "static_response"})
				for i := 0; i <= api.RuntimeUpgradePublicEdgeCaddyDepthLimit; i++ {
					r = routeNode(map[string]any{"handler": "subroute", "routes": []any{r}})
				}
				s["routes"] = []any{r}
			}
			if got, err := parseCaddyInventory(minimalConfig(t, servers)); !errors.Is(err, ErrUnverified) || got != nil {
				t.Fatal("graph overflow produced partial inventory", err)
			}
		})
	}
}

func TestCaddyInventoryCollectCancelsWithoutPartialSuccess(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	p := wholeProbe(t, func(http.ResponseWriter, *http.Request) { close(started); <-release })
	defer close(release)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		got, err := p.Collect(ctx)
		if got.ConfigSHA256 != "" {
			t.Error("cancelled inventory returned data")
		}
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnverified) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("inventory did not cancel")
	}
}

func TestCaddyInventoryFreezesReviewedBindingsBeforeNetworkWait(t *testing.T) {
	b := edgeBinding(t)
	body := caddyFixture(t, b, false)
	review := fullReview(t, body, b)
	started, release := make(chan struct{}), make(chan struct{})
	var gets atomic.Int32
	p := wholeProbe(t, func(w http.ResponseWriter, r *http.Request) {
		if gets.Add(1) == 1 {
			close(started)
			<-release
		}
		w.Header().Set("ETag", `"whole one"`)
		_, _ = w.Write(body)
	})
	done := make(chan error, 1)
	go func() { _, err := p.ObserveBindings(t.Context(), review); done <- err }()
	<-started
	for i := range review.Proxies {
		review.Proxies[i].Path = "/mutated"
		review.Proxies[i].Bindings[0].Edge.SessionID = uuid.NewString()
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal("caller mutation changed frozen review", err)
	}
}

func TestCaddyInventoryCanonicalListenerAndHostPredicates(t *testing.T) {
	for _, v := range []string{":443", "0.0.0.0:80", "[::]:443", "127.0.0.1:8080"} {
		if !caddyListener(v) {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"localhost:443", ":0", ":0443", "tcp/:443", ":443-445", "unix//run/caddy.sock", "[::1%lo0]:443", "[::ffff:127.0.0.1]:443"} {
		if caddyListener(v) {
			t.Fatal("implicit listener accepted", v)
		}
	}
	for _, v := range []string{"gregale.dev", "*.gregale.dev", "*", "localhost"} {
		if !caddyHost(v) {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"Gregale.dev", "gregale.dev.", "prefix*.gregale.dev", "-bad.example", "{http.request.host}", "bad.example:443"} {
		if caddyHost(v) {
			t.Fatal("noncanonical host accepted", v)
		}
	}
}
