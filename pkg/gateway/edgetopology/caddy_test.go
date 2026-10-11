package edgetopology

// adr: 702

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

const (
	edgeToken  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	configPath = "/config/apps/http/servers/srv0/routes/0/handle/0/routes/1/handle/0"
)

func edgeBinding(t *testing.T) Binding {
	t.Helper()
	id := ingress.PublicEdgeIdentity{SlotID: uuid.NewString(), SessionID: uuid.NewString(), ConfigSHA256: strings.Repeat("a", 64)}
	h, err := ingress.NewPublicIdentityHandler(edgeToken, id)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	return Binding{Address: server.Listener.Addr().String(), Edge: id}
}

func proxyJSON(bindings []Binding) string {
	upstreams := make([]map[string]string, 0, len(bindings))
	for _, b := range bindings {
		upstreams = append(upstreams, map[string]string{"dial": b.Address})
	}
	body, _ := json.Marshal(map[string]any{"handler": "reverse_proxy", "upstreams": upstreams, "headers": map[string]any{"request": map[string]any{"set": map[string][]string{"X-Forwarded-For": {"{http.request.remote.host}"}}}}})
	return string(body)
}

func adminProbe(t *testing.T, h http.HandlerFunc) *CaddyProbe {
	t.Helper()
	admin := httptest.NewServer(h)
	t.Cleanup(admin.Close)
	p, err := NewCaddyProbe(admin.URL, configPath, edgeToken)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCaddyProbeObservesAllSelectedBackendsReadOnlyAndPreservesInputs(t *testing.T) {
	bindings := []Binding{edgeBinding(t), edgeBinding(t)}
	before := slices.Clone(bindings)
	var gets atomic.Int32
	p := adminProbe(t, func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != configPath || r.Header.Get(ingress.TokenHeader) != "" || r.Header.Get("Cache-Control") != "no-cache" {
			t.Error("unexpected admin request", r.Method, r.URL.Path)
		}
		w.Header().Set("ETag", `"scope 1234"`)
		_, _ = fmt.Fprint(w, proxyJSON(bindings))
	})
	// Neither admin nor backend collection may use process proxy environment.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	observation, err := p.Observe(t.Context(), bindings)
	if err != nil || gets.Load() != 2 || len(observation.Bindings) != 2 || len(observation.ConfigSHA256) != 64 || observation.ConfigPath != configPath || observation.CheckedAt.IsZero() || !slices.Equal(bindings, before) {
		t.Fatal(observation, err, gets.Load())
	}
	if observation.Bindings[0].Address > observation.Bindings[1].Address {
		t.Fatal("noncanonical observation")
	}
}

func TestCaddyProbeRejectsChangedScopeAndChangedETag(t *testing.T) {
	for _, changeETag := range []bool{false, true} {
		t.Run(fmt.Sprint(changeETag), func(t *testing.T) {
			bindings := []Binding{edgeBinding(t)}
			var reads atomic.Int32
			p := adminProbe(t, func(w http.ResponseWriter, _ *http.Request) {
				body, etag := proxyJSON(bindings), `"scope one"`
				if reads.Add(1) == 2 {
					if changeETag {
						etag = `"scope two"`
					} else {
						body += " "
					}
				}
				w.Header().Set("ETag", etag)
				_, _ = fmt.Fprint(w, body)
			})
			got, err := p.Observe(t.Context(), bindings)
			if !errors.Is(err, ErrUnverified) || len(got.Bindings) != 0 || got.ConfigSHA256 != "" || reads.Load() != 2 {
				t.Fatal("changing scope produced observation", got, err)
			}
		})
	}
}

