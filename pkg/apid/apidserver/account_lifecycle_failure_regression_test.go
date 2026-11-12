package apidserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

var lifecycleStorageFailure = errors.New("temporary account storage failure")

type lifecycleFaults struct {
	mu           sync.Mutex
	readAccount  string
	failGrace    bool
	readFailures int
}

func (f *lifecycleFaults) readFails(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readAccount != id || id == "" {
		return false
	}
	f.readAccount = ""
	f.readFailures++
	return true
}

type lifecycleMemFault struct {
	MemStore *state.MemStore
	faults   *lifecycleFaults
}

func (s *lifecycleMemFault) AccountByID(ctx context.Context, id string) (state.Account, error) {
	if s.faults.readFails(id) {
		return state.Account{}, lifecycleStorageFailure
	}
	return s.MemStore.AccountByID(ctx, id)
}

func (s *lifecycleMemFault) SetAccountKeyGraceWindow(ctx context.Context, id string, days *int) error {
	s.faults.mu.Lock()
	fail := s.faults.failGrace
	s.faults.mu.Unlock()
	if fail {
		return lifecycleStorageFailure
	}
	return s.MemStore.SetAccountKeyGraceWindow(ctx, id, days)
}

type lifecyclePgFault struct {
	PgStore *state.PgStore
	faults  *lifecycleFaults
}

func (s *lifecyclePgFault) AccountByID(ctx context.Context, id string) (state.Account, error) {
	if s.faults.readFails(id) {
		return state.Account{}, lifecycleStorageFailure
	}
	return s.PgStore.AccountByID(ctx, id)
}

func (s *lifecyclePgFault) SetAccountKeyGraceWindow(ctx context.Context, id string, days *int) error {
	s.faults.mu.Lock()
	fail := s.faults.failGrace
	s.faults.mu.Unlock()
	if fail {
		return lifecycleStorageFailure
	}
	return s.PgStore.SetAccountKeyGraceWindow(ctx, id, days)
}

type lifecycleFixture struct {
	store    state.Store
	account  state.Account
	handler  http.Handler
	do       func(*testing.T, string, string, any, map[string]string) *httptest.ResponseRecorder
	setStore func(state.Store)
	provider func(billing.Provider)
}

func lifecycleSetup(t *testing.T, backend string) lifecycleFixture {
	t.Helper()
	if backend == "memory" {
		e := apidserver.Setup(t, api.PlanPro)
		return lifecycleFixture{store: *e.Store(), account: *e.Acct(), handler: *e.H(), do: e.Do,
			setStore: func(store state.Store) { *(*e.S()).Store() = store },
			provider: func(provider billing.Provider) { (*e.S()).WithBillingProvider(provider) }}
	}
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	return lifecycleFixture{store: *e.Store(), account: *e.Acct(), handler: *e.H(), do: e.Do,
		setStore: func(store state.Store) { *(*e.S()).Store() = store },
		provider: func(provider billing.Provider) { (*e.S()).WithBillingProvider(provider) }}
}

func lifecycleWrap(t *testing.T, fixture lifecycleFixture, faults *lifecycleFaults) {
	t.Helper()
	switch store := fixture.store.(type) {
	case *state.MemStore:
		fixture.setStore(statetest.NewMem(&lifecycleMemFault{MemStore: store, faults: faults}))
	case *state.PgStore:
		fixture.setStore(statetest.NewPg(&lifecyclePgFault{PgStore: store, faults: faults}))
	default:
		t.Fatal("unsupported test store")
	}
}

func TestGraceWindowStorageFailureIsRetryableAndNotAuditedAsSuccess(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			fixture := lifecycleSetup(t, backend)
			prior, zero := 14, 0
			if err := fixture.store.SetAccountKeyGraceWindow(t.Context(), fixture.account.ID, &prior); err != nil {
				t.Fatal(err)
			}
			faults := &lifecycleFaults{failGrace: true}
			lifecycleWrap(t, fixture, faults)
			path := "/v1/account/keys/grace_window_days"
			rr := fixture.do(t, http.MethodPatch, path, api.SetGraceWindowRequest{Days: &zero}, nil)
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatal(rr.Code, rr.Body.String())
			}
			if days, err := fixture.store.GetAccountKeyGraceWindow(t.Context(), fixture.account.ID); err != nil || days == nil || *days != prior {
				t.Fatal("failed update changed the saved policy", days, err)
			}
			events, err := fixture.store.ListEvents(t.Context(), fixture.account.ID, 0)
			if err != nil || apidserver.FindEventByKind(events, "key.grace_window_set") != nil {
				t.Fatal("failed update emitted a success event", err)
			}
			faults.mu.Lock()
			faults.failGrace = false
			faults.mu.Unlock()
			rr = fixture.do(t, http.MethodPatch, path, api.SetGraceWindowRequest{Days: &zero}, nil)
			if rr.Code != http.StatusOK {
				t.Fatal("retry after storage recovery failed", rr.Code, rr.Body.String())
			}
			if days, err := fixture.store.GetAccountKeyGraceWindow(t.Context(), fixture.account.ID); err != nil || days == nil || *days != zero {
				t.Fatal("successful retry did not save atomic rotation", days, err)
			}
		})
	}
}

