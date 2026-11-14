package mcpmanagement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func (f *publicationFixture) serveJournal(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/v1/tool-registry" {
		page := api.ToolRegistryPage{Versions: []api.ToolRegistryVersion{}}
		q := r.URL.Query()
		for _, record := range f.journal {
			if record.Status == "completed" && record.ID > q.Get("cursor") && toolpublication.Matches(record, q.Get("query")) {
				page.Versions = append(page.Versions, toolpublication.Version(record))
			}
		}
		sort.Slice(page.Versions, func(i, j int) bool { return page.Versions[i].Version < page.Versions[j].Version })
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit > 0 && len(page.Versions) > limit {
			page.Versions = page.Versions[:limit]
			page.NextCursor = page.Versions[limit-1].Version
		}
		_ = json.NewEncoder(w).Encode(page)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/tool-publications/"), "/outcome")
	record, exists := f.journal[id]
	switch r.Method {
	case http.MethodPut:
		var in api.ToolPublicationIntent
		_ = json.NewDecoder(r.Body).Decode(&in)
		if exists && !reflect.DeepEqual(record.Intent, in) {
			http.Error(w, "changed", 409)
			return
		}
		if !exists {
			now := time.Now().UTC()
			record = api.ToolPublicationRecord{ID: id, AccountID: "account-one", Intent: in, Status: "running", CreatedAt: now, Deadline: now.Add(api.ToolPublicationDeadline), EvidenceOrigin: "publisher_attested"}
			f.journal[id] = record
		}
		if f.loseBegin {
			f.loseBegin = false
			http.Error(w, "lost response after journal commit", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(api.BeginToolPublicationResponse{Created: !exists, Record: record})
	case http.MethodPost:
		if !exists {
			http.NotFound(w, r)
			return
		}
		var in api.ToolPublicationOutcome
		_ = json.NewDecoder(r.Body).Decode(&in)
		if toolpublication.ValidateOutcome(record.Intent, in) != nil {
			http.Error(w, "invalid evidence", 400)
			return
		}
		now := time.Now().UTC()
		record.Status, record.Outcome, record.FinishedAt = in.Status, &in, &now
		if in.Status == "completed" {
			record.DeploymentSourceSHA256 = strings.Repeat("c", 64)
		}
		f.journal[id] = record
		_ = json.NewEncoder(w).Encode(record)
	default:
		if !exists {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(toolpublication.View(record, time.Now()))
	}
}

func TestToolRegistrySurvivesFreshBridge(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, backend := newPublicationFixture(t)
			builder, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, LocalCLI: f.cli, Toolsets: []string{ToolsetBuildTools}}, protocol)
			plan := publicationCall(t, ctx, builder, "gregale_plan_tool_deploy", publicationArgs(publicationProject(t)), false)
			var id string
			_ = json.Unmarshal(plan["plan_id"], &id)
			publicationCall(t, ctx, builder, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
			if out := waitPublication(t, ctx, builder, id); string(out["status"]) != `"completed"` {
				t.Fatalf("publication: %v", out)
			}
			// Close the original session and create an entirely new management
			// instance with no CLI or source access for the consuming agent.
			_ = builder.Close()
			reader, fresh := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Toolsets: []string{ToolsetRegistry}}, protocol)
			page := publicationCall(t, fresh, reader, "gregale_search_tools", searchToolsArgs{Query: "ADD"}, false)
			if !strings.Contains(string(page["versions"]), id) {
				t.Fatal("fresh bridge cannot discover published version")
			}
			out := publicationCall(t, fresh, reader, "gregale_describe_tool", describeToolArgs{Version: id, Name: "add"}, false)
			var encoded, digest string
			_ = json.Unmarshal(out["catalog_base64"], &encoded)
			_ = json.Unmarshal(out["catalog_contract_sha256"], &digest)
			raw, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || toolpublication.Digest(raw) != digest || !strings.Contains(string(raw), `"name": "add"`) {
				t.Fatal("canonical runtime schemas were not retained")
			}
			if string(out["execution_authorized"]) != "false" {
				t.Fatal("discovery granted execution")
			}
			publicationCall(t, fresh, reader, "gregale_describe_tool", describeToolArgs{Version: id, Name: "other"}, true)
			recovered, recoveryCtx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, LocalCLI: f.cli, Toolsets: []string{ToolsetBuildTools}}, protocol)
			status := publicationCall(t, recoveryCtx, recovered, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
			if string(status["status"]) != `"completed"` || string(status["durable"]) != "true" {
				t.Fatalf("lost receipt not recovered: %v", status)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.deploys != 1 {
				t.Fatal("fresh bridge repeated execution")
			}
		})
	}
}

