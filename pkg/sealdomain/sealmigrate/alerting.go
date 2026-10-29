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
	ListAlertingSealedCredentials(ctx context.Context, afterKind, afterID string, limit int) ([]state.AlertingSealedCredential, error)
	ResealAlertingCredential(ctx context.Context, c state.AlertingSealedCredential, sealed []byte) (bool, error)
}

// MigrateAlerting re-seals alert rule webhook secrets, notification channel
// destinations and MCP readiness provider keys to the alerting domain
// recipient alone (ADR 20261022111812, step 3). legacy are the fleet and host
// identities that open values sealed before the domain. Each value keeps its
// namespace. Open MCP readiness incidents carry copies of their route's key
// in delivery_routing; those stay as sealed and age out with the incidents.
// Run it only after every meterd holds the domain identity: a re-sealed
// value no longer opens with the fleet identity.
func MigrateAlerting(ctx context.Context, store AlertingStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: alerting migration needs the store, the domain identity and the legacy identities")
	}
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
			replaced := false
			if outcome == resealWrite {
				if replaced, err = store.ResealAlertingCredential(ctx, c, sealed); err != nil {
					return report, err
				}
			}
			report.count(outcome, replaced)
		}
		if len(page) < migrateBatch {
			return report, nil
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
