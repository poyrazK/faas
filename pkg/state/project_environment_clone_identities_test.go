package state

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261104120000 — a full clone copies production's active API
// consumers with their active keys and plan assignments, and its alert rules
// paused; publication proves both, and revoking production's consumer or key
// revokes the stage's copy (amendment 2026-11-28).

func identityCloneFixture(t *testing.T) (*MemStore, ProjectEnvironment) {
	t.Helper()
	m := NewMemStore()
	m.apps["app"] = App{ID: "app", AccountID: "acct", ProjectID: "proj", Status: AppActive}
	stage := ProjectEnvironment{ID: "env", AccountID: "acct", ProjectID: "proj", Slug: "stage"}
	m.projectEnvironments["env"] = stage
	expires := time.Now().Add(time.Hour).UTC()
	revokedAt := time.Now().UTC()
	m.apiConsumers["c1"] = APIConsumer{ID: "c1", AccountID: "acct", AppID: "app", ExternalRef: "cust-1", Name: "Acme", Status: APIConsumerStatusActive}
	m.apiConsumers["gone"] = APIConsumer{ID: "gone", AccountID: "acct", AppID: "app", ExternalRef: "cust-gone", Name: "Gone", Status: APIConsumerStatusRevoked, RevokedAt: &revokedAt}
	m.apiConsumers["tenant"] = APIConsumer{ID: "tenant", AccountID: "acct", AppID: "app", ExternalRef: "cust-tenant", Name: "Tenant", Status: APIConsumerStatusActive, PlatformTenantID: "t1"}
	m.consumerKeys["k1"] = ConsumerKey{ID: "k1", AccountID: "acct", AppID: "app", ConsumerID: "c1", Name: "primary", Prefix: "gk_aaaa", Hash: []byte("hash-1"), Scopes: []string{"read"}, ExpiresAt: &expires}
	m.consumerKeys["k2"] = ConsumerKey{ID: "k2", AccountID: "acct", AppID: "app", ConsumerID: "c1", Name: "old", Prefix: "gk_bbbb", Hash: []byte("hash-2"), RevokedAt: &revokedAt}
	m.apiConsumerPlanAssignments["pa"] = APIConsumerPlanAssignment{ID: "pa", AccountID: "acct", AppID: "app", ConsumerID: "c1", PlanID: "plan", EffectiveFrom: expires.Add(-2 * time.Hour)}
	m.alertRules["r1"] = AlertRule{ID: "r1", AccountID: "acct", AppID: "app", Name: "errors", Enabled: true, Metric: "error_rate_pct", Comparison: "gt",
		Threshold: 5, WindowSpec: AlertWindow5m, Action: AlertActionWebhook, WebhookURL: "https://example.test/hook", WebhookSecretSealed: []byte("sealed"),
		CooldownMinutes: 30, State: AlertStateFiring, LastFiredAt: time.Now()}
	m.alertRules["slo"] = AlertRule{ID: "slo", AccountID: "acct", AppID: "app", Name: "slo burn", SLOID: "slo-1", Metric: "error_rate_pct", Comparison: "gt"}
	m.storeAlertRuleChannelsLocked("r1", []string{"ch-b", "ch-a"})
	return m, stage
}

func captureIdentityClone(t *testing.T, m *MemStore) (projectCloneWorkloadRecord, ProjectEnvironmentClone) {
	t.Helper()
	consumers, err := m.captureCloneConsumersLocked("app", "")
	if err != nil {
		t.Fatal(err)
	}
	rules, err := m.captureCloneAlertRulesLocked("app", "")
	if err != nil {
		t.Fatal(err)
	}
	record := projectCloneWorkloadRecord{snapshot: projectCloneWorkloadSnapshot{Consumers: &consumers, AlertRules: &rules}}
	record.AppID, record.WorkloadSlug = "app", "web"
	clone := ProjectEnvironmentClone{AccountID: "acct", ProjectID: "proj",
		capturedConsumers: capturedCloneConsumers([]projectCloneWorkloadRecord{record}), capturedAlertRules: capturedCloneAlertRules([]projectCloneWorkloadRecord{record})}
	return record, clone
}

