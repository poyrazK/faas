package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

const obsIncidentBundleSchema = "gregale.operator.incident-bundle/v1"

// Only sanitized operator API DTOs enter this format. Raw response maps,
// arbitrary diagnostic links, local config, and bearer values never do.
type obsIncidentBundle struct {
	SchemaVersion       string                                            `json:"schema_version"`
	StartedAt           time.Time                                         `json:"started_at"`
	CompletedAt         time.Time                                         `json:"completed_at"`
	RequestedIncidentID string                                            `json:"requested_incident_id"`
	IncidentID          string                                            `json:"incident_id"`
	Evidence            obsBundleSection[api.ObsIncidentEvidenceResponse] `json:"evidence"`
	Context             obsBundleSection[api.ObsIncidentContextResponse]  `json:"context"`
	Health              obsBundleSection[api.ObsHealthResponse]           `json:"health"`
	RuleHealth          *obsBundleSection[api.ObsRuleHealthResponse]      `json:"rule_health,omitempty"`
	Coverage            obsBundleCoverage                                 `json:"coverage"`
	Redaction           obsBundleRedaction                                `json:"redaction"`
	Limitations         []string                                          `json:"limitations"`
}

type obsBundleSection[T any] struct {
	CollectedAt time.Time `json:"collected_at"`
	Data        T         `json:"data"`
}

type obsBundleCoverage struct {
	RuleHealthState             string   `json:"rule_health_state"`
	RuleHealthTruncated         bool     `json:"rule_health_truncated"`
	SuppressionState            string   `json:"suppression_state"`
	TriageHistoryState          string   `json:"triage_history_state"`
	TriageHistoryTruncated      bool     `json:"triage_history_truncated"`
	TriageHistoryMissingEntries *int64   `json:"triage_history_missing_entries"`
	EvidenceHistoryTruncated    bool     `json:"evidence_history_truncated"`
	RelatedResourcesTruncated   bool     `json:"related_resources_truncated"`
	RelatedTimelineTruncated    bool     `json:"related_timeline_truncated"`
	UnavailableSources          []string `json:"unavailable_sources"`
	UnhealthySignals            []string `json:"unhealthy_signals"`
	MissingHealthMetadata       []string `json:"missing_health_metadata"`
	AlertCollectionState        string   `json:"alert_collection_state"`
	RetentionState              string   `json:"retention_state"`
	ExpiredEpisodes             *int64   `json:"expired_episodes"`
	RelationshipScope           string   `json:"relationship_scope"`
	TimelineScope               string   `json:"timeline_scope"`
}

type obsBundleRedaction struct {
	Profile  string   `json:"profile"`
	Excluded []string `json:"excluded"`
}

func cmdObsBundle(args []string) int {
	fs := flag.NewFlagSet(subObsBundle, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	incident := fs.String("incident", "", "incident ID: type/resource-id")
	output := fs.String("output", "", "new owner-only JSON file (required; - for stdout)")
	limit := fs.Int("limit", api.ObsIncidentContextLimitDefault, "maximum related resources")
	historyLimit := fs.Int("history-limit", api.ObsIncidentEvidenceLimitDefault, "maximum entries per history section")
	jsonOut := fs.Bool("json", false, "emit JSON file receipt")
	adminToken := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", api.ObsIncidentBundleTimeoutDefault, "total collection timeout (maximum 1m)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	kind, id, _ := strings.Cut(*incident, "/")
	if fs.NArg() != 0 || operatorobs.ValidateReference(kind, id) != nil || *output == "" || *limit < 1 || *limit > api.ObsIncidentContextLimitMax || *historyLimit < 1 || *historyLimit > api.ObsIncidentEvidenceLimitMax || *timeout <= 0 || *timeout > api.ObsIncidentBundleTimeoutMax {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs bundle: valid --incident, --output, resource/history limits, and --timeout (0..1m) required")
		return 2
	}
	cred, ok := resolveObsCredential("obs bundle", *adminToken)
	if !ok {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	bundle, err := collectObsBundle(ctx, apidBase(), cred, *incident, *limit, *historyLimit)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs bundle:", err)
		return 1
	}
	body, err := marshalObsBundle(bundle)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs bundle:", err)
		return 1
	}
	if *output == "-" {
		if _, err := osStdout.Write(body); err != nil {
			_, _ = fmt.Fprintln(osStderr, "gregalectl obs bundle: write stdout:", err)
			return 1
		}
		return 0
	}
	if err := writeObsBundleFile(*output, body); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs bundle:", err)
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(struct {
			Path          string `json:"path"`
			SchemaVersion string `json:"schema_version"`
			IncidentID    string `json:"incident_id"`
			Bytes         int    `json:"bytes"`
		}{*output, bundle.SchemaVersion, bundle.IncidentID, len(body)})
	}
	_, err = fmt.Fprintf(osStdout, "Wrote operator incident bundle for %s to %s (%d bytes). Coverage and limitations are included.\n", bundle.IncidentID, *output, len(body))
	if err != nil {
		return 1
	}
	return 0
}

