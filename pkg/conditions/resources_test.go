package conditions_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/conditions"
)

func TestDatabaseConditions(t *testing.T) {
	health := func(status, compute, code string) *api.ManagedPostgresHealth {
		return &api.ManagedPostgresHealth{Enabled: true, Status: status, ProviderStatus: "ready", ComputeState: compute,
			CheckedAt: "2026-10-30T14:59:00Z", LastErrorCode: code}
	}
	tests := []struct {
		name  string
		d     api.ManagedPostgresDatabase
		wants map[string]want
	}{
		{"provisioning", api.ManagedPostgresDatabase{State: "provisioning"}, map[string]want{
			"Ready": {"False", "provisioning", false}, "Progressing": {"True", "provisioning", false}, "Degraded": {"Unknown", "not_assessed", false}}},
		{"ready and healthy", api.ManagedPostgresDatabase{State: "ready", Health: health("healthy", "active", "")}, map[string]want{
			"Ready": {"True", "ready", false}, "Progressing": {"False", "complete", false}, "Degraded": {"False", "healthy", false}}},
		{"scaled to zero", api.ManagedPostgresDatabase{State: "ready", Health: health("healthy", "suspended", "")}, map[string]want{
			"Ready": {"True", "suspended", false}}},
		{"degraded with a code", api.ManagedPostgresDatabase{State: "ready", Health: health("degraded", "active", "provider_timeout")}, map[string]want{
			"Ready": {"True", "ready", false}, "Degraded": {"True", "provider_timeout", false}}},
		{"degraded without a code", api.ManagedPostgresDatabase{State: "ready", Health: health("degraded", "unknown", "")}, map[string]want{
			"Degraded": {"True", "degraded", false}}},
		{"stale health", api.ManagedPostgresDatabase{State: "ready", Health: health("stale", "active", "")}, map[string]want{
			"Degraded": {"Unknown", "stale", false}}},
		{"health off", api.ManagedPostgresDatabase{State: "ready", Health: &api.ManagedPostgresHealth{Status: "disabled"}}, map[string]want{
			"Degraded": {"Unknown", "not_assessed", false}}},
		{"updating", api.ManagedPostgresDatabase{State: "updating"}, map[string]want{
			"Ready": {"False", "updating", false}, "Progressing": {"True", "updating", false}}},
		{"failed", api.ManagedPostgresDatabase{State: "failed", LastErrorCode: "quota_exceeded"}, map[string]want{
			"Ready": {"False", "failed", true}, "Progressing": {"False", "failed", true}}},
		{"deleting", api.ManagedPostgresDatabase{State: "deleting"}, map[string]want{
			"Ready": {"False", "deleting", true}, "Progressing": {"True", "deleting", false}}},
		{"deleted", api.ManagedPostgresDatabase{State: "deleted", DeletedAt: "2026-10-30T14:00:00Z", RestorableUntil: "2026-11-06T14:00:00Z"}, map[string]want{
			"Ready": {"False", "deleted", true}, "Progressing": {"False", "complete", false}, "Degraded": {"Unknown", "not_assessed", false}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.d.ID, tc.d.Name = "db-1", "orders"
			got := conditions.Database(tc.d, observed)
			if got.Kind != "database" || got.Name != "orders" {
				t.Errorf("header = %+v", got)
			}
			assertConditions(t, got, api.ResourceKindDatabase, tc.wants)
		})
	}
}

func TestDomainConditions(t *testing.T) {
	tests := []struct {
		name  string
		d     api.CustomDomainResponse
		wants map[string]want
	}{
		{"unverified", api.CustomDomainResponse{}, map[string]want{
			"Ready": {"False", "dns_pending", false}, "Verified": {"False", "dns_pending", false}, "CertificateReady": {"False", "pending", false}}},
		{"verified, issuing", api.CustomDomainResponse{Verified: true, CertStatus: "pending"}, map[string]want{
			"Ready": {"False", "pending", false}, "Verified": {"True", "verified", false}, "CertificateReady": {"False", "pending", false}}},
		{"serving", api.CustomDomainResponse{Verified: true, CertStatus: "issued", CertExpiresAt: "2027-01-28T00:00:00Z"}, map[string]want{
			"Ready": {"True", "serving", false}, "CertificateReady": {"True", "issued", false}}},
		{"renewing", api.CustomDomainResponse{Verified: true, CertStatus: "renewing"}, map[string]want{
			"Ready": {"True", "serving", false}, "CertificateReady": {"True", "renewing", false}}},
		{"behind a CDN", api.CustomDomainResponse{Verified: true, CertStatus: "cdn"}, map[string]want{
			"Ready": {"True", "serving", false}, "CertificateReady": {"True", "cdn", false}}},
		{"certificate failed", api.CustomDomainResponse{Verified: true, CertStatus: "failed", CertLastError: "CAA forbids issuance"}, map[string]want{
			"Ready": {"False", "failed", false}, "CertificateReady": {"False", "failed", false}}},
		{"dns drifted", api.CustomDomainResponse{Verified: true, CertStatus: "dns_drifted"}, map[string]want{
			"Ready": {"False", "dns_drifted", false}, "CertificateReady": {"False", "dns_drifted", false}}},
		{"no recorded status", api.CustomDomainResponse{Verified: true}, map[string]want{
			"Ready": {"False", "not_assessed", false}, "CertificateReady": {"Unknown", "not_assessed", false}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.d.Domain = "shop.example.com"
			got := conditions.Domain(tc.d, observed)
			if got.Kind != "domain" || got.Name != "shop.example.com" {
				t.Errorf("header = %+v", got)
			}
			assertConditions(t, got, api.ResourceKindDomain, tc.wants)
		})
	}
}

