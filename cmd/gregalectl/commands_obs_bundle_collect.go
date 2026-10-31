package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func collectObsBundle(ctx context.Context, base string, cred obsCredential, incident string, limit, historyLimit int) (obsIncidentBundle, error) {
	bundle := obsIncidentBundle{SchemaVersion: obsIncidentBundleSchema, StartedAt: time.Now().UTC(), RequestedIncidentID: incident}
	// Refuse redirects, including same-origin redirects: every read must use an
	// explicit admin route. The same context bounds all section bodies.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if err := fetchObsBundleSection(ctx, client, base+"/v1/admin/obs/incidents/"+incident+"/evidence?limit="+strconv.Itoa(historyLimit), cred, "evidence", &bundle.Evidence.Data); err != nil {
		return bundle, err
	}
	bundle.Evidence.CollectedAt = time.Now().UTC()
	evidence := bundle.Evidence.Data
	if !validObsBundleEvidence(incident, evidence, historyLimit) {
		return bundle, fmt.Errorf("invalid evidence identity or snapshot metadata")
	}
	// A legacy alert series alias can change episodes between reads. Resolve it
	// once through evidence, then request context using that canonical episode.
	bundle.IncidentID = evidence.Incident.ID
	query := url.Values{"limit": {strconv.Itoa(limit)}, "history_limit": {strconv.Itoa(historyLimit)}}
	if err := fetchObsBundleSection(ctx, client, base+"/v1/admin/obs/incidents/"+bundle.IncidentID+"/context?"+query.Encode(), cred, "context", &bundle.Context.Data); err != nil {
		return bundle, err
	}
	bundle.Context.CollectedAt = time.Now().UTC()
	related := bundle.Context.Data
	if related.IncidentID != bundle.IncidentID || related.AsOf.IsZero() || related.Since.IsZero() || related.Until.Before(related.Since) || related.Limit != limit || related.TimelineLimit != historyLimit || len(related.Resources) > limit || len(related.Timeline) > historyLimit || (related.Scope.State != "known" && related.Scope.State != "unknown") || related.TimelineScope != "incident_scope_and_returned_resources" {
		return bundle, fmt.Errorf("invalid context identity or snapshot metadata")
	}
	if err := fetchObsBundleSection(ctx, client, base+"/v1/admin/obs/health", cred, "health", &bundle.Health.Data); err != nil {
		return bundle, err
	}
	bundle.Health.CollectedAt = time.Now().UTC()
	if bundle.Health.Data.GeneratedAt.IsZero() {
		return bundle, fmt.Errorf("invalid health snapshot metadata")
	}
	if h := bundle.Health.Data.AlertHistory; h != nil && h.DiagnosticsPath == operatorobs.RuleHealthPath {
		section := &obsBundleSection[api.ObsRuleHealthResponse]{}
		// An exact known route is a feature marker, never an arbitrary link to follow.
		if err := fetchObsBundleSection(ctx, client, base+operatorobs.RuleHealthPath, cred, subObsRuleHealth, &section.Data); err != nil {
			return bundle, err
		}
		section.CollectedAt = time.Now().UTC()
		if !validObsRuleHealth(section.Data) {
			return bundle, fmt.Errorf("invalid rule health snapshot metadata")
		}
		bundle.RuleHealth = section
	}
	if err := ctx.Err(); err != nil {
		return bundle, fmt.Errorf("collection deadline exceeded: %w", err)
	}
	bundle.CompletedAt = time.Now().UTC()
	summarizeObsBundle(&bundle)
	return bundle, nil
}

func validObsBundleEvidence(requested string, evidence api.ObsIncidentEvidenceResponse, limit int) bool {
	kind, id, _ := strings.Cut(evidence.Incident.ID, "/")
	resourceMatches := evidence.Incident.ResourceID == id
	if kind == "platform_alert" && evidence.Incident.AlertEpisode != nil {
		// Retained episodes preserve the series in resource_id while their
		// incident.id identifies the immutable episode.
		resourceMatches = resourceMatches || evidence.Incident.ResourceID == evidence.Incident.AlertEpisode.SeriesID
	}
	if operatorobs.ValidateReference(kind, id) != nil || evidence.Incident.Type != kind || !resourceMatches || evidence.GeneratedAt.IsZero() || evidence.Since.IsZero() || evidence.GeneratedAt.Before(evidence.Since) || evidence.Limit != limit || len(evidence.History) > limit {
		return false
	}
	if !validObsBundleTriageHistory(evidence, limit) {
		return false
	}
	if !validObsBundleSuppression(evidence) {
		return false
	}
	if requested == evidence.Incident.ID {
		return true
	}
	requestedKind, requestedID, _ := strings.Cut(requested, "/")
	return kind == "platform_alert" && requestedKind == kind && evidence.Incident.AlertEpisode != nil && evidence.Incident.AlertEpisode.SeriesID == requestedID
}

func fetchObsBundleSection(ctx context.Context, client *http.Client, address string, cred obsCredential, section string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("%s: invalid apid URL", section)
	}
	cred.apply(req)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		// Transport errors may contain URLs, proxy credentials or response text.
		return fmt.Errorf("%s: apid request failed (check connectivity and collection timeout)", section)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: apid returned HTTP %d", section, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, api.ObsIncidentBundleResponseMaxBytes+1))
	if err != nil {
		return fmt.Errorf("%s: could not read apid response", section)
	}
	if len(body) > api.ObsIncidentBundleResponseMaxBytes {
		return fmt.Errorf("%s: response exceeds %d-byte limit", section, api.ObsIncidentBundleResponseMaxBytes)
	}
	// Decode into the closed API types so unknown payload fields cannot be
	// forwarded into the artifact. Never print JSON parse errors or raw bodies.
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("%s: invalid JSON response", section)
	}
	return nil
}