func TestCaddyProbeRejectsUnreviewedReplacedAndUnreachableProcesses(t *testing.T) {
	bindings := []Binding{edgeBinding(t), edgeBinding(t)}
	var reads atomic.Int32
	p := adminProbe(t, func(w http.ResponseWriter, _ *http.Request) {
		reads.Add(1)
		w.Header().Set("ETag", `"scope one"`)
		_, _ = fmt.Fprint(w, proxyJSON(bindings))
	})
	for _, expected := range [][]Binding{bindings[:1], {bindings[0], {Address: bindings[1].Address, Edge: ingress.PublicEdgeIdentity{SlotID: bindings[1].Edge.SlotID, SessionID: uuid.NewString(), ConfigSHA256: bindings[1].Edge.ConfigSHA256}}}} {
		before := reads.Load()
		got, err := p.Observe(t.Context(), expected)
		if !errors.Is(err, ErrUnverified) || len(got.Bindings) != 0 || reads.Load() != before+1 {
			t.Fatal("partial or replaced process accepted", got, err)
		}
	}
	// Reserve then close an owned socket to create a dead endpoint without
	// assuming an external port is unused.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadBindings := slices.Clone(bindings)
	deadBindings[1].Address = dead.Listener.Addr().String()
	dead.Close()
	deadProbe := adminProbe(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"dead scope"`)
		_, _ = fmt.Fprint(w, proxyJSON(deadBindings))
	})
	got, err := deadProbe.Observe(t.Context(), deadBindings)
	if !errors.Is(err, ErrUnverified) || len(got.Bindings) != 0 {
		t.Fatal("unreachable backend fabricated evidence", got, err)
	}
}

func TestCaddyProbeRejectsUnsupportedAndAmbiguousProxyConfigurations(t *testing.T) {
	b := edgeBinding(t)
	expected, err := reviewedBindings([]Binding{b})
	if err != nil {
		t.Fatal(err)
	}
	base := `{"handler":"reverse_proxy","upstreams":[{"dial":"` + b.Address + `"}]}`
	for _, tc := range []struct{ name, body string }{
		{"dynamic", strings.Replace(base, `"handler":`, `"dynamic_upstreams":{"source":"srv"},"handler":`, 1)},
		{"duplicate field", strings.Replace(base, `"handler":`, `"handler":"reverse_proxy","handler":`, 1)},
		{"duplicate nested field", strings.Replace(base, `"dial":`, `"dial":"`+b.Address+`","dial":`, 1)},
		{"case duplicate", strings.Replace(base, `"handler":`, `"HANDLER":"reverse_proxy","handler":`, 1)},
		{"trailing document", base + ` {}`},
		{"unknown module", strings.Replace(base, "reverse_proxy", "custom_proxy", 1)},
		{"extra backend", strings.Replace(base, `}]}`, `},{"dial":"127.0.0.1:1"}]}`, 1)},
		{"duplicate backend", strings.Replace(base, `}]}`, `},{"dial":"`+b.Address+`"}]}`, 1)},
		{"hostname", strings.ReplaceAll(base, b.Address, "localhost:8080")},
		{"remote origin", strings.ReplaceAll(base, b.Address, "192.0.2.1:8080")},
		{"placeholder", strings.ReplaceAll(base, b.Address, "{http.request.host}:8080")},
		{"unix socket", strings.ReplaceAll(base, b.Address, "unix//run/public.sock")},
		{"TLS", strings.Replace(base, `"handler":`, `"transport":{"protocol":"http","tls":{}},"handler":`, 1)},
		{"custom transport", strings.Replace(base, `"handler":`, `"transport":{"protocol":"fastcgi"},"handler":`, 1)},
		{"proxy protocol", strings.Replace(base, `"handler":`, `"transport":{"protocol":"http","proxy_protocol":"v2"},"handler":`, 1)},
		{"extra upstream config", strings.Replace(base, `"dial":`, `"max_requests":3,"dial":`, 1)},
		{"empty", `{"handler":"reverse_proxy","upstreams":[]}`},
		{"null", `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := matchUpstreams([]byte(tc.body), expected); !errors.Is(err, ErrUnverified) {
				t.Fatal("unsupported proxy accepted", err)
			}
		})
	}
	plain := strings.Replace(base, `"handler":`, `"transport":{"protocol":"http"},"handler":`, 1)
	if err := matchUpstreams([]byte(plain), expected); err != nil {
		t.Fatal("explicit plain HTTP rejected", err)
	}
}

