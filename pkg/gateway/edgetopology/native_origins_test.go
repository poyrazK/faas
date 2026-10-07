package edgetopology

// adr: 620

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/miekg/dns"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

type nativeOriginFixture struct {
	probe        *NativeOriginProbe
	review       NativeOriginReview
	reader       *nativeFixtureReader
	dns          *servedDNSFixture
	body         []byte
	caddyCalls   atomic.Int32
	backendCalls atomic.Int32
	opens        atomic.Int32
	caddyHook    func(int32, http.ResponseWriter) []byte
	backendHook  func()
}

func newNativeOriginFixture(t *testing.T) *nativeOriginFixture {
	t.Helper()
	f := &nativeOriginFixture{dns: newServedDNSFixture(t, nil)}
	id := ingress.PublicEdgeIdentity{SlotID: uuid.NewString(), SessionID: uuid.NewString(), ConfigSHA256: strings.Repeat("a", 64)}
	h, err := ingress.NewPublicIdentityHandler(edgeToken, id)
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.backendCalls.Add(1)
		if f.backendHook != nil {
			f.backendHook()
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(backend.Close)
	b := Binding{Address: backend.Listener.Addr().String(), Edge: id}
	f.body = caddyFixture(t, b, true)
	caddy := wholeProbe(t, func(w http.ResponseWriter, r *http.Request) {
		call := f.caddyCalls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/config/" || r.Header.Get("Cache-Control") != "no-cache" || r.Header.Get(ingress.TokenHeader) != "" {
			t.Error("unexpected Caddy operation", r.Method, r.URL.Path)
		}
		w.Header().Set("ETag", `"/config/ native-one"`)
		body := f.body
		if f.caddyHook != nil {
			body = f.caddyHook(call, w)
		}
		_, _ = w.Write(body)
	})
	f.probe, err = NewNativeOriginProbe(f.dns.probe, caddy)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := url.Parse(caddy.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	scope := nativeTestScope(b.Address, admin.Host)
	f.reader = newNativeFixtureReader(t, scope)
	reviewDNS := f.dns.review
	reviewDNS.Questions = slices.DeleteFunc(slices.Clone(reviewDNS.Questions), func(q DNSQuestion) bool { return q.Type == "CNAME" })
	f.review = NativeOriginReview{DNS: reviewDNS, CaddyConfigSHA256: configDigest(f.body), CaddyService: "caddy.service", Scope: scope,
		Proxies: []NativeProxyReview{{Path: "/config/apps/http/servers/srv0/routes/1/handle/0", Backends: []NativeBackendReview{{Service: "faas-gatewayd-public.service", Binding: b}}}},
	}
	for _, a := range scope.Host.Addresses {
		kind, listen := "A", "0.0.0.0:443"
		if strings.Contains(a.IP, ":") {
			kind, listen = "AAAA", "[::]:443"
		}
		f.review.Origins = append(f.review.Origins, NativeOriginLink{Question: DNSQuestion{Name: "origin.gregale.dev", Type: kind}, Value: a.IP, Server: "srv0", ConfiguredListener: ":443", NativeListener: listen})
	}
	f.probe.open = func(ctx context.Context, review NativeScopeReview) (nativeScopeSession, error) {
		f.opens.Add(1)
		if ctx.Err() != nil {
			return nil, ErrNativeUnverified
		}
		return f.reader, nil
	}
	return f
}

func TestNativeOriginsReconcileFreshSelectedDNSCaddyAndServiceFacts(t *testing.T) {
	f := newNativeOriginFixture(t)
	shared := f.review.Proxies[0]
	shared.Path = configPath
	f.review.Proxies = append(f.review.Proxies, shared)
	before := string(jsonConfig(t, f.review))
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	for i := 0; i < 2; i++ {
		got, err := f.probe.Observe(t.Context(), f.review)
		if err != nil || got.CheckedAt.IsZero() || len(got.Native) != 2 || !sameNativeScope(got.Native[0], got.Native[1]) || len(got.Review.Origins) != 3 || len(got.Review.Proxies) != 2 || len(got.DNS.Rounds) != 2 || got.Caddy.ConfigSHA256 != f.review.CaddyConfigSHA256 {
			t.Fatal(got, err)
		}
		if f.caddyCalls.Load() != int32((i+1)*2) || f.backendCalls.Load() != int32(i+1) || f.dns.apiCalls.Load() != int32(4+(i+1)*12) || f.dns.dnsCalls.Load() != int32((i+1)*22) || f.reader.captures != (i+1)*2 || f.reader.closes != i+1 {
			t.Fatal("cached or partial reconciliation", f.caddyCalls.Load(), f.backendCalls.Load(), f.dns.apiCalls.Load(), f.dns.dnsCalls.Load(), f.reader.captures, f.reader.closes)
		}
		foundS3 := false
		for _, h := range got.Caddy.Handlers {
			if slices.Contains(h.Upstreams, "127.0.0.1:8084") {
				foundS3 = true
			}
		}
		if !foundS3 {
			t.Fatal("omitted unverified S3 graph")
		}
		encoded := string(jsonConfig(t, got))
		for _, secret := range []string{edgeToken, dnsTestToken, "secret-header-token", "secret-key-material", "secret-arbitrary-native-error", "valid_until", "verified_proxies", "execution_available"} {
			if strings.Contains(encoded, secret) {
				t.Fatal("secret or broader authority leaked", secret)
			}
		}
	}
	if string(jsonConfig(t, f.review)) != before {
		t.Fatal("mutated caller review")
	}
}

func TestNativeOriginsRejectLateNativeAndConfigurationReplacement(t *testing.T) {
	for _, kind := range []string{"caddy-body", "caddy-etag", "provider", "provider-late", "socket-inode", "executable-inode", "cgroup-device", "service-invocation", "exit"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeOriginFixture(t)
			switch kind {
			case "caddy-body":
				f.caddyHook = func(call int32, w http.ResponseWriter) []byte {
					if call == 2 {
						return append(slices.Clone(f.body), ' ')
					}
					return f.body
				}
			case "caddy-etag":
				f.caddyHook = func(call int32, w http.ResponseWriter) []byte {
					if call == 2 {
						w.Header().Set("ETag", `"/config/ replaced"`)
					}
					return f.body
				}
			case "provider":
				f.reader.captureHook = func(call int) {
					if call == 1 {
						f.dns.changeAPI.Store(true)
					}
				}
			case "provider-late":
				f.backendHook = func() { f.dns.changeAPI.Store(true) }
			default:
				f.reader.captureHook = func(call int) {
					if call != 2 {
						return
					}
					switch kind {
					case "socket-inode":
						f.reader.reads["self/net/tcp"] = []byte(strings.ReplaceAll(string(f.reader.reads["self/net/tcp"]), "1000 ", "9999 "))
						for name, value := range f.reader.links {
							if value == "socket:[1000]" {
								f.reader.links[name] = "socket:[9999]"
							}
						}
					case "executable-inode":
						id := f.reader.executables["101/exe"]
						id.Inode++
						f.reader.executables["101/exe"] = id
					case "cgroup-device":
						id := f.reader.cgroups["/system.slice/caddy.service"]
						id.Device++
						f.reader.cgroups["/system.slice/caddy.service"] = id
					case "service-invocation":
						f.reader.units["caddy.service"] = []byte(strings.ReplaceAll(string(f.reader.units["caddy.service"]), strings.Repeat("2", 32), strings.Repeat("4", 32)))
					case "exit":
						f.reader.fail = "alive"
					}
				}
			}
			got, err := f.probe.Observe(t.Context(), f.review)
			if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeOriginObservation{}) || f.reader.closes != 1 {
				t.Fatal(got, err, f.reader.closes)
			}
		})
	}
}