func marshalObsBundle(bundle obsIncidentBundle) ([]byte, error) {
	if h := bundle.Health.Data.AlertHistory; h != nil && h.DiagnosticsPath != "" && h.DiagnosticsPath != operatorobs.RuleHealthPath {
		safe := *h
		safe.DiagnosticsPath = ""
		bundle.Health.Data.AlertHistory = &safe
	}
	if t := bundle.Evidence.Data.Incident.Triage; t != nil {
		safe := *t
		safe.OwnerAccountID = ""
		bundle.Evidence.Data.Incident.Triage = &safe
	}
	body, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode bundle: %w", err)
	}
	if len(body)+1 > api.ObsIncidentBundleMaxBytes {
		return nil, fmt.Errorf("bundle exceeds %d-byte limit", api.ObsIncidentBundleMaxBytes)
	}
	return append(body, '\n'), nil
}

func summarizeObsBundle(bundle *obsIncidentBundle) {
	evidence, related, health := bundle.Evidence.Data, bundle.Context.Data, bundle.Health.Data
	coverage := obsBundleCoverage{
		EvidenceHistoryTruncated: evidence.Truncated, RelatedResourcesTruncated: related.NextCursor != "", RelatedTimelineTruncated: related.TimelineTruncated,
		UnavailableSources: []string{}, UnhealthySignals: []string{}, MissingHealthMetadata: []string{}, AlertCollectionState: "unknown", RetentionState: "unknown",
		RelationshipScope: related.Scope.State, TimelineScope: related.TimelineScope,
	}
	for _, source := range evidence.UnavailableSources {
		coverage.UnavailableSources = append(coverage.UnavailableSources, "evidence."+source)
	}
	for _, source := range related.UnavailableSources {
		coverage.UnavailableSources = append(coverage.UnavailableSources, "context."+source)
	}
	for _, name := range []string{"audit_log_write_total_5m", "audit_log_write_failures_5m", "alerts_firing"} {
		signal, ok := health.Signals[name]
		if !ok || signal.ObservedAt == nil {
			coverage.MissingHealthMetadata = append(coverage.MissingHealthMetadata, name)
		}
		if !ok || signal.State != api.ObsHealthSignalHealthy || signal.ObservedAt == nil {
			coverage.UnhealthySignals = append(coverage.UnhealthySignals, name)
		}
	}
	if health.AlertHistory == nil {
		coverage.MissingHealthMetadata = append(coverage.MissingHealthMetadata, "alert_history")
	} else {
		if health.AlertHistory.CollectionState != "" {
			coverage.AlertCollectionState = health.AlertHistory.CollectionState
		} else {
			coverage.MissingHealthMetadata = append(coverage.MissingHealthMetadata, "alert_history.collection_state")
		}
	}
	if health.AlertRetention == nil {
		coverage.MissingHealthMetadata = append(coverage.MissingHealthMetadata, "alert_retention")
	} else {
		coverage.RetentionState = health.AlertRetention.State
		n := health.AlertRetention.ExpiredEpisodes
		coverage.ExpiredEpisodes = &n
	}
	coverage.TriageHistoryState = "not_applicable"
	coverage.RuleHealthState = "unknown"
	if bundle.RuleHealth != nil {
		coverage.RuleHealthState = bundle.RuleHealth.Data.State
		coverage.RuleHealthTruncated = bundle.RuleHealth.Data.Truncated
	} else {
		coverage.MissingHealthMetadata = append(coverage.MissingHealthMetadata, "rule_health")
	}
	coverage.SuppressionState = "not_applicable"
	if evidence.Incident.Type == "platform_alert" {
		coverage.SuppressionState = "unknown"
		if evidence.Suppression != nil {
			coverage.SuppressionState = evidence.Suppression.State
		}
		coverage.TriageHistoryState = "unknown"
		if history := evidence.TriageHistory; history != nil {
			coverage.TriageHistoryState = history.Coverage.State
			coverage.TriageHistoryTruncated = history.Truncated
			missing := history.Coverage.MissingEntries
			coverage.TriageHistoryMissingEntries = &missing
		}
	}
	bundle.Coverage = coverage
	bundle.Redaction = obsBundleRedaction{Profile: "operator-metadata-only", Excluded: []string{
		"credentials", "local_configuration", "request_bodies_and_headers", "raw_logs", "raw_event_payloads", "actor_identities", "commands_and_environment", "raw_span_attributes", "raw_sql",
	}}
	bundle.Limitations = []string{
		"rule_health_covers_current_loaded_rules_not_expected_inventory_or_past_failure_cause",
		"suppression_is_current_only_and_does_not_prove_delivery",
		"sections_are_separate_reads_not_one_atomic_snapshot",
		"relationships_are_current_potential_exposure_not_historical_impact_or_causation",
		"one_resource_page_only_timeline_covers_incident_scope_and_returned_resources",
		"history_windows_and_truncation_are_per_section",
		"triage_history_is_one_page_revision_bounded_receipts_can_expire_between_reads",
		"trace_and_diagnostic_links_are_not_fetched",
		"unhealthy_or_missing_signal_metadata_makes_numeric_fallbacks_unusable",
		"identifiers_and_operational_metadata_require_controlled_sharing",
	}
}
