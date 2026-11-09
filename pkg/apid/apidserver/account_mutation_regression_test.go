package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestBillingPatchPreservesOtherRequestFields(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			account, err := store.CreateAccount(t.Context(), "billing-patch@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.com", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, nil, nil, 15*24, "")
			// Both authenticated requests hold this same pre-write snapshot.
			for _, body := range []string{`{"business_name":"  New company  "}`, `{"tax_id":"NEW-TAX"}`, `{"billing_address":"Street"}`, `{"tax_id":""}`} {
				rr := httptest.NewRecorder()
				srv.UpdateAccountBillingInfo(rr, httptest.NewRequest(http.MethodPatch, "/v1/account/billing", strings.NewReader(body)), account)
				if rr.Code != http.StatusOK {
					t.Fatal(rr.Code, rr.Body.String())
				}
			}
			got, err := store.AccountByID(t.Context(), account.ID)
			if err != nil || got.BusinessName != "New company" || got.BillingAddress != "Street" || got.TaxID != "" {
				t.Fatalf("stale request erased another patch: %+v %v", got, err)
			}
		})
	}
}

type restoreFaultStore struct {
	MemStore *state.MemStore
	err      error
}

func (s *restoreFaultStore) RestoreAccount(ctx context.Context, id string) error {
	if s.err != nil {
		return s.err
	}
	return s.MemStore.RestoreAccount(ctx, id)
}

func TestAccountRestoreReportsRetryableFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"timeout", context.DeadlineExceeded, http.StatusServiceUnavailable, api.CodeCapacity},
		{"unavailable", errors.New("database unavailable"), http.StatusServiceUnavailable, api.CodeCapacity},
		{"wrapped conflict", fmt.Errorf("restore: %w", state.ErrConflict), http.StatusConflict, api.CodeAccountNotRestorable},
		{"not found", state.ErrNotFound, http.StatusConflict, api.CodeAccountNotRestorable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			base, account := *e.Store(), *e.Acct()
			if err := base.MarkAccountDeletionPending(t.Context(), account.ID); err != nil {
				t.Fatal(err)
			}
			fault := &restoreFaultStore{MemStore: base, err: tc.err}
			*(*e.S()).Store() = statetest.NewMem(fault)
			rr := e.Do(t, http.MethodPost, "/v1/account/restore", nil, nil)
			var problem api.Problem
			if err := json.Unmarshal(rr.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if rr.Code != tc.status || problem.Code != tc.code {
				t.Fatal(rr.Code, rr.Body.String())
			}
			if tc.status == http.StatusServiceUnavailable {
				if strings.Contains(rr.Body.String(), "Grace expired") {
					t.Fatal("transient failure called permanent")
				}
				if got, err := base.AccountByID(t.Context(), account.ID); err != nil || got.Status != state.AccountDeletedPending {
					t.Fatal("failed restore changed lifecycle", err)
				}
				fault.err = nil
				if retry := e.Do(t, http.MethodPost, "/v1/account/restore", nil, nil); retry.Code != http.StatusOK {
					t.Fatal("retry did not restore account", retry.Code, retry.Body.String())
				}
			}
		})
	}
}

func TestPGOwnershipTransferEnforcesFreeRecipientLimit(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	store, owner := *e.Store(), *e.Acct()
	recipient, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: "transfer-recipient@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := store.(state.SharedOrgCreationStore).CreateSharedOrg(t.Context(), recipient.Account.ID, "recipient-free", "Recipient")
	if err != nil {
		t.Fatal(err)
	}
	incoming, err := store.CreateOrg(t.Context(), state.Org{Slug: "transfer-free", Name: "Transfer", Plan: api.PlanHobby})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), incoming.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), incoming.ID, recipient.Account.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOrgPlan(t.Context(), incoming.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	path := "/v1/orgs/" + incoming.Slug + "/transfer_ownership"
	body := api.TransferOwnershipRequest{NewOwnerAccountID: recipient.Account.ID}
	headers := map[string]string{"X-Active-Org": incoming.Slug}
	rr := e.Do(t, http.MethodPost, path, body, headers)
	var problem api.Problem
	if err := json.Unmarshal(rr.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusForbidden || problem.Code != api.CodeOrgFreeLimitExceeded || !strings.Contains(problem.Detail, "new owner") {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if current, err := store.OrgMemberByAccount(t.Context(), incoming.ID, owner.ID); err != nil || current.Role != state.OrgRoleOwner {
		t.Fatal("refusal demoted owner", err)
	}
	// Making room permits the same transfer through the complete access fence.
	if err := store.UpdateOrgPlan(t.Context(), owned.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	if retry := e.Do(t, http.MethodPost, path, body, headers); retry.Code != http.StatusOK {
		t.Fatal("transfer after capacity freed failed", retry.Code, retry.Body.String())
	}
}