func TestToolRegistryScopeAndResponseBoundaries(t *testing.T) {
	for _, opts := range []Options{{Org: "team"}, {agentKey: true, AllowedApps: map[string]string{"alpha": "app-one"}}} {
		f, backend := newPublicationFixture(t)
		opts.APIBase, opts.Toolsets = backend.URL, []string{ToolsetRegistry}
		cs, ctx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
		catalog, err := cs.ListTools(ctx, nil)
		if err != nil || len(catalog.Tools) != 0 {
			t.Fatalf("scoped catalog: %v %v", catalog, err)
		}
		m := &management{opts: opts}
		if _, err := m.searchTools(context.Background(), searchToolsArgs{}); err == nil {
			t.Fatal("handler bypassed scope boundary")
		}
		_ = f
	}
}

func TestToolRegistryRejectsInvalidSelectors(t *testing.T) {
	_, backend := newPublicationFixture(t)
	reader, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Toolsets: []string{ToolsetRegistry}}, "2026-07-28")
	publicationCall(t, ctx, reader, "gregale_search_tools", searchToolsArgs{Limit: 21}, true)
	publicationCall(t, ctx, reader, "gregale_describe_tool", describeToolArgs{Version: "latest", Name: "add"}, true)
}

func TestToolBuildingLostJournalResponseNeverReplays(t *testing.T) {
	f, backend := newPublicationFixture(t)
	f.loseBegin = true
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, LocalCLI: f.cli, Toolsets: []string{ToolsetBuildTools}}, "2026-07-28")
	plan := publicationCall(t, ctx, cs, "gregale_plan_tool_deploy", publicationArgs(publicationProject(t)), false)
	var id string
	_ = json.Unmarshal(plan["plan_id"], &id)
	// Either a surfaced transport failure or an internally retried begin is
	// safe. Once committed, neither response path may launch the publisher.
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_apply_tool_deploy", Arguments: applyArgs{PlanID: id}}); err != nil {
		t.Fatal(err)
	}
	out := publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
	if string(out["status"]) != `"running"` || string(out["durable"]) != "true" {
		t.Fatalf("lost-start recovery: %s", out["status"])
	}
	f.mu.Lock()
	deploys, retained := f.deploys, len(f.journal)
	record := f.journal[id]
	record.CreatedAt = time.Now().Add(-time.Hour)
	record.Deadline = time.Now().Add(-time.Minute)
	f.journal[id] = record
	f.mu.Unlock()
	if deploys != 0 || retained != 1 {
		t.Fatal("lost journal response authorized execution")
	}
	out = publicationCall(t, ctx, cs, "gregale_get_tool_deploy", planLookupArgs{PlanID: id}, false)
	if string(out["status"]) != `"outcome_unknown"` {
		t.Fatal("abandoned begin became retryable")
	}
	publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deploys != 0 {
		t.Fatal("abandoned journal replayed publication")
	}
}

