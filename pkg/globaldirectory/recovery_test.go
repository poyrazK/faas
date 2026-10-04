package globaldirectory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type delegationTransport func(*http.Request) (*http.Response, error)

func (f delegationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func delegationReply(r *http.Request, status int, data any) *http.Response {
	body, _ := json.Marshal(data)
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}
}
func TestDelegationLostRegistrationAndBackgroundRecovery(t *testing.T) {
	s := NewMemoryStore()
	owner := uuid.NewString()
	app, targets := releaseSetup(t, s, owner)
	admin, _, _ := api.GenerateAPIKey()
	keys := delegationKeys(t)
	var mu sync.Mutex
	metadata := map[string]api.RegionalReleaseGrantResponse{}
	receipts := map[string]api.CellImageReleaseResponse{}
	var grantPuts, imagePuts atomic.Int32
	var lost atomic.Bool
	service := &DelegationService{Store: s, Journal: s, Keys: keys, Authorize: func(context.Context, string) error { return nil }}
	service.Verifier.Client = &http.Client{Transport: delegationTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		var target Target
		for _, t := range targets {
			if r.URL.Host == strings.TrimPrefix(t.APIBase, "https://") {
				target = t
			}
		}
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if strings.HasSuffix(r.URL.Path, "/binding") {
			if auth != admin && !api.ValidRegionalReleaseTokenFormat(auth) {
				t.Error("invalid proof authority")
			}
			return delegationReply(r, 200, api.CellAppBindingResponse{CellID: target.CellID, Region: target.Region, AccountID: target.RegionalAccountID, AppID: target.RegionalAppID, AppSlug: target.RegionalAppSlug}), nil
		}
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if strings.Contains(r.URL.Path, "release-grants") {
			if r.Method == http.MethodPut {
				grantPuts.Add(1)
				if auth != admin {
					t.Error("consent registration did not use request admin")
				}
				g, err := s.ReadDelegation(r.Context(), owner, app.ID, id)
				if err != nil || len(g.Sealed) == 0 {
					t.Errorf("registration preceded durable seal: %v", err)
				}
				var req api.PutRegionalReleaseGrantRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					return nil, err
				}
				metadata[id] = api.RegionalReleaseGrantResponse{GrantID: id, CellID: target.CellID, RegionalAppID: target.RegionalAppID, GlobalAppID: req.GlobalAppID, ExpiresAt: req.ExpiresAt, Status: DelegationActive}
				if lost.CompareAndSwap(false, true) {
					return nil, errors.New("registration response lost after commit")
				}
			} else if r.Method == http.MethodDelete {
				meta := metadata[id]
				meta.Status = DelegationRevoked
				metadata[id] = meta
			}
			meta, ok := metadata[id]
			if !ok {
				return delegationReply(r, 401, nil), nil
			}
			return delegationReply(r, 200, meta), nil
		}
		if !api.ValidRegionalReleaseTokenFormat(auth) {
			t.Error("admin credential used for release")
		}
		if r.Method == http.MethodPut {
			imagePuts.Add(1)
			dep := uuid.NewString()
			receipts[r.URL.Path] = api.CellImageReleaseResponse{CellID: target.CellID, Region: target.Region, RegionalAccountID: target.RegionalAccountID, RegionalAppID: target.RegionalAppID, ReleaseID: id, Image: releaseTestImage, CreatedAt: time.Now().UTC(), DeploymentID: &dep, DeploymentStatus: "pending", Ordering: &api.CellImageReleaseOrdering{GlobalAppID: app.ID, Generation: 1}}
		}
		if receipt, ok := receipts[r.URL.Path]; ok {
			return delegationReply(r, 200, receipt), nil
		}
		return delegationReply(r, 404, nil), nil
	})}
	id := uuid.NewString()
	if _, err := s.PlanRelease(t.Context(), owner, app.Slug, id, releaseTestImage); err != nil {
		t.Fatal(err)
	}
	r := Reconciler{Store: s, Verifier: service.Verifier, Delegations: service}
	first, err := r.Resume(t.Context(), owner, app.Slug, id, map[string]string{targets[0].Region: admin, targets[1].Region: admin})
	if err != nil || first.Status() != ReleasePending || grantPuts.Load() != 2 || imagePuts.Load() != 1 {
		t.Fatalf("partial: %+v %v grants=%d images=%d", first, err, grantPuts.Load(), imagePuts.Load())
	}
	// Construct a new service with only persisted keys and journal.
	restarted := *service
	if err := (Recovery{Service: &restarted}).Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.GetRelease(t.Context(), owner, app.Slug, id)
	if err != nil || recovered.Status() != ReleaseAccepted || grantPuts.Load() != 2 || imagePuts.Load() != 2 {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
}
func TestDelegationRecoveryTransientFailureAndRetirement(t *testing.T) {
	s := NewMemoryStore()
	owner := uuid.NewString()
	app, targets := releaseSetup(t, s, owner)
	keys := delegationKeys(t)
	g, err := keys.Mint(app, targets[0])
	if err != nil {
		t.Fatal(err)
	}
	g, err = s.EnsureDelegation(t.Context(), owner, app.Slug, g)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	service := &DelegationService{Store: s, Journal: s, Keys: keys, Authorize: func(context.Context, string) error { return errors.New("account backend unavailable") }}
	service.Verifier.Client = &http.Client{Transport: delegationTransport(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return delegationReply(r, 401, nil), nil
	})}
	w := Recovery{Service: service}
	if err := w.grant(t.Context(), g); err == nil {
		t.Fatal("backend failure ignored")
	}
	retained, _ := s.ReadDelegation(t.Context(), owner, app.ID, g.ID)
	if retained.Status != DelegationPending || requests.Load() != 0 {
		t.Fatal("transient failure retired consent or dispatched")
	}
	service.Authorize = func(context.Context, string) error { return ErrDelegationOwnerInactive }
	if err := w.grant(t.Context(), g); !errors.Is(err, ErrDelegationUnavailable) {
		t.Fatalf("401 should remain retryable: %v", err)
	}
	retained, _ = s.ReadDelegation(t.Context(), owner, app.ID, g.ID)
	if retained.Status != DelegationRevoking || len(retained.Sealed) == 0 {
		t.Fatal("401 erased durable revocation")
	}
	// Revocation keeps using its immutable original endpoint after registry changes.
	if _, err := s.PutCell(t.Context(), Cell{ID: targets[0].CellID, Region: targets[0].Region, APIBase: "https://replacement.example.com", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	service.Verifier.Client = &http.Client{Transport: delegationTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != strings.TrimPrefix(g.APIBase, "https://") || r.Method != http.MethodDelete {
			t.Error("revocation redirected to replacement origin")
		}
		return delegationReply(r, 200, api.RegionalReleaseGrantResponse{GrantID: g.ID, CellID: g.Binding.CellID, RegionalAppID: g.Binding.RegionalAppID, GlobalAppID: g.GlobalAppID, ExpiresAt: g.ExpiresAt, Status: DelegationRevoked}), nil
	})}
	if err := service.Revoke(t.Context(), retained); err != nil {
		t.Fatal(err)
	}
	retained, _ = s.ReadDelegation(t.Context(), owner, app.ID, g.ID)
	if retained.Status != DelegationRevoked || len(retained.Sealed) != 0 {
		t.Fatal("positive revocation did not erase cipher")
	}
}
func TestRecoveryCursorFairness(t *testing.T) {
	s := NewMemoryStore()
	owner := uuid.NewString()
	app, _ := releaseSetup(t, s, owner)
	s.mu.Lock()
	for i := range api.GlobalRecoveryDelegationsPerApp + 1 {
		id := fmt.Sprintf("%08x-0000-4000-8000-000000000001", i+1)
		s.delegations[id] = Delegation{ID: id, GlobalAppID: app.ID, OwnerID: owner, Slug: app.Slug, Status: DelegationRevoking, ExpiresAt: time.Now().Add(-time.Second), Sealed: []byte("erase at expiry")}
	}
	s.wake(app)
	s.mu.Unlock()
	service := &DelegationService{Store: s, Journal: s, Keys: delegationKeys(t)}
	if err := (Recovery{Service: service}).Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	remaining, err := s.ListRecoveryDelegations(t.Context(), owner, app.ID)
	if err != nil || len(remaining) != 1 {
		t.Fatalf("bounded pass: %d %v", len(remaining), err)
	}
	s.mu.Lock()
	s.wake(app)
	s.mu.Unlock()
	if err := (Recovery{Service: service}).Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	remaining, err = s.ListRecoveryDelegations(t.Context(), owner, app.ID)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("cursor skipped retained grant: %d %v", len(remaining), err)
	}
}