func TestNativeOriginsRejectWrongDNSListenerProxyAndStartupScope(t *testing.T) {
	cases := map[string]func(*nativeOriginFixture){
		"stale-caddy": func(f *nativeOriginFixture) { f.review.CaddyConfigSHA256 = strings.Repeat("f", 64) },
		"missing-origin": func(f *nativeOriginFixture) {
			f.review.Origins = f.review.Origins[1:]
			f.review.Scope.Host.Addresses = f.review.Scope.Host.Addresses[1:]
		},
		"foreign-origin": func(f *nativeOriginFixture) {
			f.review.Origins[0].Value = "192.0.2.12"
			f.review.Scope.Host.Addresses[0].IP = "192.0.2.12"
			f.reader.addresses[0].IP = "192.0.2.12"
		},
		"wrong-server":              func(f *nativeOriginFixture) { f.review.Origins[0].Server = "absent" },
		"wrong-configured-listener": func(f *nativeOriginFixture) { f.review.Origins[0].ConfiguredListener = ":80" },
		"cross-family":              func(f *nativeOriginFixture) { f.review.Origins[0].NativeListener = "[::]:443" },
		"foreign-bind":              func(f *nativeOriginFixture) { f.review.Origins[0].NativeListener = "192.0.2.99:443" },
		"unheld-admin": func(f *nativeOriginFixture) {
			f.review.Scope.Services[0].TCPListeners[2] = "127.0.0.1:2019"
			f.reader = newNativeFixtureReader(t, f.review.Scope)
		},
		"missing-proxy": func(f *nativeOriginFixture) {
			f.review.Proxies[0].Path = "/config/apps/http/servers/srv0/routes/99/handle/0"
		},
		"other-server-proxy": func(f *nativeOriginFixture) {
			f.review.Proxies[0].Path = "/config/apps/http/servers/srv1/routes/0/handle/0"
		},
		"unsupported-s3": func(f *nativeOriginFixture) {
			f.review.Proxies[0].Path = "/config/apps/http/servers/srv0/routes/2/handle/0"
		},
		"replaced-startup": func(f *nativeOriginFixture) {
			f.review.Proxies[0].Backends[0].Binding.Edge.SessionID = uuid.NewString()
		},
		"bad-served-dns": func(f *nativeOriginFixture) {
			f.dns.mutate = func(parent bool, reply *dns.Msg) {
				if !parent && reply.Question[0].Qtype == dns.TypeA {
					reply.Answer = reply.Answer[:1]
				}
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newNativeOriginFixture(t)
			mutate(f)
			got, err := f.probe.Observe(t.Context(), f.review)
			if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeOriginObservation{}) || f.reader.closes != 1 {
				t.Fatal(got, err, f.reader.closes)
			}
		})
	}
}

