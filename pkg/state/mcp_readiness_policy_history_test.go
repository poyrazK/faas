package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessPolicyHistoryAndRestore(t *testing.T) {
	check := func(t *testing.T, s state.Store) {
		ctx := t.Context()
		account, e := s.CreateAccount(ctx, "history@example.com", api.PlanPro)
		if e != nil {
			t.Fatal(e)
		}
		app, e := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "policy-history", Type: state.AppTypeApp, Status: state.AppActive})
		if e != nil {
			t.Fatal(e)
		}
		d, e := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:history", TrafficPercentExplicit: true})
		if e != nil {
			t.Fatal(e)
		}
		if e = s.MarkDeploymentLive(ctx, d.ID); e != nil {
			t.Fatal(e)
		}
		h := s.(state.MCPReadinessPolicyHistoryStore)
		request := api.SetMCPReadinessPolicyRequest{Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "original reviewed policy", Roles: []api.MCPReadinessRole{{Name: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("a", 64)}}}
		old, e := h.SetMCPReadinessPolicy(ctx, account.ID, app.ID, request)
		if e != nil {
			t.Fatal(e)
		}
		request.ExpectedRevision = old.Revision
		request.Roles[0].CatalogSHA256 = strings.Repeat("b", 64)
		request.Reason = "reviewed catalog update"
		current, e := h.SetMCPReadinessPolicy(ctx, account.ID, app.ID, request)
		if e != nil {
			t.Fatal(e)
		}
		request.Scope = "staging"
		request.ExpectedRevision = ""
		request.Reason = "staging independent policy"
		staging, e := h.SetMCPReadinessPolicy(ctx, account.ID, app.ID, request)
		if e != nil {
			t.Fatal(e)
		}
		subject, e := h.MCPReadinessSubject(ctx, account.ID, d.ID)
		if e != nil {
			t.Fatal(e)
		}
		proof := api.MCPReadinessEvidence{Role: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("b", 64), Passed: true}
		if _, e = h.RecordMCPReadiness(ctx, subject, proof); e != nil {
			t.Fatal(e)
		}
		override, e := h.CreateMCPReadinessOverride(ctx, account.ID, d.ID, api.MCPReadinessOverrideRequest{ExpectedRevision: current.Revision, Reason: "before restore approval"})
		if e != nil {
			t.Fatal(e)
		}
		restore := api.RestoreMCPReadinessPolicyRequest{ExpectedRevision: current.Revision, Reason: "restore reviewed original catalog"}
		if _, e = h.RestoreMCPReadinessPolicy(ctx, account.ID, app.ID, staging.Revision, restore); !errors.Is(e, state.ErrNotFound) {
			t.Fatal("cross-scope restore", e)
		}
		stale := restore
		stale.ExpectedRevision = old.Revision
		if _, e = h.RestoreMCPReadinessPolicy(ctx, account.ID, app.ID, old.Revision, stale); !errors.Is(e, state.ErrConflict) {
			t.Fatal("stale CAS restore", e)
		}
		restored, e := h.RestoreMCPReadinessPolicy(ctx, account.ID, app.ID, old.Revision, restore)
		if e != nil {
			t.Fatal(e)
		}
		if restored.Revision == old.Revision || restored.Revision == current.Revision || restored.RestoredFrom != old.Revision || restored.UpdatedBy != account.ID || restored.Reason != restore.Reason || restored.Roles[0].CatalogSHA256 != old.Roles[0].CatalogSHA256 {
			t.Fatal("restore reused audit or configuration", restored)
		}
		after, e := h.GetMCPReadinessPolicy(ctx, account.ID, app.ID, "staging")
		if e != nil || after.Revision != staging.Revision {
			t.Fatal("restore changed staging", after, e)
		}
		if _, e = h.RecordMCPReadiness(ctx, subject, proof); !errors.Is(e, state.ErrConflict) {
			t.Fatal("pre-restore in-flight proof accepted", e)
		}
		// Prior evidence and an unused emergency approval must not admit traffic.
		if pg, ok := s.(*state.PgStore); ok {
			_, e = (*pg.Pool()).Exec(ctx, `UPDATE deployments SET traffic_percent=10 WHERE id=$1`, d.ID)
		} else {
			m := s.(*state.MemStore)
			(*m.Mu()).Lock()
			e = m.CheckMCPReadinessLocked((*m.Deployments())[d.ID], 10)
			(*m.Mu()).Unlock()
		}
		if !state.IsMCPReadinessRequired(e) {
			t.Fatal("restore reused evidence or emergency override", override.ID, e)
		}
		historical, e := h.GetMCPReadinessPolicyRevision(ctx, account.ID, app.ID, "default", old.Revision)
		if e != nil || historical.Reason != old.Reason || historical.Revision != old.Revision {
			t.Fatal("old audit was mutated", historical, e)
		}
		page, e := h.ListMCPReadinessPolicyHistory(ctx, account.ID, app.ID, "default", "", 1)
		if e != nil || len(page.Policies) != 1 || page.Policies[0].Revision != restored.Revision || page.NextBeforeRevision != restored.Revision {
			t.Fatal("first page", page, e)
		}
		// A new write between page requests must not duplicate or skip older rows.
		if _, e = h.RestoreMCPReadinessPolicy(ctx, account.ID, app.ID, old.Revision, api.RestoreMCPReadinessPolicyRequest{ExpectedRevision: restored.Revision, Reason: "second reviewed restore"}); e != nil {
			t.Fatal(e)
		}
		next, e := h.ListMCPReadinessPolicyHistory(ctx, account.ID, app.ID, "default", page.NextBeforeRevision, 1)
		if e != nil || len(next.Policies) != 1 || next.Policies[0].Revision != current.Revision || next.NextBeforeRevision != current.Revision {
			t.Fatal("second page", next, e)
		}
		last, e := h.ListMCPReadinessPolicyHistory(ctx, account.ID, app.ID, "default", next.NextBeforeRevision, 1)
		if e != nil || len(last.Policies) != 1 || last.Policies[0].Revision != old.Revision || last.NextBeforeRevision != "" {
			t.Fatal("last page", last, e)
		}
		if _, e = h.ListMCPReadinessPolicyHistory(ctx, account.ID, app.ID, "default", staging.Revision, 1); !errors.Is(e, state.ErrNotFound) {
			t.Fatal("cross-scope cursor accepted", e)
		}
		if _, e = h.GetMCPReadinessPolicyRevision(ctx, account.ID, app.ID, "staging", old.Revision); !errors.Is(e, state.ErrNotFound) {
			t.Fatal("cross-scope read", e)
		}
		foreign, e := s.CreateAccount(ctx, "history-foreign@example.com", api.PlanPro)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = h.ListMCPReadinessPolicyHistory(ctx, foreign.ID, app.ID, "default", "", 10); !errors.Is(e, state.ErrNotFound) {
			t.Fatal("foreign history leaked", e)
		}
		if _, e = h.RestoreMCPReadinessPolicy(ctx, foreign.ID, app.ID, old.Revision, restore); !errors.Is(e, state.ErrNotFound) {
			t.Fatal("foreign restore", e)
		}
		other, e := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "other-history", Type: state.AppTypeApp, Status: state.AppActive})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = h.GetMCPReadinessPolicyRevision(ctx, account.ID, other.ID, "default", old.Revision); !errors.Is(e, state.ErrNotFound) {
			t.Fatal("cross-app history", e)
		}
		if _, e = h.ListMCPReadinessPolicyHistory(ctx, account.ID, app.ID, "default", "", 51); !errors.Is(e, state.ErrInvalidArgument) {
			t.Fatal("unbounded history", e)
		}
		if _, e = h.GetMCPReadinessPolicyRevision(ctx, account.ID, app.ID, "default", uuid.NewString()); !errors.Is(e, state.ErrNotFound) {
			t.Fatal("unknown revision", e)
		}
		fresh, e := h.MCPReadinessSubject(ctx, account.ID, d.ID)
		if e != nil {
			t.Fatal(e)
		}
		proof.CatalogSHA256 = old.Roles[0].CatalogSHA256
		if _, e = h.RecordMCPReadiness(ctx, fresh, proof); e != nil {
			t.Fatal("fresh restored proof rejected", e)
		}
		if pg, ok := s.(*state.PgStore); ok {
			_, e = (*pg.Pool()).Exec(ctx, `UPDATE deployments SET traffic_percent=10 WHERE id=$1`, d.ID)
		} else {
			m := s.(*state.MemStore)
			(*m.Mu()).Lock()
			e = m.CheckMCPReadinessLocked((*m.Deployments())[d.ID], 10)
			(*m.Mu()).Unlock()
		}
		if e != nil {
			t.Fatal("fresh restored proof did not admit traffic", e)
		}

	}
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}