func TestBillingContactLookupFailuresBlockRemovalAndDeletion(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			for _, operation := range []string{"remove", "delete"} {
				t.Run(operation, func(t *testing.T) {
					fixture := lifecycleSetup(t, backend)
					store := fixture.store
					created, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.com", Plan: api.PlanPro})
					if err != nil {
						t.Fatal(err)
					}
					member := created.Account
					org, err := store.CreateOrg(t.Context(), state.Org{Slug: "contact-" + uuid.NewString()[:8], Name: "Contact", Plan: api.PlanPro})
					if err != nil {
						t.Fatal(err)
					}
					for _, m := range []struct {
						id   string
						role state.OrgRole
					}{{fixture.account.ID, state.OrgRoleOwner}, {member.ID, state.OrgRoleBilling}} {
						if err := store.AddOrgMember(t.Context(), org.ID, m.id, m.role, nil); err != nil {
							t.Fatal(err)
						}
					}
					writer := store.(interface {
						UpdateOrgBillingEmail(context.Context, string, string) error
					})
					if err := writer.UpdateOrgBillingEmail(t.Context(), org.ID, member.Email); err != nil {
						t.Fatal(err)
					}
					if err := store.UpdateAccountProviderCustomerID(t.Context(), org.ID, uuid.NewString()); err != nil {
						t.Fatal(err)
					}
					provider, err := polar.NewProvider(polar.Config{APIKey: "offline-contact-test", WebhookSecret: orgSubjectWebhookSecret, BaseURL: "http://127.0.0.1:1"}, nil)
					if err != nil {
						t.Fatal(err)
					}
					fixture.provider(provider)
					faults := &lifecycleFaults{}
					lifecycleWrap(t, fixture, faults)
					var request func() *httptest.ResponseRecorder
					if operation == "remove" {
						faults.readAccount = member.ID
						request = func() *httptest.ResponseRecorder {
							return fixture.do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+member.ID, nil, map[string]string{"X-Active-Org": org.Slug})
						}
					} else {
						faults.readAccount = org.ID
						plain, hash, err := api.GenerateAPIKey()
						if err != nil {
							t.Fatal(err)
						}
						if _, err := store.CreateAPIKey(t.Context(), member.ID, hash, "deletion-test", api.ScopesAdminOnly); err != nil {
							t.Fatal(err)
						}
						request = func() *httptest.ResponseRecorder {
							req := httptest.NewRequest(http.MethodDelete, "/v1/account", strings.NewReader(`{}`))
							req.Header.Set("Authorization", "Bearer "+plain)
							req.Header.Set("Content-Type", "application/json")
							rr := httptest.NewRecorder()
							fixture.handler.ServeHTTP(rr, req)
							return rr
						}
					}
					if rr := request(); rr.Code != http.StatusServiceUnavailable {
						t.Fatal("lookup failure was not retryable", rr.Code, rr.Body.String())
					}
					faults.mu.Lock()
					failures := faults.readFailures
					faults.mu.Unlock()
					if failures != 1 {
						t.Fatal("fault did not reach the guard", failures)
					}
					if rr := request(); rr.Code != http.StatusConflict {
						t.Fatal("healthy billing contact guard was bypassed", rr.Code, rr.Body.String())
					}
					membership, err := store.OrgMemberByAccount(t.Context(), org.ID, member.ID)
					if err != nil || membership.RemovedAt != nil {
						t.Fatal("refusal removed the member", err)
					}
					account, err := store.AccountByID(t.Context(), member.ID)
					if err != nil || account.Status != state.AccountActive {
						t.Fatal("refusal scheduled account deletion", err)
					}
					if err := writer.UpdateOrgBillingEmail(t.Context(), org.ID, "finance@example.com"); err != nil {
						t.Fatal(err)
					}
					rr := request()
					if operation == "remove" && rr.Code != http.StatusNoContent || operation == "delete" && rr.Code != http.StatusOK {
						t.Fatal("mutation after moving the contact failed", rr.Code, rr.Body.String())
					}
				})
			}
		})
	}
}

func TestPostgresSignupCanRetryAfterCredentialStorageFailure(t *testing.T) {
	for _, path := range []string{"/signup", "/v1/auth/signup"} {
		t.Run(path, func(t *testing.T) {
			e := apidserver.SetupPGHandler(t, api.PlanFree)
			pool, store := *e.Pool(), *e.Store()
			const failPassword = `CREATE FUNCTION reject_signup_password() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'test credential storage failure'; END $$;
CREATE TRIGGER reject_signup_password BEFORE INSERT ON account_passwords FOR EACH ROW EXECUTE FUNCTION reject_signup_password();`
			if _, err := pool.Exec(t.Context(), failPassword); err != nil {
				t.Fatal(err)
			}
			email := uuid.NewString() + "@example.com"
			body := map[string]string{"email": email, "password": "Valid test password for signup retry"}
			headers := map[string]string{"Content-Type": "application/json"}
			if rr := e.Do(t, http.MethodPost, path, body, headers); rr.Code != http.StatusInternalServerError {
				t.Fatal(rr.Code, rr.Body.String())
			}
			if _, err := store.AccountByEmail(t.Context(), email); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("failed signup reserved the address", err)
			}
			if _, err := pool.Exec(t.Context(), `DROP TRIGGER reject_signup_password ON account_passwords`); err != nil {
				t.Fatal(err)
			}
			if rr := e.Do(t, http.MethodPost, path, body, headers); rr.Code != http.StatusOK {
				t.Fatal("signup retry failed", rr.Code, rr.Body.String())
			}
			created, err := store.AccountByEmail(t.Context(), email)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.AccountPasswordByAccountID(t.Context(), created.ID); err != nil {
				t.Fatal("successful signup has no credential", err)
			}
			if rr := e.Do(t, http.MethodPost, path, body, headers); rr.Code != http.StatusOK {
				t.Fatal("same-password signup retry stopped working", rr.Code, rr.Body.String())
			}
		})
	}
}
