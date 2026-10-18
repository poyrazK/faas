package internalmail

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDomainLifecycleFlagsGateRoutesAndKeyIO(t *testing.T) {
	for _, value := range []string{"", "0", "true", " 1"} {
		get := func(key string) string {
			if key == EnableEnv {
				return "1"
			}
			return value
		}
		if RotationEnabled(get) || ReverificationEnabled(get) {
			t.Fatal("truthy flag enabled domain lifecycle")
		}
	}
	s := &Service{Enabled: func() bool { return true }, OperationsEnabled: func() bool { return true }, OperationsToken: operatorToken}
	h, err := Handler(s, testToken)
	if err != nil {
		t.Fatal(err)
	}
	id, key := uuid.NewString(), uuid.NewString()
	for _, path := range []string{"/keys", "/keys/stage", "/keys/" + key + "/verify", "/keys/" + key + "/activate", "/keys/" + key + "/retire", "/reverify"} {
		r := httptest.NewRequest(http.MethodPost, OperationsPath+"/domains/"+id+path, forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+operatorToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("disabled lifecycle route visible", w.Code)
		}
	}
	if _, err := s.StageDKIMKey(t.Context(), id, auditInput()); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	if _, _, err := s.newDKIMKey(id); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled rotation touched codec", err)
	}
	if err := s.ReverifyDomains(t.Context()); !errors.Is(err, ErrDisabled) {
		t.Fatal("disabled checks touched store", err)
	}
	s.RotationEnabled = func() bool { return true }
	s.ReverificationEnabled = func() bool { return true }
	for _, token := range []string{testToken, reportToken, feedbackToken, "customer"} {
		r := httptest.NewRequest(http.MethodPost, OperationsPath+"/domains/"+id+"/keys/stage", forbiddenRead{t})
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("credential crossed into lifecycle", w.Code)
		}
	}
}

type resolverFunc func(context.Context, string) ([]string, error)

func (f resolverFunc) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return f(ctx, name)
}

func TestDomainAuthorizationConfirmsSeparatedMismatchesAndFreshness(t *testing.T) {
	now := time.Now().UTC()
	d := Domain{AuthorizationState: "valid", LastVerifiedAt: &now, VerifiedSendingIP: "192.0.2.10"}
	state, count, first := nextDomainAuthorization(d, "unavailable", now)
	if state != "valid" || count != 0 || first != nil {
		t.Fatal("resolver failure counted as authorization denial")
	}
	state, count, first = nextDomainAuthorization(d, "mismatch", now)
	d.AuthorizationState, d.FailureCount, d.FirstFailureAt = state, count, first
	state, count, _ = nextDomainAuthorization(d, "mismatch", now.Add(time.Minute))
	if state != "failing" || count != 1 {
		t.Fatal("one negative observation was counted twice")
	}
	state, count, _ = nextDomainAuthorization(d, "mismatch", now.Add(api.InternalMailDomainFailureSeparation))
	if state != "paused" || count != 2 {
		t.Fatal("separated failures did not pause")
	}
	d.AuthorizationState = "paused"
	state, count, first = nextDomainAuthorization(d, "valid", now)
	if state != "valid" || count != 0 || first != nil {
		t.Fatal("valid proof did not recover")
	}
	if authorizedDomain(d, domainAuthorization{}, now) {
		t.Fatal("turning off re-verification bypassed a durable pause")
	}
	d.AuthorizationState = "valid"
	if !authorizedDomain(d, domainAuthorization{true, d.VerifiedSendingIP}, now) || authorizedDomain(d, domainAuthorization{true, "192.0.2.11"}, now) || authorizedDomain(d, domainAuthorization{true, d.VerifiedSendingIP}, now.Add(api.InternalMailDomainCheckMaxAge+time.Second)) {
		t.Fatal("freshness or sending IP was ignored")
	}

	s := &Service{}
	for _, tc := range []struct {
		err  error
		want string
	}{{&net.DNSError{IsNotFound: true}, "mismatch"}, {&net.DNSError{IsTimeout: true}, "unavailable"}, {errors.New("private resolver diagnostic"), "unavailable"}} {
		s.Resolver = resolverFunc(func(context.Context, string) ([]string, error) { return nil, tc.err })
		outcome, err := s.observeRecords(t.Context(), Domain{}, []DNSRecord{{Name: "_test.example.test", Value: "proof"}}, []int{0})
		if err != nil || outcome != tc.want {
			t.Fatal("DNS classification failed", outcome, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.observeDomain(ctx, d, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled observation recorded", err)
	}
}
