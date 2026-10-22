//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/financialfixture"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// This is a single-host full-daemon gate. It runs real APID, imaged, schedd,
// VMMD, both gateways and meterd, with a local registry/provider and real PG.
// It deliberately makes no physical two-host or provider-sandbox claim.
func TestFinancialSystemDaemonRecoveryMetal(t *testing.T) {
	if os.Getenv("FAAS_FINANCIAL_SYSTEM_ACCEPTANCE") != "1" {
		t.Skip("opt-in full-daemon financial acceptance")
	}
	if !metalAvailable(t) {
		return
	}
	pool := pgtest.OpenMigrated(t)
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	proxy := postgresfixture.OpenProxy(t, pool, "127.0.0.1", 0)
	proxyDSN, err := e2etest.DatabaseURLViaTCP(pool, proxy.Host, proxy.Port)
	if err != nil {
		t.Fatal(err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)
	base, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", base))
	body := strings.Repeat("funded response\n", 4096)
	deployBase, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", body)
	registry.AddImage("onebox-faas/deploy-base", deployBase)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")
	image, _ := e2etest.HelloImageAboveBase("library/financial-system", body)
	ref := registry.AddImage("library/financial-system", image)
	provider := financialfixture.NewProvider(t, 2000)
	env := append(provider.Env(financial.ComputeMonth(time.Now())), "DATABASE_URL="+proxyDSN)
	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.GatewaydPublic|e2etest.Meterd, env...)
	// Restore database access before Harness.Stop; the latter drains schedd
	// while VMMD is still present. Cleanup order is LIFO.
	t.Cleanup(func() { proxy.SetAvailable(true) })
	s := state.NewPgStore(pool)
	// Keep empty-account activation within one UTC minute, so it never
	// needs invented completeness for a minute the real sampler just closed.
	if until := time.Until(time.Now().UTC().Truncate(time.Minute).Add(time.Minute)); until < 15*time.Second {
		timer := time.NewTimer(until + time.Second)
		defer timer.Stop()
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-timer.C:
		}
	}
	key := h.SeedAccount(t.Context(), api.PlanHobby)
	account := accountIDFromKey(t, t.Context(), pool, key)
	month := financial.ComputeMonth(time.Now())
	for _, p := range []financial.Price{
		{Version: "native-system-compute", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: 50 * api.SecondsPerGBHour},
		{Version: "native-system-egress", Meter: "egress", Currency: "USD", Unit: "interface_bytes", UnitQuantity: 1 << 30, MillicentsPerUnit: 2000, IncludedQuantity: 1 << 30},
	} {
		if _, err := s.PutFinancialPriceSnapshot(t.Context(), state.FinancialPriceSnapshot{AccountID: account, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: month, Plan: api.PlanHobby, DeliveryMode: "live", Price: p}); err != nil {
			t.Fatal(err)
		}
	}
	// Empty-account activation exercises the real readiness proofs. No test
	// registers a fake owner or manufactures sampling completeness.
	financialSystemWait(t, 10*time.Second, "current native financial owner", func() bool {
		ok, err := s.FinancialComputeReady(t.Context(), account, "")
		eg, egErr := s.FinancialEgressReady(t.Context(), account)
		return err == nil && egErr == nil && ok && eg
	})
	releaseStrictBudgetsForSystem(t, s)
	spec := financial.BudgetSpec{Name: "native shared account cap", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute", "egress"}, Basis: "net_usage", LimitMillicents: 20000, NotifyMillicents: []int64{}, Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", Enabled: true}
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: spec}, map[string]string{"Idempotency-Key": uuid.NewString()})
	var budget api.FinancialBudgetResponse
	if status != http.StatusCreated || json.Unmarshal(raw, &budget) != nil || !budget.EnforcementReady {
		t.Fatalf("strict account activation: %d %s", status, raw)
	}
	var apps []state.App
	for _, slug := range []string{"financial-system-a", "financial-system-b"} {
		falsy := false
		raw, status = doReq(t, h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy, MaxConcurrency: 1})
		if status != http.StatusCreated {
			t.Fatalf("create app: %d %s", status, raw)
		}
		app, err := s.AppBySlug(t.Context(), slug)
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
		raw, status = doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
		var dep api.DeploymentResponse
		if status != http.StatusAccepted || json.Unmarshal(raw, &dep) != nil {
			t.Fatalf("deploy under strict budget: %d %s", status, raw)
		}
		if _, err := e2etest.WaitForDeploymentLive(t.Context(), t, pool, dep.ID, 10*time.Minute); err != nil {
			h.DumpLogs(t)
			t.Fatal(err)
		}
	}
	request := func(t *testing.T, slug string) int {
		t.Helper()
		got, status := doGetWithHost(t, &http.Client{}, h.GatewayPublicURL+"/", slug+".apps.test.example", 30*time.Second)
		if status == http.StatusOK && string(got) != body {
			t.Fatalf("response body mismatch: %d bytes", len(got))
		}
		return status
	}
	for _, app := range apps {
		if got := request(t, app.Slug); got != http.StatusOK {
			t.Fatalf("funded public response: %d", got)
		}
	}
	instances, err := e2etest.WaitForInstanceState(t.Context(), t, pool, apps[1].ID, state.StateRunning, 30*time.Second)
	if err != nil || len(instances) != 1 {
		t.Fatalf("single running instance: %+v %v", instances, err)
	}
	instance := instances[0].ID
	lease := financialSystemLease(t, s, account, apps[1].ID, instance)
	if !lease.EgressFunded || !lease.AccountFunded || lease.EgressGranted == 0 {
		t.Fatalf("unfunded native publication: %+v", lease)
	}
	record := financialSystemNativeRecord(t, instance)
	if record.PID <= 0 || !record.Authorized || record.ResourcesRemoved {
		t.Fatalf("missing physical owner: %+v", record)
	}
	beforeCounter, err := netns.ReadVethRXBytes(record.Lease.VethHost)
	if err != nil {
		t.Fatal(err)
	}
	for range 12 {
		if got := request(t, apps[1].Slug); got != http.StatusOK {
			t.Fatalf("funded traffic: %d", got)
		}
	}
	counter, err := netns.ReadVethRXBytes(record.Lease.VethHost)
	lease = financialSystemLease(t, s, account, apps[1].ID, instance)
	if err != nil || counter < beforeCounter+uint64(12*len(body)) || counter > uint64(lease.EgressGranted) {
		t.Fatalf("physical bytes=%d grant=%d err=%v", counter, lease.EgressGranted, err)
	}
	if !t.Run("remote_http_variants", func(t *testing.T) {
		financialRemoteVariants(t, h, record, body, func(t *testing.T) financial.ComputeLease {
			return financialSystemLease(t, s, account, apps[1].ID, instance)
		})
	}) {
		return
	}
	// Actual meterd sampling must observe this physical attempt; fixture
	// rows cannot satisfy this predicate. Compute closes at a UTC minute.
	keepaliveAt := time.Now()
	financialSystemWait(t, 2*time.Minute, "meterd samples funded compute and interface bytes", func() bool {
		if time.Now().After(keepaliveAt) {
			if got := request(t, apps[1].Slug); got != http.StatusOK {
				t.Fatalf("live metering request: %d", got)
			}
			keepaliveAt = time.Now().Add(5 * time.Second)
		}
		var mb, eg int64
		if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(mb_seconds),0),COALESCE(sum(net_tx_bytes),0) FROM usage_minutes WHERE instance_id=$1`, instance).Scan(&mb, &eg); err != nil {
			t.Fatal(err)
		}
		return mb > 0 && eg > 0
	})
	if !t.Run("database_outage", func(t *testing.T) {
		if err := h.KillSchedd(); err != nil {
			t.Fatal(err)
		}
		proxy.SetAvailable(false) // all real daemon pools, independent observer stays up
		lease = financialSystemLease(t, s, account, apps[1].ID, instance)
		if !t.Run("remote_exhaustion", func(t *testing.T) {
			financialRemoteExhaustion(t, h, record, lease, body)
		}) {
			return
		}
		financialSystemWait(t, time.Until(lease.FundedUntil)+2*time.Second, "physical retirement within funded teardown", func() bool {
			r := financialSystemNativeRecord(t, instance)
			return r.ResourcesRemoved && financialSystemPhysicalAbsent(r)
		})
		if time.Now().After(lease.FundedUntil.Add(2 * time.Second)) {
			t.Fatal("unfunded physical tail")
		}
		current := financialSystemLease(t, s, account, apps[1].ID, instance)
		if current.SettledAt != nil || current.EgressGranted != lease.EgressGranted || current.Reserved != lease.Reserved {
			t.Fatal("outage refunded or reissued uncertain authority")
		}
		proxy.SetAvailable(true)
		if err := h.RestartVMMD(); err != nil {
			t.Fatal(err)
		}
		financialSystemWait(t, 45*time.Second, "replacement owner settles exact old attempt", func() bool { return financialSystemLease(t, s, account, apps[1].ID, instance).SettledAt != nil })
		if err := h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
	}) {
		return
	}
	t.Run("owner_crash", func(t *testing.T) {
		if got := request(t, apps[1].Slug); got != http.StatusOK {
			t.Fatalf("request after recovery: %d", got)
		}
		live, err := e2etest.WaitForInstanceState(t.Context(), t, pool, apps[1].ID, state.StateRunning, 30*time.Second)
		if err != nil || len(live) != 1 {
			t.Fatalf("replacement instance: %+v %v", live, err)
		}
		old := financialSystemLease(t, s, account, apps[1].ID, live[0].ID)
		physical := financialSystemNativeRecord(t, old.InstanceID)
		if err := h.KillSchedd(); err != nil {
			t.Fatal(err)
		}
		if err := h.RestartVMMD(); err != nil {
			t.Fatal(err)
		}
		financialSystemWait(t, 45*time.Second, "crashed generation physically retired and settled", func() bool {
			return financialSystemPhysicalAbsent(physical) && financialSystemLease(t, s, account, apps[1].ID, old.InstanceID).SettledAt != nil
		})
		if err := h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
		t.Run("remote_after_restart", func(t *testing.T) {
			client := financialRemoteDaemonClient(t, h)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			response := financialRemoteRequest(ctx, client, old.InstanceID)
			cancel()
			if len(response.body) != 0 {
				t.Fatal("retired owner delivered a response after restart")
			}
			if got := request(t, apps[1].Slug); got != http.StatusOK {
				t.Fatalf("new funded generation after restart: %d", got)
			}
			live, err := e2etest.WaitForInstanceState(t.Context(), t, pool, apps[1].ID, state.StateRunning, 30*time.Second)
			if err != nil || len(live) != 1 || live[0].ID == old.InstanceID {
				t.Fatalf("replacement attempt: %+v %v", live, err)
			}
			physical := financialSystemNativeRecord(t, live[0].ID)
			before, err := netns.ReadVethRXBytes(physical.Lease.VethHost)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel = context.WithTimeout(t.Context(), 5*time.Second)
			response = financialRemoteRequest(ctx, client, live[0].ID)
			cancel()
			after, err := netns.ReadVethRXBytes(physical.Lease.VethHost)
			funding := financialSystemLease(t, s, account, apps[1].ID, live[0].ID)
			if err != nil || response.err != nil || string(response.body) != body || after < before+uint64(len(body)) || after > uint64(funding.EgressGranted) {
				t.Fatalf("replacement remote funding: bytes=%d -> %d grant=%d err=%v/%v", before, after, funding.EgressGranted, response.err, err)
			}
			t.Logf("financial-remote-restart: payload=%d interface_delta=%d cumulative=%d funded=%d retired_payload=0", len(response.body), after-before, after, funding.EgressGranted)
		})
	})
	raw, status = doReq(t, h, key, http.MethodGet, "/v1/billing/budgets/"+budget.ID, nil)
	if status != http.StatusOK || json.Unmarshal(raw, &budget) != nil || budget.Runtime == nil || budget.Runtime.Compute == nil {
		t.Fatalf("budget readback: %d %s", status, raw)
	}
	b := budget.Runtime.Compute
	if b.Egress == nil || b.CommittedMillicents+b.ReservedMillicents > spec.LimitMillicents {
		t.Fatalf("shared monetary ceiling: %+v", b)
	}
	t.Logf("financial-system evidence: account=%s budget=%s original_instance=%s physical_bytes=%d issued_bytes=%d shared_limit_millicents=%d", account, budget.ID, instance, counter, lease.EgressGranted, spec.LimitMillicents)
}

// releaseStrictBudgetsForSystem writes the strict_budgets platform flag the
// way an operator does (ADR-1750) and waits until apid has applied it.
func releaseStrictBudgetsForSystem(t *testing.T, s *state.PgStore) {
	t.Helper()
	value, err := json.Marshal(platformflags.Definition{Enabled: true, Default: true})
	if err != nil {
		t.Fatal(err)
	}
	key := platformflags.ConfigKey(platformflags.StrictBudgets)
	row, err := s.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: key, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: value, ApplyMode: state.RuntimeConfigApplyHot, Reason: "native financial system acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	financialSystemWait(t, 30*time.Second, "apid applies strict_budgets", func() bool {
		receipts, err := s.ListRuntimeConfigApplyReceipts(t.Context(), []string{key})
		for _, r := range receipts {
			if err == nil && r.Daemon == "apid" && r.Status == state.RuntimeConfigReceiptApplied && r.Version >= row.Version {
				return true
			}
		}
		return false
	})
}

func financialSystemWait(t *testing.T, timeout time.Duration, what string, ready func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s: %v", what, ctx.Err())
		case <-tick.C:
		}
	}
}

func financialSystemLease(t *testing.T, s *state.PgStore, account, app, instance string) financial.ComputeLease {
	t.Helper()
	lease, ok, err := s.FinancialComputeInstanceLease(t.Context(), account, app, instance)
	if err != nil || !ok {
		t.Fatalf("native attempt funding: found=%v err=%v", ok, err)
	}
	return lease
}

type financialSystemRecord struct {
	Lease            fcvm.Lease `json:"lease"`
	Authorized       bool       `json:"authorized"`
	PID              int        `json:"pid"`
	StartTime        uint64     `json:"start_time"`
	ResourcesRemoved bool       `json:"resources_removed"`
}

func financialSystemNativeRecord(t *testing.T, instance string) financialSystemRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fcvm.JailChrootBase, ".native-processes", instance+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var record financialSystemRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Lease.Instance != instance {
		t.Fatalf("native record identity: %v", err)
	}
	return record
}

// Verify the original PID incarnation and its actual network assets, instead
// of accepting a stale database state or a process-exited log as retirement.
func financialSystemPhysicalAbsent(record financialSystemRecord) bool {
	if record.PID <= 0 || record.StartTime == 0 || record.Lease.Netns == "" || record.Lease.VethHost == "" {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", record.PID))
	if err == nil {
		end := strings.LastIndexByte(string(data), ')')
		if end < 0 {
			return false
		}
		fields := strings.Fields(string(data[end+1:]))
		if len(fields) <= 19 {
			return false
		}
		start, parseErr := strconv.ParseUint(fields[19], 10, 64)
		if parseErr != nil || start == record.StartTime {
			return false
		}
	} else if !os.IsNotExist(err) {
		return false
	}
	for _, path := range []string{filepath.Join("/run/netns", record.Lease.Netns), filepath.Join("/sys/class/net", record.Lease.VethHost)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			return false
		}
	}
	return true
}