func TestMemCloneCopiesConsumersAndPausedAlertRules(t *testing.T) {
	m, stage := identityCloneFixture(t)
	record, clone := captureIdentityClone(t, m)
	if got := record.snapshot.Consumers.Consumers; len(got) != 1 || got[0].ExternalRef != "cust-1" || len(got[0].Keys) != 1 || len(got[0].Plans) != 1 {
		t.Fatalf("captured consumers = %+v; want only the active, non-tenant consumer with its active key", got)
	}
	if got := record.snapshot.AlertRules; len(got.Rules) != 1 || got.LeftWithSource != 1 || !got.Rules[0].Rule.Enabled {
		t.Fatalf("captured rules = %+v; want the standalone rule, the SLO rule left with the source", got)
	}

	var result ProjectEnvironmentCloneResult
	now := time.Now().UTC()
	m.copyCapturedConsumersLocked(clone, stage, now, &result)
	m.copyCapturedAlertRulesLocked(clone, stage, now, &result)
	if result.ConsumersCopied != 1 || result.ConsumerKeysCopied != 1 || result.AlertRulesCopied != 1 || result.AlertRulesLeftWithSource != 1 {
		t.Fatalf("result = %+v", result)
	}
	var copiedKey ConsumerKey
	for _, k := range m.consumerKeys {
		if k.EnvironmentID == stage.ID {
			copiedKey = k
		}
	}
	if copiedKey.Prefix != "gk_aaaa" || !bytes.Equal(copiedKey.Hash, []byte("hash-1")) || copiedKey.RevokedAt != nil || copiedKey.ConsumerID == "c1" {
		t.Fatalf("copied key = %+v; want production's prefix and secret on the stage's consumer", copiedKey)
	}
	var copiedRule AlertRule
	for _, r := range m.alertRules {
		if r.EnvironmentID == stage.ID {
			copiedRule = r
		}
	}
	if copiedRule.Enabled || copiedRule.State != AlertStateOk || !copiedRule.LastFiredAt.IsZero() || copiedRule.Name != "errors" ||
		len(m.alertRuleChannels[copiedRule.ID]) != 2 {
		t.Fatalf("copied rule = %+v, channels %v; want a paused copy with fresh state", copiedRule, m.alertRuleChannels[copiedRule.ID])
	}

	op := ProjectEnvironmentCloneOperation{ProjectID: "proj", TargetEnvironment: "stage"}
	records := []projectCloneWorkloadRecord{record}
	if err := m.verifyCloneConsumerPublicationLocked(op, records); err != nil {
		t.Fatalf("publication of the copy: %v", err)
	}
	if err := m.verifyCloneAlertRulePublicationLocked(op, records); err != nil {
		t.Fatalf("publication of the paused rules: %v", err)
	}

	// Revoking production's key revokes the stage's copy, and publication
	// still holds: the source was revoked since.
	if _, err := m.RevokeConsumerKey(t.Context(), "acct", "k1"); err != nil {
		t.Fatal(err)
	}
	if k := m.consumerKeys[copiedKey.ID]; k.RevokedAt == nil {
		t.Fatal("the stage's copy outlived production's key")
	}
	if err := m.verifyCloneConsumerPublicationLocked(op, records); err != nil {
		t.Fatalf("publication after the source key's revocation: %v", err)
	}
	if _, err := m.RevokeAPIConsumer(t.Context(), "acct", "c1"); err != nil {
		t.Fatal(err)
	}
	for _, c := range m.apiConsumers {
		if c.EnvironmentID == stage.ID && c.Active() {
			t.Fatal("the stage's consumer outlived production's")
		}
	}
	if err := m.verifyCloneConsumerPublicationLocked(op, records); err != nil {
		t.Fatalf("publication after the source consumer's revocation: %v", err)
	}

	// An enabled copy is not a published clone.
	copiedRule.Enabled = true
	m.alertRules[copiedRule.ID] = copiedRule
	if err := m.verifyCloneAlertRulePublicationLocked(op, records); !errors.Is(err, ErrConflict) {
		t.Fatalf("an enabled copy published: %v", err)
	}
}