func TestCronConditions(t *testing.T) {
	completed := observed.Add(-time.Minute)
	run := func(outcome api.CronRunOutcome) *api.CronRun {
		r := &api.CronRun{ID: "r1", StartedAt: observed.Add(-2 * time.Minute), Outcome: outcome}
		if outcome != api.CronRunRunning {
			r.CompletedAt = &completed
		}
		return r
	}
	enabled := api.CronResponse{Enabled: true, Schedule: "*/5 * * * *"}
	tests := []struct {
		name  string
		cron  api.CronResponse
		last  *api.CronRun
		wants map[string]want
		since string
	}{
		{"never run", enabled, nil, map[string]want{
			"Ready": {"True", "scheduled", false}, "LastRunSucceeded": {"Unknown", "never_run", false}}, ""},
		{"running", enabled, run(api.CronRunRunning), map[string]want{
			"LastRunSucceeded": {"Unknown", "running", false}}, "2026-10-30T14:58:00Z"},
		{"succeeded", enabled, run(api.CronRunSuccess), map[string]want{
			"LastRunSucceeded": {"True", "success", false}}, "2026-10-30T14:59:00Z"},
		{"timed out", enabled, run(api.CronRunTimeout), map[string]want{
			"LastRunSucceeded": {"False", "timeout", false}}, "2026-10-30T14:59:00Z"},
		{"disabled", api.CronResponse{Schedule: "@daily"}, nil, map[string]want{
			"Ready": {"False", "disabled", true}}, ""},
		{"suspended", api.CronResponse{Enabled: true, SuspendedReason: "no_live_deployment"}, nil, map[string]want{
			"Ready": {"False", "no_live_deployment", false}}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.cron.ID = "c1"
			got := conditions.Cron(tc.cron, tc.last, observed)
			if got.Kind != "cron" || got.Name != "c1" {
				t.Errorf("header = %+v", got)
			}
			assertConditions(t, got, api.ResourceKindCron, tc.wants)
			if since := got.Conditions[1].Since; since != tc.since {
				t.Errorf("LastRunSucceeded since = %q, want %q", since, tc.since)
			}
		})
	}
}

func TestJobRunConditions(t *testing.T) {
	tests := []struct {
		name  string
		run   api.JobRunResponse
		wants map[string]want
	}{
		{"queued", api.JobRunResponse{AggregateStatus: "queued", Tasks: 4}, map[string]want{
			"Succeeded": {"False", "queued", false}, "Progressing": {"True", "queued", false}}},
		{"running", api.JobRunResponse{AggregateStatus: "running", Tasks: 4, TasksSucceeded: 1, TasksRunning: 3}, map[string]want{
			"Succeeded": {"False", "running", false}, "Progressing": {"True", "running", false}}},
		{"succeeded", api.JobRunResponse{AggregateStatus: "succeeded", Tasks: 4, TasksSucceeded: 4}, map[string]want{
			"Succeeded": {"True", "succeeded", false}, "Progressing": {"False", "complete", false}}},
		{"failed", api.JobRunResponse{AggregateStatus: "failed", Tasks: 4, TasksSucceeded: 3, TasksFailed: 1}, map[string]want{
			"Succeeded": {"False", "failed", true}, "Progressing": {"False", "failed", true}}},
		{"cancelled", api.JobRunResponse{AggregateStatus: "cancelled", Tasks: 4, TasksCancelled: 4}, map[string]want{
			"Succeeded": {"False", "cancelled", true}}},
		{"dead letter", api.JobRunResponse{AggregateStatus: "dead_letter", Tasks: 1, TasksFailed: 1, DeadLetterCount: 1}, map[string]want{
			"Succeeded": {"False", "dead_letter", true}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.run.ID = "run-1"
			got := conditions.JobRun(tc.run, observed)
			if got.Kind != "job_run" || got.Name != "run-1" {
				t.Errorf("header = %+v", got)
			}
			assertConditions(t, got, api.ResourceKindJobRun, tc.wants)
		})
	}
}