func TestDelegationRevokedConsentRequiresFreshAdminIdentity(t *testing.T) {
	s := NewMemoryStore()
	owner := uuid.NewString()
	app, targets := releaseSetup(t, s, owner)
	keys := delegationKeys(t)
	g, err := keys.Mint(app, targets[0])
	if err != nil {
		t.Fatal(err)
	}
	g, err = s.EnsureDelegation(t.Context(), owner, app.Slug, g)
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.PlanRelease(t.Context(), owner, app.Slug, uuid.NewString(), releaseTestImage)
	if err != nil {
		t.Fatal(err)
	}
	admin, _, _ := api.GenerateAPIKey()
	var puts atomic.Int32
	service := &DelegationService{Store: s, Journal: s, Keys: keys, Authorize: func(context.Context, string) error { return nil }}
	service.Verifier.Client = &http.Client{Transport: delegationTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/binding") {
			b := targets[0].Binding
			return delegationReply(r, 200, api.CellAppBindingResponse{CellID: b.CellID, Region: b.Region, AccountID: b.RegionalAccountID, AppID: b.RegionalAppID, AppSlug: b.RegionalAppSlug}), nil
		}
		if r.Method == http.MethodGet {
			return delegationReply(r, 401, nil), nil
		}
		if r.Method != http.MethodPut || r.Header.Get("Authorization") != "Bearer "+admin {
			t.Error("renewal lacks explicit admin consent")
		}
		puts.Add(1)
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		stored, err := s.ReadDelegation(r.Context(), owner, app.ID, id)
		if err != nil {
			return nil, err
		}
		status := DelegationActive
		if id == g.ID {
			status = DelegationRevoked
		}
		return delegationReply(r, 200, api.RegionalReleaseGrantResponse{GrantID: id, CellID: stored.Binding.CellID, RegionalAppID: stored.Binding.RegionalAppID, GlobalAppID: app.ID, ExpiresAt: stored.ExpiresAt, Status: status}), nil
	})}
	var target ReleaseTarget
	for _, t := range release.Targets {
		if t.Region == targets[0].Region {
			target = t
		}
	}
	if _, err := service.Credential(t.Context(), owner, app.Slug, release, target, ""); !errors.Is(err, ErrDelegationUnavailable) || puts.Load() != 0 {
		t.Fatalf("automatic renewal: %v", err)
	}
	token, err := service.Credential(t.Context(), owner, app.Slug, release, target, admin)
	if err != nil || !api.ValidRegionalReleaseTokenFormat(token) || puts.Load() != 2 {
		t.Fatalf("explicit renewal: %v puts=%d", err, puts.Load())
	}
	old, _ := s.ReadDelegation(t.Context(), owner, app.ID, g.ID)
	current, err := s.CurrentDelegation(t.Context(), owner, app.Slug, targets[0].Region)
	if err != nil || current.ID == g.ID || old.Status != DelegationRevoked || len(old.Sealed) != 0 {
		t.Fatalf("old consent revived: %v", err)
	}
}