func TestNativeOriginsFreezeAllNestedReviewBeforeAnyNativeOrNetworkIO(t *testing.T) {
	f := newNativeOriginFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	f.reader.captureHook = func(call int) {
		if call == 1 {
			close(started)
			<-release
		}
	}
	type result struct {
		observation NativeOriginObservation
		err         error
	}
	done := make(chan result, 1)
	go func() { got, err := f.probe.Observe(t.Context(), f.review); done <- result{got, err} }()
	<-started
	if f.caddyCalls.Load() != 0 || f.dns.apiCalls.Load() != 4 {
		t.Error("network preceded frozen native review")
	}
	f.review.DNS.Authorities[0].Addresses[0] = "127.0.0.1:1"
	f.review.DNS.Questions[0].Name = "changed.gregale.dev"
	f.review.Scope.Host.Addresses[0].IP = "changed"
	f.review.Scope.Services[0].TCPListeners[0] = "changed"
	f.review.Origins[0].Value = "changed"
	f.review.Proxies[0].Backends[0].Binding.Edge.SessionID = uuid.NewString()
	close(release)
	got := <-done
	if got.err != nil || got.observation.Review.Origins[0].Value == "changed" || got.observation.Review.Scope.Host.Addresses[0].IP == "changed" || slices.Contains(got.observation.Review.Scope.Services[0].TCPListeners, "changed") {
		t.Fatal(got.observation, got.err)
	}
}

