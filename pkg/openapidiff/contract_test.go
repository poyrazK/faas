package openapidiff

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type contractTestStore struct {
	*state.MemStore
	testAppID string
}

func newTestSnapshotStore(t *testing.T) *contractTestStore {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "contract@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "contract"})
	if err != nil {
		t.Fatal(err)
	}
	return &contractTestStore{MemStore: store, testAppID: app.ID}
}

func TestCompareSnapshotsReportsBreaksAndAdditions(t *testing.T) {
	baseline, err := LoadBytes([]byte(`
openapi: 3.1.0
paths:
  /v1/users:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string}
                required: [id]
`))
	if err != nil {
		t.Fatal(err)
	}
	proposed, err := LoadBytes([]byte(`
openapi: 3.1.0
paths:
  /v1/users:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: integer}
                  name: {type: string}
                required: [id]
`))
	if err != nil {
		t.Fatal(err)
	}
	baselineRaw, _, err := MarshalSnapshot(baseline)
	if err != nil {
		t.Fatal(err)
	}
	proposedRaw, _, err := MarshalSnapshot(proposed)
	if err != nil {
		t.Fatal(err)
	}

	diff, err := CompareSnapshots(baselineRaw, proposedRaw)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Breaks) != 1 || diff.Breaks[0].Kind != SchemaKindTypeChange {
		t.Fatalf("breaks = %+v, want one type change", diff.Breaks)
	}
	if len(diff.Additions) != 1 || diff.Additions[0].Field != "properties.name" {
		t.Fatalf("additions = %+v, want properties.name addition", diff.Additions)
	}
	if diff.BaselineSHA256 == "" || diff.ProposedSHA256 == "" || diff.BaselineSHA256 == diff.ProposedSHA256 {
		t.Fatalf("hashes = %q/%q, want distinct non-empty hashes", diff.BaselineSHA256, diff.ProposedSHA256)
	}
	message := (&GateError{Diff: diff}).Error()
	if !strings.Contains(message, "type_change") || !strings.Contains(message, "get /v1/users 200") {
		t.Fatalf("gate error = %q, want stable break anchor", message)
	}
}

func TestCompareSnapshotsBlocksChangedResponseUnionAsIncomplete(t *testing.T) {
	load := func(schema string) json.RawMessage {
		t.Helper()
		spec, err := LoadBytes([]byte(schema))
		if err != nil {
			t.Fatal(err)
		}
		raw, _, err := MarshalSnapshot(spec)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	baseline := load(`{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"oneOf":[{"type":"string","enum":["alpha","beta"]},{"type":"integer"}]}}}}}}}}}`)
	proposed := load(`{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"oneOf":[{"type":"string","enum":["alpha","gamma"]},{"type":"integer"}]}}}}}}}}}`)
	if strings.Contains(string(baseline), "alpha") || strings.Contains(string(baseline), "beta") {
		t.Fatalf("snapshot leaked raw enum values: %s", baseline)
	}

	diff, err := CompareSnapshots(baseline, proposed)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Breaks) != 0 || len(diff.Unknowns) != 1 || !diff.Blocking() {
		t.Fatalf("diff = %+v, want one blocking unknown and no confirmed break", diff)
	}
	message := (&GateError{Diff: diff}).Error()
	if !strings.Contains(message, "incomplete comparison") || !strings.Contains(message, "unsupported_union_change") {
		t.Fatalf("gate error = %q, want an explicit incomplete comparison", message)
	}
}

