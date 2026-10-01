//go:build !no_pg

// adr: 375
package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgTrafficTenantActivationRefusalAndRepair(t *testing.T) {
	for _, mode := range trafficTenantActivationModes {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			testTrafficTenantActivation(t, store, account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) })
		})
	}
}

func TestPgTrafficTenantMetadataEligibilityAndBounds(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	surface, host := trafficTenantClaim(t, store, account, app, "UPPER.TENANT.EXAMPLE.TEST")
	if err := store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	read := func() trafficHostAnalysis {
		t.Helper()
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
		view, err := readTrafficHostAnalysis(t.Context(), tx, uuidToPgtype(account.ID), store.trafficAppsSuffix)
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	if view := read(); len(view.Tenants) != 0 {
		t.Fatal("unverified hostname entered serving analysis")
	}
	if matched, err := store.MarkTenantHostnameVerifiedIfChallenge(t.Context(), strings.ToLower(host.Hostname), host.ChallengeToken); err != nil || !matched {
		t.Fatalf("challenge verification: matched=%v err=%v", matched, err)
	}
	assertTenant := func(count int, tenantID string) {
		t.Helper()
		view := read()
		if len(view.Tenants) != count {
			t.Fatalf("tenant metadata count=%d want=%d", len(view.Tenants), count)
		}
		if count == 1 && view.Tenants[0] != (trafficHostTenant{Host: strings.ToLower(host.Hostname), App: app.ID, Surface: surface.ID, ID: host.ID, PlatformTenant: tenantID}) {
			t.Fatalf("tenant binding identity=%+v", view.Tenants[0])
		}
		encoded, err := json.Marshal(view)
		if err != nil || strings.Contains(string(encoded), host.ChallengeToken) || strings.Contains(string(encoded), "Traffic private tenant") {
			t.Fatal("skinny metadata disclosed tenant challenge or display name")
		}
	}
	assertTenant(1, "")
	if err := store.WithPublicHostPolicySnapshot(t.Context(), func(reader PublicHostPolicyReader) error {
		lower := strings.ToLower(host.Hostname)
		gotSurface, err := reader.TenantSurfaceByHostname(t.Context(), lower)
		if err != nil || gotSurface.ID != surface.ID {
			t.Fatalf("case-insensitive surface snapshot: id=%q err=%v", gotSurface.ID, err)
		}
		gotHost, err := reader.GetTenantHostnameByName(t.Context(), lower)
		if err != nil || !gotHost.Verified() || gotHost.SurfaceID != surface.ID {
			t.Fatalf("case-insensitive hostname snapshot: verified=%v err=%v", gotHost.Verified(), err)
		}
		binding, err := reader.PlatformTenantHostBinding(t.Context(), lower)
		if err != nil || binding.SurfaceID != surface.ID || !binding.Verified {
			t.Fatalf("case-insensitive binding snapshot: binding=%+v err=%v", binding, err)
		}
		reserved, err := reader.PublicHostReserved(t.Context(), "", lower)
		if err != nil || !reserved {
			t.Fatalf("case-insensitive reservation snapshot: reserved=%v err=%v", reserved, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, status := range []SurfaceStatus{SurfaceStatusPending, SurfaceStatusSuspended, SurfaceStatusDeleted} {
		if err := store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, status); err != nil {
			t.Fatal(err)
		}
		assertTenant(0, "")
		if err := store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
	}
	visibility := api.AppVisibilityInternal
	if _, err := store.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility}); err != nil {
		t.Fatal(err)
	}
	assertTenant(0, "")
	legacy := publicationLegacyRule(account, app, strings.ToLower(host.Hostname))
	seedPgDomainRemovalLegacy(t, pool, legacy)
	visibility = api.AppVisibilityPublic
	_, err := store.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility})
	var aggregate *TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Host != strings.ToLower(host.Hostname) {
		t.Fatalf("app publication omitted tenant binding: %v", err)
	}
	assertTenant(0, "")
	if err := store.DeleteEdgeRule(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetVisibility: true, Visibility: &visibility}); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(t.Context(), account.ID, "metadata-tenant", "Traffic private tenant", account.Plan.ConsumerKeysPerAccount())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(t.Context(), account.ID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	assertTenant(1, tenant.ID)
	if _, err := store.SetPlatformTenantStatus(t.Context(), account.ID, tenant.ID, PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	assertTenant(0, "")
	if _, err := store.SetPlatformTenantStatus(t.Context(), account.ID, tenant.ID, PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	assertTenant(1, tenant.ID)
	row, err := sqlc.New().ReadTrafficHostAnalysis(t.Context(), pool, sqlc.ReadTrafficHostAnalysisParams{
		AccountID: uuidToPgtype(account.ID), MaxInputs: 1, MaxBytes: api.TrafficPolicyMaxAnalysisMetadataBytes, Defaults: []byte(`{}`),
		AppsSuffix: store.trafficAppsSuffix, EnvironmentHostBytes: 6 * api.TrafficPolicyMaxHostnameBytes,
	})
	if err != nil || row.Inputs <= 1 || len(row.Data) != 0 {
		t.Fatalf("tenant metadata exceeded bounded transfer: inputs=%d bytes=%d data=%d err=%v", row.Inputs, row.Bytes, len(row.Data), err)
	}
}

func TestPgTrafficTenantVerificationClaimReplacementDuringLockWait(t *testing.T) {
	for _, mode := range []string{"reused-token", "new-token", "plain"} {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			surface, host := trafficTenantClaim(t, store, account, app, "replacement.tenant.example.test")
			if err := store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, SurfaceStatusActive); err != nil {
				t.Fatal(err)
			}
			_, release, busy, err := store.tryAcquireTrafficPolicySession(t.Context(), uuidToPgtype(account.ID), false)
			if err != nil || busy {
				t.Fatalf("account session: busy=%v err=%v", busy, err)
			}
			defer release(t.Context())
			waiting, application := domainRemovalWaitingStore(t, pool)
			result := make(chan error, 1)
			go func() {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if mode == "plain" {
					result <- waiting.MarkTenantHostnameVerified(ctx, host.Hostname)
					return
				}
				matched, err := waiting.MarkTenantHostnameVerifiedIfChallenge(ctx, host.Hostname, host.ChallengeToken)
				if matched {
					err = errors.New("stale verifier published a replacement hostname row")
				}
				result <- err
			}()
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND state='idle' AND query LIKE '%pg_try_advisory_lock%')`, application).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("verifier did not reach an observed account-session wait")
				case <-time.After(5 * time.Millisecond):
				}
			}
			if err := store.DeleteTenantHostname(t.Context(), host.Hostname); err != nil {
				t.Fatal(err)
			}
			token := host.ChallengeToken
			if mode == "new-token" {
				token = "replacement-private-token"
			}
			replacement, err := store.CreateTenantHostnameIfUnderQuota(t.Context(), CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: host.Hostname, ChallengeToken: token}, api.MustLimitsFor(account.Plan))
			if err != nil || replacement.ID == host.ID {
				t.Fatalf("replacement row: id=%q err=%v", replacement.ID, err)
			}
			release(t.Context())
			err = <-result
			if mode == "plain" && !errors.Is(err, ErrNotFound) || mode != "plain" && err != nil {
				t.Fatalf("stale verification result: %v", err)
			}
			if after, err := store.GetTenantHostnameByName(t.Context(), host.Hostname); err != nil || !reflect.DeepEqual(after, replacement) {
				t.Fatalf("stale verifier changed replacement: err=%v", err)
			}
			if matched, err := waiting.MarkTenantHostnameVerifiedIfChallenge(t.Context(), host.Hostname, token); err != nil || !matched {
				t.Fatalf("single-connection verifier unusable after refusal: matched=%v err=%v", matched, err)
			}
		})
	}
}
