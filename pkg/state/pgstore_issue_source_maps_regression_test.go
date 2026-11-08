package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgIssueSymbolicationWithExpandedRedactionAndRetirement(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	request := api.PublishIssueSourceMapsRequest{Directory: "/app/dist", Manifest: api.IssueSourceMapManifest{Version: 1, Artifacts: []api.IssueSourceMapArtifact{{GeneratedFile: "bundle.js", SHA256: strings.Repeat("a", 64), Payload: `{"version":3,"sourceRoot":"../","sources":["src/checkout.ts"],"names":["charge"],"mappings":"AASAA"}`}}}}
	now := time.Now().UTC()
	raw := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: strings.Repeat("a@b.co ", 30), Message: strings.Repeat("é", 1024), GroupingStrategy: issues.FailureSiteStrategy, SymbolicationStrategy: issues.DeploymentSourceMapStrategy, Frames: []api.IssueFrame{{File: "/app/dist/bundle.js", Function: "a", Line: 1, Column: 1, InApp: true}}}
	event, fp, title, err := issues.Normalize(raw, now, api.PlanPro.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	in := state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: event, Fingerprint: fp, Title: title, PayloadHash: issues.PayloadDigest(raw), GroupingVersion: issues.GroupingVersionFor(event), Limits: api.PlanPro.IssueLimits(), Now: now}
	plain := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "PlainError"}
	plainEvent, plainFP, plainTitle, err := issues.Normalize(plain, now, api.PlanPro.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	plainIn := in
	plainIn.Event, plainIn.Fingerprint, plainIn.Title, plainIn.PayloadHash, plainIn.GroupingVersion = plainEvent, plainFP, plainTitle, issues.PayloadDigest(plain), issues.GroupingVersionFor(plainEvent)
	groups := [][]state.RecordIssueParams{{in}, {plainIn}}
	permanent := func(error) bool { return false }
	splittable := func(err error) bool { return errors.Is(err, state.ErrIssueSourceMapsUnavailable) }
	first, rejected, err := issues.DeliverOTLPGroups(ctx, groups, s.RecordIssueBatch, permanent, splittable, nil)
	if !errors.Is(err, state.ErrIssueSourceMapsUnavailable) || rejected != 0 || len(first) != 1 || first[0].EventID != plain.EventID {
		t.Fatalf("independent event blocked: %+v %d %v", first, rejected, err)
	}
	if _, err := s.PublishIssueSourceMaps(ctx, account.ID, app.ID, dep.ID, account.ID, request); err != nil {
		t.Fatal(err)
	}
	retry, rejected, err := issues.DeliverOTLPGroups(ctx, groups, s.RecordIssueBatch, permanent, splittable, nil)
	if err != nil || rejected != 0 || len(retry) != 2 || retry[0].EventID != raw.EventID || !retry[1].Duplicate {
		t.Fatalf("retry did not deduplicate: %+v %d %v", retry, rejected, err)
	}
	if _, err := s.RecordIssue(ctx, in); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	var payload []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, raw.EventID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var got api.IssueEvent
	if err := json.Unmarshal(payload, &got); err != nil || got.Symbolication == nil || len(got.Frames) != 1 || got.Frames[0].Function != "charge" || got.ExceptionType != event.ExceptionType {
		t.Fatalf("symbolicated payload: %+v %v", got, err)
	}
	if _, err := s.RetireIssueSourceMaps(ctx, account.ID, app.ID, dep.ID, account.ID); err != nil {
		t.Fatal(err)
	}
	in.Event.EventID = uuid.NewString()
	if _, err := s.RecordIssue(ctx, in); !errors.Is(err, state.ErrIssueSourceMapsUnavailable) {
		t.Fatalf("retired maps used: %v", err)
	}
	if _, err := s.PublishIssueSourceMaps(ctx, account.ID, app.ID, dep.ID, account.ID, request); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordIssue(ctx, in); err != nil {
		t.Fatalf("restored maps unavailable: %v", err)
	}
}

func TestPgIssueSourceMapExpandedEventFitsStorage(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	mapping, err := json.Marshal(map[string]any{"version": 3, "sources": []string{strings.Repeat("a@b.co ", 60)}, "names": []string{strings.Repeat("a@b.co ", 70)}, "mappings": "AAAAA"})
	if err != nil {
		t.Fatal(err)
	}
	request := api.PublishIssueSourceMapsRequest{Directory: "/app/dist", Manifest: api.IssueSourceMapManifest{Version: 1, Artifacts: []api.IssueSourceMapArtifact{{GeneratedFile: "bundle.js", SHA256: strings.Repeat("a", 64), Payload: string(mapping)}}}}
	if _, err = s.PublishIssueSourceMaps(ctx, account.ID, app.ID, dep.ID, account.ID, request); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	frames := make([]api.IssueFrame, 32)
	for i := range frames {
		frames[i] = api.IssueFrame{File: "/app/dist/bundle.js", Function: "x", Line: 1, Column: 1, InApp: true}
	}
	raw := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", GroupingStrategy: issues.CauseStrategy, SymbolicationStrategy: issues.DeploymentSourceMapStrategy, Frames: frames}
	for i := 0; i < 8; i++ {
		raw.Causes = append(raw.Causes, api.IssueCause{ExceptionType: "Error", Frames: frames})
	}
	event, fp, title, err := issues.Normalize(raw, now, api.PlanPro.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: event, Fingerprint: fp, Title: title, PayloadHash: issues.PayloadDigest(raw), GroupingVersion: issues.GroupingVersionFor(event), Limits: api.PlanPro.IssueLimits(), Now: now})
	if err != nil {
		t.Fatalf("record expanded source maps: %v", err)
	}
	var size int
	if err = pool.QueryRow(ctx, `SELECT octet_length(payload::text) FROM issue_events WHERE app_id=$1 AND event_id=$2`, app.ID, raw.EventID).Scan(&size); err != nil || size > 512<<10 {
		t.Fatalf("stored payload size=%d, err=%v", size, err)
	}
}