func TestMemCloneSkipsAKeyRevokedSinceCapture(t *testing.T) {
	m, stage := identityCloneFixture(t)
	record, clone := captureIdentityClone(t, m)
	revokedAt := time.Now().UTC()
	k := m.consumerKeys["k1"]
	k.RevokedAt = &revokedAt
	m.consumerKeys["k1"] = k
	var result ProjectEnvironmentCloneResult
	m.copyCapturedConsumersLocked(clone, stage, time.Now(), &result)
	if result.ConsumersCopied != 1 || result.ConsumerKeysCopied != 0 {
		t.Fatalf("result = %+v; want the consumer without the revoked key", result)
	}
	op := ProjectEnvironmentCloneOperation{ProjectID: "proj", TargetEnvironment: "stage"}
	if err := m.verifyCloneConsumerPublicationLocked(op, []projectCloneWorkloadRecord{record}); err != nil {
		t.Fatalf("publication without the revoked key: %v", err)
	}
	// A key missing while its source is active fails publication.
	k.RevokedAt = nil
	m.consumerKeys["k1"] = k
	if err := m.verifyCloneConsumerPublicationLocked(op, []projectCloneWorkloadRecord{record}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a missing active key published: %v", err)
	}
}

func TestMemCloneIdentityCaptureRefusesUncapturedApps(t *testing.T) {
	m, _ := identityCloneFixture(t)
	if err := m.requireCloneConsumersCapturedLocked("proj", "", map[string]bool{"app": true}); err != nil {
		t.Fatalf("captured app: %v", err)
	}
	if err := m.requireCloneConsumersCapturedLocked("proj", "", map[string]bool{}); !errors.Is(err, ErrProjectEnvironmentCloneConsumerCaptureUnavailable) {
		t.Fatalf("uncaptured consumers = %v", err)
	}
	if err := m.requireCloneAlertRulesCapturedLocked("proj", "", map[string]bool{}); !errors.Is(err, ErrProjectEnvironmentCloneAlertRuleCaptureUnavailable) {
		t.Fatalf("uncaptured rules = %v", err)
	}
}

func TestCheckCloneIdentityQuotas(t *testing.T) {
	count := func(n int) func(string) (int, error) { return func(string) (int, error) { return n, nil } }
	limits := api.Limits{ConsumerKeysPerApp: 5, ConsumerKeysPerAccount: 10, AlertRuleLimitPerApp: 5, AlertRuleLimitPerAccount: 10}
	if err := checkCloneConsumerKeyQuota(map[string]int{"a": 2}, count(3), 8, limits); err != nil {
		t.Fatalf("fits: %v", err)
	}
	var keys *ConsumerKeyQuotaError
	if err := checkCloneConsumerKeyQuota(map[string]int{"a": 3}, count(3), 3, limits); !errors.As(err, &keys) || keys.Scope != api.PlanQuotaScopeApp || keys.Observed != 6 {
		t.Fatalf("app key cap = %v", err)
	}
	if err := checkCloneConsumerKeyQuota(map[string]int{"a": 1, "b": 1}, count(0), 9, limits); !errors.As(err, &keys) || keys.Scope != api.PlanQuotaScopeAccount || keys.Observed != 11 {
		t.Fatalf("account key cap = %v", err)
	}
	var rules *AlertRuleQuotaError
	if err := checkCloneAlertRuleQuota(map[string]int{"a": 3}, count(3), 3, limits); !errors.As(err, &rules) || rules.Scope != AlertRuleQuotaScopeApp || rules.Observed != 6 {
		t.Fatalf("app rule cap = %v", err)
	}
	if err := checkCloneAlertRuleQuota(map[string]int{}, count(99), 99, limits); err != nil {
		t.Fatalf("nothing copied: %v", err)
	}
}

func TestNormalizeCloneConsumersRefusesDuplicateIdentities(t *testing.T) {
	for name, in := range map[string]projectCloneConsumers{
		"external ref": {Consumers: []projectCloneConsumer{{ExternalRef: "a"}, {ExternalRef: "a"}}},
		"key prefix": {Consumers: []projectCloneConsumer{{ExternalRef: "a", Keys: []projectCloneConsumerKey{{Name: "x", Prefix: "p"}}},
			{ExternalRef: "b", Keys: []projectCloneConsumerKey{{Name: "y", Prefix: "p"}}}}},
		"key name": {Consumers: []projectCloneConsumer{{ExternalRef: "a", Keys: []projectCloneConsumerKey{{Name: "x", Prefix: "p"}, {Name: "x", Prefix: "q"}}}}},
	} {
		if _, err := normalizeCloneConsumers(in); !errors.Is(err, ErrConflict) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
