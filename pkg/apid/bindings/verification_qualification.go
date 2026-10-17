package bindings

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"slices"
	"strings"
	"time"
)

// Unknown or failed evidence is never classified by message text. Every blocker
// must independently be a recognized, well-formed readiness observation.
func pendingQualification(i api.AppBindingInventory, report bindingcheck.Report) *api.BindingVerificationQualification {
	if report.Passed || len(report.Blockers) == 0 || len(report.Blockers) > api.BindingVerificationQualificationMaxConditions || !i.Complete || len(i.Issues) > 0 {
		return nil
	}
	out := &api.BindingVerificationQualification{CheckedAt: report.CheckedAt, Conditions: []api.BindingVerificationQualificationCondition{}}
	for _, finding := range report.Blockers {
		switch finding.Code {
		case "runtime_updating", "runtime_stale":
			// Evaluate has already validated counts and observations; any concurrent
			// runtime_unknown blocker will cause the entire classification to fail.
		case "queue_consumer_unobserved", "queue_consumer_stale":
			matched := false
			for _, item := range i.Bindings {
				if item.Type == finding.Type && item.Name == finding.Name && item.Binding == finding.Binding && item.Scope == finding.Scope {
					if item.Type != api.BindingTypeQueue || item.State != "enabled" || item.Access != "push" || item.ConsumerState != "active" || !slices.Contains([]string{"healthy", "stale", "not_observed"}, item.ConsumerLiveness) || item.ObservedAt != nil && item.ObservedAt.After(report.CheckedAt) {
						return nil
					}
					matched = true
				}
			}
			if !matched {
				return nil
			}
		case "application_adoption_unknown", "application_ack_stale", "application_ack_candidate_unobserved":
			matched := false
			for _, item := range i.Bindings {
				if item.Type == finding.Type && item.Name == finding.Name && item.Binding == finding.Binding && item.Scope == finding.Scope {
					if !acknowledgmentCanProgress(item, report.CheckedAt) {
						return nil
					}
					matched = true
				}
			}
			if !matched {
				return nil
			}
		default:
			return nil
		}
		out.Conditions = append(out.Conditions, api.BindingVerificationQualificationCondition{App: i.App, Code: finding.Code, Type: finding.Type, Name: finding.Name, Binding: finding.Binding, Scope: finding.Scope})
	}
	return out
}
func acknowledgmentCanProgress(item api.AppBindingInventoryItem, now time.Time) bool {
	if item.ApplicationAdoption == nil {
		return false
	}
	a := bindingcheck.SummarizeApplicationAdoption(*item.ApplicationAdoption, now)
	if a.Source != "application_ack" || !a.Complete || a.ObservedAt.IsZero() || a.ObservedAt.After(now) || a.SecretsExpected != len(api.BindingCredentialSecretKeys(item.Type, item.Binding)) || a.SecretsExpected == 0 || a.SecretsObserved != a.SecretsExpected || a.Status == "failed" {
		return false
	}
	keys := api.BindingCredentialSecretKeys(item.Type, item.Binding)
	for _, t := range a.Targets {
		if !slices.Contains(keys, t.Key) || t.ReloadSupport != "enabled" || len(t.ProcessGeneration) != 32 || strings.Trim(t.ProcessGeneration, "0123456789abcdef") != "" || !slices.Contains([]string{"current", "stale"}, t.ReloadStatus) || t.ApplicationAck == "failed" {
			return false
		}
		if t.ApplicationAckGeneration != "" && (len(t.ApplicationAckGeneration) != 32 || strings.Trim(t.ApplicationAckGeneration, "0123456789abcdef") != "") {
			return false
		}
		if t.ApplicationAckAt != nil && (t.ApplicationAckAt.IsZero() || t.ApplicationAckAt.After(now) || t.ApplicationAckVersion < 1 || t.ApplicationAckVersion > t.CurrentVersion || t.ApplicationAck != "applied") {
			return false
		}
		if t.ApplicationAckAt == nil && (t.ApplicationAckVersion != 0 || t.ApplicationAck != "") {
			return false
		}
		if !slices.Contains([]string{"current", "application_ack_missing", "application_ack_generation_missing", "application_ack_generation_mismatch", "application_ack_stale"}, t.ApplicationAckReason) {
			return false
		}
	}
	return true
}