func TestNativeOriginsInvalidReviewsCannotOpenNativeOrNetworkScope(t *testing.T) {
	cases := map[string]func(*NativeOriginReview){
		"digest": func(r *NativeOriginReview) { r.CaddyConfigSHA256 = "bad" }, "missing-caddy": func(r *NativeOriginReview) { r.CaddyService = "absent.service" },
		"alias": func(r *NativeOriginReview) {
			r.DNS.Questions = append(r.DNS.Questions, DNSQuestion{Name: "alias.gregale.dev", Type: "CNAME"})
		},
		"missing-questions": func(r *NativeOriginReview) { r.DNS.Questions = nil }, "missing-origins": func(r *NativeOriginReview) { r.Origins = nil },
		"duplicate-link": func(r *NativeOriginReview) { r.Origins = append(r.Origins, r.Origins[0]) }, "extra-host-address": func(r *NativeOriginReview) {
			r.Scope.Host.Addresses = append(r.Scope.Host.Addresses, NativeHostAddress{IP: "192.0.2.99", Interface: "eth0", Index: 2, Prefix: 24})
		},
		"question-family": func(r *NativeOriginReview) { r.Origins[0].Question.Type = "AAAA" }, "hostname-origin": func(r *NativeOriginReview) { r.Origins[0].Value = "host.example.net" },
		"origin-bound": func(r *NativeOriginReview) {
			r.Origins = make([]NativeOriginLink, api.RuntimeUpgradeNativeOriginLimit+1)
		},
		"proxy-bound": func(r *NativeOriginReview) {
			r.Proxies = make([]NativeProxyReview, api.RuntimeUpgradePublicEdgeCaddyHandlerLimit+1)
		},
		"backend-bound": func(r *NativeOriginReview) {
			r.Proxies[0].Backends = make([]NativeBackendReview, api.RuntimeUpgradePublicEdgeLimit+1)
		},
		"duplicate-proxy": func(r *NativeOriginReview) { r.Proxies = append(r.Proxies, r.Proxies[0]) }, "missing-proxy": func(r *NativeOriginReview) { r.Proxies = nil },
		"bad-path": func(r *NativeOriginReview) { r.Proxies[0].Path = "/../../config" }, "unheld-backend": func(r *NativeOriginReview) { r.Proxies[0].Backends[0].Binding.Address = "127.0.0.1:1" },
		"wrong-service": func(r *NativeOriginReview) { r.Proxies[0].Backends[0].Service = "caddy.service" }, "missing-service": func(r *NativeOriginReview) { r.Proxies[0].Backends[0].Service = "absent.service" },
		"startup-nonce": func(r *NativeOriginReview) { r.Proxies[0].Backends[0].Binding.Edge.Nonce = uuid.NewString() },
		"unused-service": func(r *NativeOriginReview) {
			s := r.Scope.Services[1]
			s.Unit = "unused.service"
			s.PID = 103
			s.Cgroup = "/system.slice/unused.service"
			s.TCPListeners = []string{"127.0.0.1:1"}
			r.Scope.Services = append(r.Scope.Services, s)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newNativeOriginFixture(t)
			mutate(&f.review)
			got, err := f.probe.Observe(t.Context(), f.review)
			if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeOriginObservation{}) || f.opens.Load() != 0 || f.caddyCalls.Load() != 0 || f.dns.apiCalls.Load() != 4 || f.dns.dnsCalls.Load() != 0 {
				t.Fatal(got, err, f.opens.Load(), f.caddyCalls.Load(), f.dns.apiCalls.Load())
			}
		})
	}
	if _, err := NewNativeOriginProbe(nil, nil); !errors.Is(err, ErrNativeUnverified) {
		t.Fatal(err)
	}
	var zero NativeOriginProbe
	if got, err := zero.Observe(t.Context(), NativeOriginReview{}); !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeOriginObservation{}) {
		t.Fatal(got, err)
	}
}

func TestNativeOriginsCloseRetainedHandlesOnReadFailureAndCancellation(t *testing.T) {
	for _, stage := range []string{"native-first", "caddy-first", "dns", "cancel", "native-last"} {
		t.Run(stage, func(t *testing.T) {
			f := newNativeOriginFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch stage {
			case "native-first":
				f.reader.fail = "read:machine-id"
			case "caddy-first":
				f.caddyHook = func(call int32, w http.ResponseWriter) []byte {
					w.WriteHeader(http.StatusServiceUnavailable)
					return nil
				}
			case "dns":
				f.dns.changeAPI.Store(true)
			case "cancel":
				f.reader.captureHook = func(call int) {
					if call == 1 {
						cancel()
					}
				}
			case "native-last":
				f.reader.captureHook = func(call int) {
					if call == 2 {
						f.reader.fail = "read:101/stat"
					}
				}
			}
			got, err := f.probe.Observe(ctx, f.review)
			if !errors.Is(err, ErrNativeUnverified) || !reflect.DeepEqual(got, NativeOriginObservation{}) || f.reader.closes != 1 {
				t.Fatal(got, err, f.reader.closes)
			}
		})
	}
}
