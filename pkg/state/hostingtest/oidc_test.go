package hostingtest

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func hostingOIDCLifecycle(t *testing.T, store hostingTestStore) {
	// adr: 202610171028 — immutable explicit trust, separate bounded sessions and immediate revocation.
	account, site := seedDurable(t, store)
	ctx := t.Context()
	rules := api.HostingOIDCTrustRules{RepositoryOwnerID: "123", RepositoryID: "456", Repository: "org/site", Ref: "refs/heads/main", WorkflowRef: "org/site/.github/workflows/preview.yml@refs/heads/main", EventName: "push"}
	trust, err := store.CreateHostingOIDCTrust(ctx, api.HostingOIDCTrust{ID: uuid.NewString(), AccountID: account.ID, SiteID: site.ID, HostingOIDCTrustRules: rules, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	credential := func() api.HostingAutomationCredential {
		_, hash, e := api.GenerateHostingAutomationToken()
		if e != nil {
			t.Fatal(e)
		}
		return api.HostingAutomationCredential{ID: uuid.NewString(), AccountID: account.ID, SiteID: site.ID, OIDCTrustID: trust.ID, Label: "GitHub OIDC", Operations: []string{"stage", "preview"}, Hash: hash, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	}
	c := credential()
	if _, err = store.CreateHostingAutomationCredential(ctx, c); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("manual issuance bypassed verifier", err)
	}
	if _, err = store.CreateHostingOIDCCredential(ctx, trust, c); err != nil {
		t.Fatal(err)
	}
	_, key, err := store.AuthenticateHostingAutomationToken(ctx, c.Hash)
	if err != nil || key.HostingOIDCTrustID != trust.ID || key.HostingSiteID != site.ID {
		t.Fatal(key, err)
	}
	if err = store.RevokeHostingAutomationCredential(ctx, account.ID, site.ID, c.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("OIDC session exposed as manual credential", err)
	}
	manual := credential()
	manual.OIDCTrustID = ""
	manual.ExpiresAt = now.Add(time.Hour)
	if _, err = store.CreateHostingAutomationCredential(ctx, manual); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListHostingAutomationCredentials(ctx, account.ID, site.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != manual.ID {
		t.Fatal(rows, err)
	}
	changed := trust
	changed.RepositoryID = "999"
	if _, err = store.CreateHostingOIDCCredential(ctx, changed, credential()); err == nil {
		t.Fatal("unverified replacement trust accepted")
	}
	expired := credential()
	expired.CreatedAt = now.Add(-2 * time.Minute)
	expired.ExpiresAt = now.Add(-time.Minute)
	if _, err = store.CreateHostingOIDCCredential(ctx, trust, expired); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired assertion minted session", err)
	}
	var wg sync.WaitGroup
	var accepted atomic.Int64
	for range api.HostingOIDCSessionsPerSite + 6 {
		wg.Go(func() {
			_, e := store.CreateHostingOIDCCredential(ctx, trust, credential())
			if e == nil {
				accepted.Add(1)
			} else if !errors.Is(e, state.ErrHostingQuota) {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != api.HostingOIDCSessionsPerSite-1 {
		t.Fatal("session quota raced", accepted.Load())
	}
	if err = store.RevokeHostingOIDCTrust(ctx, uuid.NewString(), site.ID, trust.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign revoke", err)
	}
	if _, err = store.GetHostingOIDCTrust(ctx, uuid.NewString(), trust.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross site lookup", err)
	}
	if err = store.RevokeHostingOIDCTrust(ctx, account.ID, site.ID, trust.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AuthenticateHostingAutomationToken(ctx, c.Hash); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("revocation cached", err)
	}
	if _, _, err = store.AuthenticateHostingAutomationToken(ctx, manual.Hash); err != nil {
		t.Fatal("manual credential revoked with trust", err)
	}
	if _, err = store.CreateHostingOIDCCredential(ctx, trust, credential()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("revoked trust resurrected", err)
	}
	// Race minting against revocation. Every issued token must be dead after revoke returns.
	trust.ID = uuid.NewString()
	trust, err = store.CreateHostingOIDCTrust(ctx, trust)
	if err != nil {
		t.Fatal(err)
	}
	hashes := make(chan []byte, 12)
	for range 12 {
		wg.Go(func() {
			c := credential()
			_, e := store.CreateHostingOIDCCredential(ctx, trust, c)
			if e == nil {
				hashes <- c.Hash
			} else if !errors.Is(e, state.ErrNotFound) {
				t.Error(e)
			}
		})
	}
	wg.Go(func() {
		if e := store.RevokeHostingOIDCTrust(ctx, account.ID, site.ID, trust.ID); e != nil {
			t.Error(e)
		}
	})
	wg.Wait()
	close(hashes)
	for hash := range hashes {
		if _, _, err = store.AuthenticateHostingAutomationToken(ctx, hash); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("concurrent revoke left a live session", err)
		}
	}
}
