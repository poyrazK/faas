// adr: 570
package gateway

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var synthPolicyRoutes = []struct{ name, path, body string }{
	{"wake", "/v1/synthesize", `{"app_id":"app-1","path":"/"}`},
	{"single", "/v1/invocations:dispatch", `{"app_id":"app-1","invocation_id":"inv-1","source":"async_invoke"}`},
	{"batch", "/v1/invocations:dispatch_batch", `{"app_id":"app-1","invocation_id":"inv-1","source":"queue","records":[{"id":"record-1"}]}`},
}

func TestSynthIngressPolicyLookupFailureBeforeDispatch(t *testing.T) {
	for _, route := range synthPolicyRoutes {
		t.Run(route.name, func(t *testing.T) {
			dispatcher := &fakeDispatcher{}
			mode, lookupErr := state.AppPublicAuthModeOpen, errors.New("database credential private-sentinel")
			lookups := 0
			server := NewSynthServer("", dispatcher, nil).WithVerifiedAppModeLookup(func(ctx context.Context, appID string) (string, error) {
				lookups++
				if appID != "app-1" || ctx.Err() != nil {
					t.Fatalf("lookup app/context = %s/%v", appID, ctx.Err())
				}
				return mode, lookupErr
			})
			request := func() *httptest.ResponseRecorder {
				response := httptest.NewRecorder()
				server.Mux().ServeHTTP(response, httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(route.body)))
				return response
			}
			refused := request()
			if refused.Code != http.StatusServiceUnavailable || refused.Header().Get("Retry-After") != "1" ||
				!strings.Contains(refused.Body.String(), api.CodeTrafficPolicyUnavailable) || strings.Contains(refused.Body.String(), "private-sentinel") || len(dispatcher.calls) != 0 {
				t.Fatalf("lookup refusal = %d %s, calls=%v", refused.Code, refused.Body, dispatcher.calls)
			}
			lookupErr = nil
			if recovered := request(); recovered.Code != http.StatusOK || len(dispatcher.calls) != 1 {
				t.Fatalf("recovery = %d %s, calls=%v", recovered.Code, recovered.Body, dispatcher.calls)
			}
			mode = state.AppPublicAuthModeInternalOnly
			server.WithInternalSvcVerifier(&testInternalSvcVerifier{})
			if changed := request(); changed.Code != http.StatusForbidden || len(dispatcher.calls) != 1 || lookups != 3 {
				t.Fatalf("fresh policy = %d %s, calls=%v lookups=%d", changed.Code, changed.Body, dispatcher.calls, lookups)
			}
		})
	}
}

func TestSynthIngressPolicyKnownModesAndTokenGate(t *testing.T) {
	pub, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	validToken := mintTestToken(t, pub, private, "schedd", 30)
	for _, tc := range []struct {
		name, mode, token string
		verifier          bool
		status            int
	}{
		{"open", state.AppPublicAuthModeOpen, "", false, http.StatusOK},
		{"bearer", state.AppPublicAuthModeBearer, "", false, http.StatusOK},
		{"basic", state.AppPublicAuthModeBasic, "", false, http.StatusOK},
		{"allowlist", state.AppPublicAuthModeIPAllowlist, "", false, http.StatusOK},
		{"missing token", state.AppPublicAuthModeInternalOnly, "", true, http.StatusForbidden},
		{"invalid token", state.AppPublicAuthModeInternalOnly, "invalid-private-token", true, http.StatusForbidden},
		{"valid token", state.AppPublicAuthModeInternalOnly, validToken, true, http.StatusOK},
		{"missing verifier", state.AppPublicAuthModeInternalOnly, validToken, false, http.StatusInternalServerError},
	} {
		for _, route := range synthPolicyRoutes {
			t.Run(tc.name+"/"+route.name, func(t *testing.T) {
				dispatcher := &fakeDispatcher{}
				server := NewSynthServer("", dispatcher, nil).WithVerifiedAppModeLookup(func(context.Context, string) (string, error) { return tc.mode, nil })
				if tc.verifier {
					server.WithInternalSvcVerifier(&testInternalSvcVerifier{allowed: map[string]ed25519.PublicKey{"schedd": pub}})
				}
				request := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(route.body))
				if tc.token != "" {
					request.Header.Set("Authorization", "Bearer "+tc.token)
				}
				response := httptest.NewRecorder()
				server.Mux().ServeHTTP(response, request)
				wantCalls := 0
				if tc.status == http.StatusOK {
					wantCalls = 1
				}
				if response.Code != tc.status || len(dispatcher.calls) != wantCalls {
					t.Fatalf("response=%d %s, dispatch=%v", response.Code, response.Body, dispatcher.calls)
				}
				if tc.token != "" && strings.Contains(response.Body.String(), tc.token) {
					t.Fatal("response leaked the internal token")
				}
			})
		}
	}
}

type synthPolicyContextKey struct{}