func TestToolRegistryLargeSchemasUseBoundedCatalogChunks(t *testing.T) {
	f, backend := newPublicationFixture(t)
	catalog, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{{Name: "large_schema", InputSchema: map[string]any{"type": "object", "description": strings.Repeat("x", 100<<10)}}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mcphosting.MarshalContract(catalog)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	now := time.Now().UTC()
	f.journal[id] = api.ToolPublicationRecord{ID: id, AccountID: "account-one", Status: "completed", CreatedAt: now, Deadline: now.Add(api.ToolPublicationDeadline), EvidenceOrigin: "publisher_attested", DeploymentSourceSHA256: strings.Repeat("b", 64),
		Intent:  api.ToolPublicationIntent{App: "alpha", SourceSHA256: strings.Repeat("c", 64), FixturesSHA256: strings.Repeat("d", 64), AccessMode: "open", ToolScopes: map[string][]string{"large_schema": {}}, TestCases: []string{"reviewed"}},
		Outcome: &api.ToolPublicationOutcome{Status: "completed", DeploymentID: "published", CatalogSHA256: strings.Repeat("e", 64), Catalog: raw, ProtocolVerified: true, Checks: []api.ToolPublicationCheck{{Name: "reviewed", Status: "passed"}}}}
	reader, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Toolsets: []string{ToolsetRegistry}}, "2026-07-28")
	var restored []byte
	for offset := 0; offset != -1; {
		out := publicationCall(t, ctx, reader, "gregale_describe_tool", describeToolArgs{Version: id, Name: "large_schema", CatalogOffset: offset}, false)
		if string(out["tool_schema_inline"]) != "false" {
			t.Fatal("large schema exceeded inline bound")
		}
		var chunk, digest string
		_ = json.Unmarshal(out["catalog_base64"], &chunk)
		_ = json.Unmarshal(out["catalog_contract_sha256"], &digest)
		data, err := base64.StdEncoding.DecodeString(chunk)
		if err != nil || len(data) > toolTemplateChunkBytes || digest != toolpublication.Digest(raw) {
			t.Fatal("catalog chunk lost integrity")
		}
		restored = append(restored, data...)
		var next int
		_ = json.Unmarshal(out["next_catalog_offset"], &next)
		if next != -1 && next <= offset {
			t.Fatal("catalog continuation did not advance")
		}
		offset = next
	}
	if string(restored) != string(raw) {
		t.Fatal("large runtime schema changed across chunks")
	}
}

func TestToolRegistryLargePagesKeepContinuation(t *testing.T) {
	f, backend := newPublicationFixture(t)
	scopes := map[string][]string{}
	for i := range api.ToolPublicationMaxTools {
		scopes[fmt.Sprintf("tool_%02d_", i)+strings.Repeat("x", 110)] = []string{}
	}
	now := time.Now().UTC()
	for i := range api.ToolRegistryPageMax {
		id := fmt.Sprintf("%064x", i+1)
		f.journal[id] = api.ToolPublicationRecord{ID: id, AccountID: "account-one", Status: "completed", CreatedAt: now, Deadline: now.Add(api.ToolPublicationDeadline), EvidenceOrigin: "publisher_attested", DeploymentSourceSHA256: strings.Repeat("b", 64),
			Intent:  api.ToolPublicationIntent{App: "alpha", SourceSHA256: strings.Repeat("c", 64), FixturesSHA256: strings.Repeat("d", 64), AccessMode: "open", ToolScopes: scopes, TestCases: []string{"reviewed"}},
			Outcome: &api.ToolPublicationOutcome{Status: "completed", DeploymentID: "published", CatalogSHA256: strings.Repeat("e", 64)}}
	}
	reader, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Toolsets: []string{ToolsetRegistry}}, "2026-07-28")
	seen := map[string]bool{}
	cursor := ""
	for {
		out := publicationCall(t, ctx, reader, "gregale_search_tools", searchToolsArgs{Cursor: cursor, Limit: api.ToolRegistryPageMax}, false)
		var versions []api.ToolRegistryVersion
		_ = json.Unmarshal(out["versions"], &versions)
		if len(versions) == 0 {
			t.Fatal("empty continuation page")
		}
		if cursor == "" && len(versions) == api.ToolRegistryPageMax {
			t.Fatal("oversized native page was not bounded")
		}
		for _, version := range versions {
			if seen[version.Version] {
				t.Fatal("continuation repeated a version")
			}
			seen[version.Version] = true
		}
		_ = json.Unmarshal(out["next_cursor"], &cursor)
		if cursor == "" {
			break
		}
	}
	if len(seen) != api.ToolRegistryPageMax {
		t.Fatal("bounded pagination lost versions")
	}
}