func TestCaddyProbeRejectsInvalidCollectionInputsBeforeNetwork(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1:2019", "http://localhost:2019", "http://192.0.2.1:2019", "http://user:secret@127.0.0.1:2019", "http://127.0.0.1:2019/other", "http://127.0.0.1:2019?x=1", "http://127.0.0.1:2019#fragment", "http://127.0.0.1:02019", "http://127.0.0.1:2019?"} {
		if _, err := NewCaddyProbe(endpoint, configPath, edgeToken); err == nil {
			t.Fatal("implicit admin origin accepted", endpoint)
		}
	}
	for _, path := range []string{"/load", "/id/proxy", "/config/", configPath + "/upstreams", configPath + "/../0", strings.Replace(configPath, "routes/0", "routes/00", 1), strings.Repeat("a", api.RuntimeUpgradePublicEdgeConfigPathMaxBytes+1)} {
		if _, err := NewCaddyProbe("http://127.0.0.1:2019", path, edgeToken); err == nil {
			t.Fatal("unreviewed scope accepted", path)
		}
	}
	if _, err := NewCaddyProbe("http://127.0.0.1:2019", configPath, "bad secret"); err == nil {
		t.Fatal("invalid secret accepted")
	}
	b := edgeBinding(t)
	for _, bindings := range [][]Binding{nil, {b, b}, make([]Binding, api.RuntimeUpgradePublicEdgeLimit+1), {{Address: "localhost:8080", Edge: b.Edge}}, {{Address: b.Address, Edge: ingress.PublicEdgeIdentity{SlotID: b.Edge.SlotID, SessionID: b.Edge.SessionID, ConfigSHA256: b.Edge.ConfigSHA256, Proof: "unexpected"}}}} {
		if _, err := reviewedBindings(bindings); !errors.Is(err, ErrUnverified) {
			t.Fatal("ambiguous bindings accepted", err)
		}
	}
}

func TestCaddyProbeRejectsMissingETagOversizedResponsesAndRedirects(t *testing.T) {
	bindings := []Binding{edgeBinding(t)}
	var redirectCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirectCalls.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	for _, mode := range []string{"missing etag", "duplicate etag", "combined etag", "weak etag", "oversized", "redirect", "not found"} {
		t.Run(mode, func(t *testing.T) {
			p := adminProbe(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "redirect" {
					http.Redirect(w, r, target.URL, 307)
					return
				}
				if mode != "missing etag" {
					w.Header().Set("ETag", `"one"`)
				}
				if mode == "duplicate etag" {
					w.Header().Add("ETag", `"two"`)
				}
				if mode == "combined etag" {
					w.Header().Set("ETag", `"one", "two"`)
				}
				if mode == "weak etag" {
					w.Header().Set("ETag", `W/"one"`)
				}
				if mode == "oversized" {
					_, _ = fmt.Fprint(w, strings.Repeat(" ", api.RuntimeUpgradePublicEdgeProxyMaxBytes+1))
					return
				}
				if mode == "not found" {
					w.WriteHeader(404)
				}
				_, _ = fmt.Fprint(w, proxyJSON(bindings))
			})
			got, err := p.Observe(t.Context(), bindings)
			if !errors.Is(err, ErrUnverified) || got.ConfigSHA256 != "" || redirectCalls.Load() != 0 {
				t.Fatal("invalid admin reply accepted", got, err)
			}
		})
	}
}

func TestCaddyProbeCancellationIsBoundedAndReturnsNoPartialObservation(t *testing.T) {
	bindings := []Binding{edgeBinding(t)}
	started, release := make(chan struct{}), make(chan struct{})
	p := adminProbe(t, func(http.ResponseWriter, *http.Request) { close(started); <-release })
	defer close(release)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		got, err := p.Observe(ctx, bindings)
		if got.ConfigSHA256 != "" {
			t.Error("cancelled collection returned successful observation")
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
		t.Fatal("collection did not cancel")
	}
}
