package sealmigrate

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// Alerting namespaces, as the writers seal them. They match
// pkg/alerts.AlertSecretNamespace (and its legacy form, which the evaluator
// still opens) and pkg/alertchannels.SealNamespace; this package does not
// import those to stay below pkg/alerts.
var (
	alertRuleNamespaces     = []string{"alert_rule_secret", "alert_rule"}
	alertChannelNamespaces  = []string{"alert_channel_target"}
	mcpPagerDutyNamespaces  = []string{api.MCPReadinessPagerDutySecretNamespace}
	alertChannelTargetBytes = 512
)

// AlertingStore is the slice of *state.PgStore the alerting re-seal uses.
type AlertingStore interface {
	AlertingLister
	ResealAlertingCredential(ctx context.Context, c state.AlertingSealedCredential, sealed []byte) (bool, error)
}

// AlertingLister is the slice of *state.PgStore the alerting check reads.
type AlertingLister interface {
	ListAlertingSealedCredentials(ctx context.Context, afterKind, afterID string, limit int) ([]state.AlertingSealedCredential, error)
	ListOpenMCPReadinessRoutingSecrets(ctx context.Context, afterKind, afterID string, limit int) ([]state.AlertingSealedCredential, error)
}

// MigrateAlerting re-seals alert rule webhook secrets, notification channel
// destinations and MCP readiness provider keys to the alerting domain
// recipient alone (ADR 20261022111812, step 3). legacy are the fleet and host
// identities that open values sealed before the domain. Each value keeps its
// namespace. Open MCP readiness incidents carry copies of their route's key
// in delivery_routing; those stay as sealed and age out with the incidents,
// and the copies the domain does not open are reported as skipped.
// Run it only after every meterd holds the domain identity: a re-sealed
// value no longer opens with the fleet identity.
func MigrateAlerting(ctx context.Context, store AlertingStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: alerting migration needs the store, the domain identity and the legacy identities")
	}
	return walkAlerting(ctx, store, domain, legacy, store.ResealAlertingCredential)
}

// CheckAlerting counts, without writing, the alerting values that legacy
// identities still open, and the incident copies that only they open (ADR
// 20261022111812, the contract step's precondition): meterd may drop the
// fleet and host identities only once Pending and Skipped are zero.
func CheckAlerting(ctx context.Context, store AlertingLister, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil {
		return MigrationReport{}, errors.New("sealmigrate: alerting check needs the store and the domain identity")
	}
	return walkAlerting(ctx, store, domain, legacy, nil)
}

// walkAlerting visits every alerting value, then counts the incident
// copies left on legacy identities. With reseal nil it only counts.
func walkAlerting(ctx context.Context, store AlertingLister, domain *age.X25519Identity, legacy []*age.X25519Identity, reseal func(context.Context, state.AlertingSealedCredential, []byte) (bool, error)) (MigrationReport, error) {
	report := MigrationReport{Domain: sealdomain.Alerting, Target: domain.Recipient().String()}
	afterKind, afterID := "", ""
	for {
		page, err := store.ListAlertingSealedCredentials(ctx, afterKind, afterID, migrateBatch)
		if err != nil {
			return report, err
		}
		for _, c := range page {
			report.Scanned++
			afterKind, afterID = c.Kind, c.ID
			namespaces, maxBytes := alertingNamespaces(c.Kind)
			sealed, outcome, err := resealBlobInNamespace(c.Sealed, namespaces, maxBytes, domain, legacy)
			if err != nil {
				return report, fmt.Errorf("sealmigrate: reseal alerting credential %s %s: %w", c.Kind, c.ID, err)
			}
			if reseal == nil {
				report.checked(outcome)
				continue
			}
			replaced := false
			if outcome == resealWrite {
				if replaced, err = reseal(ctx, c, sealed); err != nil {
					return report, err
				}
			}
			report.count(outcome, replaced)
		}
		if len(page) < migrateBatch {
			break
		}
	}
	left, err := alertingRoutingLeft(ctx, store, domain, legacy)
	if err != nil {
		return report, err
	}
	report.Skipped += left
	return report, nil
}

// alertingRoutingLeft counts the destination secret copies open MCP
// readiness incidents carry that a legacy identity opens and the domain
// does not. Delivery may still open them, the migration cannot re-seal them
// in place, and they clear as their incidents resolve.
func alertingRoutingLeft(ctx context.Context, store AlertingLister, domain *age.X25519Identity, legacy []*age.X25519Identity) (int, error) {
	if len(legacy) == 0 {
		return 0, nil
	}
	opens := func(ids []*age.X25519Identity, blob []byte) bool {
		_, plaintext, err := secretbox.OpenBytesMulti(ids, blob)
		clear(plaintext)
		return err == nil
	}
	left := 0
	afterKind, afterID := "", ""
	for {
		page, err := store.ListOpenMCPReadinessRoutingSecrets(ctx, afterKind, afterID, migrateBatch)
		if err != nil {
			return left, err
		}
		for _, c := range page {
			afterKind, afterID = c.Kind, c.ID
			if !opens([]*age.X25519Identity{domain}, c.Sealed) && opens(legacy, c.Sealed) {
				left++
			}
		}
		if len(page) < migrateBatch {
			return left, nil
		}
	}
}

func alertingNamespaces(kind string) ([]string, int) {
	switch kind {
	case state.AlertingSealedCredentialChannel:
		return alertChannelNamespaces, alertChannelTargetBytes
	case state.AlertingSealedCredentialMCPPagerDuty, state.AlertingSealedCredentialMCPFallbackPagerDuty:
		return mcpPagerDutyNamespaces, api.MCPReadinessPagerDutyKeyBytes
	}
	return alertRuleNamespaces, api.AlertRuleWebhookSecretMaxBytes
}

// resealBlobInNamespace is resealBlob for a value its readers accept under
// more than one namespace: it re-seals under the namespace the value has.
func resealBlobInNamespace(blob []byte, namespaces []string, maxBytes int, domain *age.X25519Identity, legacy []*age.X25519Identity) ([]byte, resealOutcome, error) {
	ns, plaintext, err := secretbox.OpenBytesMulti(legacy, blob)
	if err != nil {
		if _, _, domainErr := secretbox.OpenBytesMulti([]*age.X25519Identity{domain}, blob); domainErr == nil {
			return nil, resealCurrent, nil
		}
		return nil, resealFailed, nil
	}
	defer clear(plaintext)
	if !slices.Contains(namespaces, ns) {
		return nil, resealFailed, nil
	}
	sealed, err := secretbox.SealBytesTo([]*age.X25519Recipient{domain.Recipient()}, ns, plaintext, maxBytes)
	if err != nil {
		return nil, resealFailed, err
	}
	return sealed, resealWrite, nil
}