func TestCompareSnapshotsMarksLegacyUnionBaselineIncomplete(t *testing.T) {
	spec, err := LoadBytes([]byte(`{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"oneOf":[{"type":"string","enum":["alpha","beta"]},{"type":"integer"}]}}}}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := MarshalSnapshot(spec)
	if err != nil {
		t.Fatal(err)
	}
	var legacyValue any
	if err := json.Unmarshal(current, &legacyValue); err != nil {
		t.Fatal(err)
	}
	var removeMarkers func(any)
	removeMarkers = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			delete(value, "unsupported_facets_sha256")
			for _, child := range value {
				removeMarkers(child)
			}
		case []any:
			for _, child := range value {
				removeMarkers(child)
			}
		}
	}
	removeMarkers(legacyValue)
	legacy, err := json.Marshal(legacyValue)
	if err != nil {
		t.Fatal(err)
	}

	diff, err := CompareSnapshots(legacy, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Blocking() || len(diff.Breaks) != 0 || len(diff.Unknowns) != 1 ||
		diff.Unknowns[0].Code != SchemaUnknownUnionBaselineIncomplete {
		t.Fatalf("legacy diff = %+v, want union_baseline_incomplete", diff)
	}
}

func TestCompareSnapshotsMarksLegacyUnsupportedSchemaBaselineIncomplete(t *testing.T) {
	spec, err := LoadBytes([]byte(`{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"state":{"type":"string","enum":["ready","failed"]}}}}}}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := MarshalSnapshot(spec)
	if err != nil {
		t.Fatal(err)
	}
	var legacyValue any
	if err := json.Unmarshal(current, &legacyValue); err != nil {
		t.Fatal(err)
	}
	var removeMarkers func(any)
	removeMarkers = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			delete(value, "unsupported_facets_sha256")
			for _, child := range value {
				removeMarkers(child)
			}
		case []any:
			for _, child := range value {
				removeMarkers(child)
			}
		}
	}
	removeMarkers(legacyValue)
	legacy, err := json.Marshal(legacyValue)
	if err != nil {
		t.Fatal(err)
	}

	diff, err := CompareSnapshots(legacy, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Blocking() || len(diff.Breaks) != 0 || len(diff.Unknowns) != 1 ||
		diff.Unknowns[0].PathInSchema != "" ||
		diff.Unknowns[0].Code != SchemaUnknownSchemaBaselineIncomplete {
		t.Fatalf("legacy schema diff = %+v, want schema_baseline_incomplete", diff)
	}
}