func TestSynthIngressPolicyContextBoundAndCancellation(t *testing.T) {
	for _, route := range synthPolicyRoutes {
		for _, scenario := range []string{"cancelled", "expired", "read timeout", "parent timeout"} {
			t.Run(scenario+"/"+route.name, func(t *testing.T) {
				ctx := context.WithValue(t.Context(), synthPolicyContextKey{}, "inbound")
				var cancel context.CancelFunc
				switch scenario {
				case "cancelled":
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				case "expired":
					ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				case "parent timeout":
					ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
				default:
					ctx, cancel = context.WithCancel(ctx)
				}
				defer cancel()
				dispatcher, lookups := &fakeDispatcher{}, 0
				server := NewSynthServer("", dispatcher, nil).WithVerifiedAppModeLookup(func(readCtx context.Context, _ string) (string, error) {
					lookups++
					deadline, ok := readCtx.Deadline()
					if !ok || time.Until(deadline) > api.TrafficServicePolicyReadTimeout || readCtx.Value(synthPolicyContextKey{}) != "inbound" {
						t.Fatal("lookup lost inbound context or its finite bound")
					}
					<-readCtx.Done()
					// An adapter's late success cannot override timeout/cancellation.
					return state.AppPublicAuthModeOpen, nil
				})
				request := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(route.body)).WithContext(ctx)
				response, started := httptest.NewRecorder(), time.Now()
				server.Mux().ServeHTTP(response, request)
				wantLookups := 1
				if scenario == "cancelled" || scenario == "expired" {
					wantLookups = 0
				}
				if response.Code != http.StatusServiceUnavailable || len(dispatcher.calls) != 0 || lookups != wantLookups || time.Since(started) > time.Second {
					t.Fatalf("bounded refusal = %d calls=%v lookups=%d time=%s", response.Code, dispatcher.calls, lookups, time.Since(started))
				}
			})
		}
	}
}

func TestSynthIngressPolicyCoversPrewokenAndEmptyBatch(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/v1/invocations:dispatch", `{"app_id":"app-1","invocation_id":"inv-1","source":"async_invoke","instance_id":"instance-1","node_id":"node-1","deployment_id":"deployment-1","port":8080}`},
		{"/v1/invocations:dispatch_batch", `{"app_id":"app-1","invocation_id":"inv-1","records":[]}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			dispatcher, lookups := &fakeDispatcher{}, 0
			server := NewSynthServer("", dispatcher, nil).WithVerifiedAppModeLookup(func(context.Context, string) (string, error) {
				lookups++
				return "", errors.New("policy read failed")
			})
			response := httptest.NewRecorder()
			server.Mux().ServeHTTP(response, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
			if response.Code != http.StatusServiceUnavailable || len(dispatcher.calls) != 0 || lookups != 1 {
				t.Fatalf("policy refusal = %d, calls=%v lookups=%d", response.Code, dispatcher.calls, lookups)
			}
		})
	}
}

func TestSynthIngressPolicyPreservesWorkflowAdmissionOrder(t *testing.T) {
	dispatcher, lookups := &fakeDispatcher{}, 0
	server := NewSynthServer("", dispatcher, nil).WithVerifiedAppModeLookup(func(context.Context, string) (string, error) {
		lookups++
		return state.AppPublicAuthModeOpen, nil
	})
	server.WithInternalSvcVerifier(&testInternalSvcVerifier{})
	server.WithWorkflowAdmission(func(context.Context, string, string, string, int) error { return nil })
	response := httptest.NewRecorder()
	server.Mux().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch",
		strings.NewReader(`{"app_id":"app-1","invocation_id":"inv-1","source":"workflow"}`)))
	if response.Code != http.StatusForbidden || lookups != 0 || len(dispatcher.calls) != 0 {
		t.Fatalf("workflow authorization = %d, policy reads=%d dispatch=%v", response.Code, lookups, dispatcher.calls)
	}
}

func TestSynthIngressPolicyUnknownModeRefusesAllRoutes(t *testing.T) {
	for _, mode := range []string{"", "unknown"} {
		for _, route := range synthPolicyRoutes {
			t.Run(mode+"/"+route.name, func(t *testing.T) {
				dispatcher := &fakeDispatcher{}
				server := NewSynthServer("", dispatcher, nil).WithAppModeLookup(func(context.Context, string) string { return mode })
				response := httptest.NewRecorder()
				server.Mux().ServeHTTP(response, httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(route.body)))
				if response.Code != http.StatusServiceUnavailable {
					t.Errorf("status = %d, want 503", response.Code)
				}
				if len(dispatcher.calls) != 0 || server.Calls() != 0 {
					t.Errorf("unverified policy reached dispatch: calls=%v successful=%d", dispatcher.calls, server.Calls())
				}
				var problem api.Problem
				if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || problem.Code != api.CodeTrafficPolicyUnavailable {
					t.Errorf("problem = %s, decode = %v", response.Body, err)
				}
			})
		}
	}
}