func TestCompareSnapshotsMarksLegacySimpleSchemaBaselineIncomplete(t *testing.T) {
	spec, err := LoadBytes([]byte(`{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"string"}}}}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := MarshalSnapshot(spec)
	if err != nil {
		t.Fatal(err)
	}
	var legacyValue any
	if err := json.Unmarshal(current, &legacyValue); err != nil {
		t.Fatal(err)
	}
	var removeMarkers func(any)
	removeMarkers = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			delete(value, "unsupported_facets_sha256")
			for _, child := range value {
				removeMarkers(child)
			}
		case []any:
			for _, child := range value {
				removeMarkers(child)
			}
		}
	}
	removeMarkers(legacyValue)
	legacy, err := json.Marshal(legacyValue)
	if err != nil {
		t.Fatal(err)
	}

	diff, err := CompareSnapshots(legacy, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Blocking() || len(diff.Breaks) != 0 || len(diff.Unknowns) != 1 ||
		diff.Unknowns[0].Code != SchemaUnknownSchemaBaselineIncomplete {
		t.Fatalf("legacy simple schema diff = %+v, want schema_baseline_incomplete", diff)
	}
}

func TestCompareSnapshotsRejectsInvalidEnvelope(t *testing.T) {
	_, err := CompareSnapshots([]byte(`{}`), []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("CompareSnapshots error = %v, want schema_version validation", err)
	}
}

func TestSnapshotFromDocumentPrefersImportedContract(t *testing.T) {
	doc := []byte(`
openapi: 3.1.0
info:
  title: imported
  version: "1"
paths:
  /declared:
    get:
      responses:
        '200':
          description: ok
`)
	snapshot, source, err := SnapshotFromDocument("dep-1", "app-1", "prod", doc, []state.EdgeRule{{
		ID: "rule-1", Enabled: true, MatchPath: "/fallback", MatchMethods: []string{"GET"},
		Kind: state.EdgeRuleKindRoute,
	}})
	if err != nil {
		t.Fatalf("SnapshotFromDocument: %v", err)
	}
	if source != SnapshotSourceManualImport {
		t.Fatalf("source = %q, want %q", source, SnapshotSourceManualImport)
	}
	spec, err := UnmarshalSnapshot(snapshot.Snapshot)
	if err != nil {
		t.Fatalf("UnmarshalSnapshot: %v", err)
	}
	if _, ok := spec.Paths["/declared"]; !ok {
		t.Fatalf("paths = %v, want imported /declared path", spec.Paths)
	}
	if _, ok := spec.Paths["fallback"]; ok {
		t.Fatalf("paths = %v, edge-rule fallback leaked into imported snapshot", spec.Paths)
	}
}

func TestCheckPromotionAllowsFirstSnapshot(t *testing.T) {
	store := newTestSnapshotStore(t)
	check, err := CheckPromotion(t.Context(), store, store.testAppID, "pending", "prod")
	if !errors.Is(err, ErrSnapshotBaselineMissing) {
		t.Fatalf("CheckPromotion error = %v, want ErrSnapshotBaselineMissing", err)
	}
	if check.HasBaseline || check.Proposed.SHA256 == "" || check.Diff.ProposedSHA256 == "" {
		t.Fatalf("first promotion check = %+v, want proposed hash without baseline", check)
	}
	if check.ProposedSource != SnapshotSourceEdgeRules {
		t.Fatalf("first promotion source = %q, want %q", check.ProposedSource, SnapshotSourceEdgeRules)
	}
}

func TestCheckPromotionUsesImportedDocument(t *testing.T) {
	store := newTestSnapshotStore(t)
	app, err := store.AppByID(t.Context(), store.testAppID)
	if err != nil {
		t.Fatal(err)
	}
	doc := []byte(`openapi: 3.1.0
info:
  title: imported
  version: "1"
paths:
  /declared:
    get:
      responses:
        '200':
          description: ok
`)
	if err := store.UpsertAppOpenAPIDoc(t.Context(), app.ID, app.AccountID, doc, 1, "3.1.0"); err != nil {
		t.Fatalf("UpsertAppOpenAPIDoc: %v", err)
	}
	check, err := CheckPromotion(t.Context(), store, app.ID, "pending", "prod")
	if !errors.Is(err, ErrSnapshotBaselineMissing) {
		t.Fatalf("CheckPromotion error = %v, want ErrSnapshotBaselineMissing", err)
	}
	if check.ProposedSource != SnapshotSourceManualImport {
		t.Fatalf("proposed source = %q, want %q", check.ProposedSource, SnapshotSourceManualImport)
	}
	spec, err := UnmarshalSnapshot(check.Proposed.Snapshot)
	if err != nil {
		t.Fatalf("UnmarshalSnapshot: %v", err)
	}
	if _, ok := spec.Paths["/declared"]; !ok {
		t.Fatalf("proposed paths = %v, want imported /declared path", spec.Paths)
	}
}

func TestCheckPromotionResetsPreImportBaseline(t *testing.T) {
	store := newTestSnapshotStore(t)
	app, err := store.AppByID(t.Context(), store.testAppID)
	if err != nil {
		t.Fatal(err)
	}
	oldSpec, err := LoadBytes([]byte(`openapi: 3.1.0
info:
  title: old
  version: "1"
paths: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	raw, sha, err := MarshalSnapshot(oldSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentOpenAPISnapshot(t.Context(), state.OpenAPISnapshot{
		DeploymentID: "old-deployment", AppID: app.ID, Scope: "prod", Snapshot: raw,
		SHA256: sha, SchemaVersion: SnapshotSchemaVersion, CapturedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("UpdateDeploymentOpenAPISnapshot: %v", err)
	}
	doc := []byte(`openapi: 3.1.0
info:
  title: imported
  version: "1"
paths:
  /declared:
    get:
      responses:
        '200':
          description: ok
`)
	if err := store.UpsertAppOpenAPIDoc(t.Context(), app.ID, app.AccountID, doc, 1, "3.1.0"); err != nil {
		t.Fatalf("UpsertAppOpenAPIDoc: %v", err)
	}
	check, err := CheckPromotion(t.Context(), store, app.ID, "pending", "prod")
	if !errors.Is(err, ErrSnapshotBaselineMissing) || check.HasBaseline {
		t.Fatalf("CheckPromotion = check=%+v err=%v, want pre-import baseline reset", check, err)
	}
}

func TestCompareSnapshotsFindsIncompleteNestedFacetFingerprint(t *testing.T) {
	spec, err := LoadBytes([]byte(`{"openapi":"3.1.0","paths":{"/items":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"value":{"type":"string","enum":["a","b"]}}}}}}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := MarshalSnapshot(spec)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := UnmarshalSnapshot(current)
	if err != nil {
		t.Fatal(err)
	}
	baseline.Paths["/items"].Methods["get"].Responses["200"].Content["application/json"].Properties["value"].UnsupportedFacetsSHA256 = ""
	partial, _, err := MarshalSnapshot(baseline)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := CompareSnapshots(partial, current)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Blocking() || len(diff.Unknowns) != 1 || diff.Unknowns[0].PathInSchema != "properties.value" || diff.Unknowns[0].Code != SchemaUnknownSchemaBaselineIncomplete {
		t.Fatalf("missing nested fingerprint must remain incomplete even with a complete parent: %+v", diff)
	}
}
