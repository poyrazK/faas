package agentundo

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// maxCapturedBody bounds the request body a capture reads to learn which
// fields the agent set.
const maxCapturedBody = 1 << 20

// Pending is a capture begun before the handler ran.
type Pending struct {
	e       *Engine
	base    *http.Request
	acct    state.Account
	key     state.APIKey
	method  string
	path    string
	kind    string
	summary string
	// reason, when set, records the write as not undoable.
	reason string
	finish func(ctx context.Context, status int, body []byte) (*Inverse, *Check, string)
}

type beginFunc func(ctx context.Context, p *Pending, r *http.Request)

// routes are the agent writes Gregale can undo, or knows it cannot, by
// route pattern. A write on any other route is recorded as not undoable.
var routes = map[string]beginFunc{
	"PATCH /v1/apps/{slug}":                       beginAppSettings,
	"POST /v1/apps/{slug}/scaling":                beginScaling,
	"POST /v1/apps/{slug}/prewarm":                beginPrewarm,
	"PUT /v1/apps/{slug}/env/{key}":               beginEnv,
	"DELETE /v1/apps/{slug}/env/{key}":            beginEnv,
	"POST /v1/apps/{slug}/env/batch":              beginEnv,
	"DELETE /v1/apps/{slug}":                      beginRestore,
	"DELETE /v1/postgres/databases/{id}":          beginRestore,
	"DELETE /v1/crons/{id}":                       beginRecreate,
	"DELETE /v1/domains/{domain}":                 beginRecreate,
	"DELETE /v1/apps/{slug}/buckets/{bucket}":     beginBucketDelete,
	"POST /v1/apps":                               beginCreate,
	"POST /v1/apps/{slug}/buckets":                beginCreate,
	"POST /v1/crons":                              beginCreate,
	"POST /v1/domains":                            beginCreate,
	"POST /v1/postgres/databases":                 beginCreate,
	"POST /v1/apps/{slug}/deployments":            beginDeploy,
	"POST /v1/apps/{slug}/deployments/source-ref": beginDeploy,
	"POST /v1/apps/{slug}/deployments/dev-source": beginDeploy,
	"POST /v1/apps/{slug}/rollback":               beginDeploy,
	"PUT /v1/apps/{slug}/secrets/{key}":           beginSecret,
	"DELETE /v1/apps/{slug}/secrets/{key}":        beginSecret,
	"POST /v1/apps/{slug}/secrets/{key}/rotate":   beginSecret,
}

// Routes lists the route patterns with a capture, for tests that check
// each is still served.
func Routes() []string {
	out := make([]string, 0, len(routes))
	for pattern := range routes {
		out = append(out, pattern)
	}
	sort.Strings(out)
	return out
}

// Begin starts the capture of an agent's write r, as acct with key. It reads
// what the write will replace; call Finish once the handler answered.
func (e *Engine) Begin(r *http.Request, acct state.Account, key state.APIKey) *Pending {
	p := &Pending{
		e: e, base: r, acct: acct, key: key, method: r.Method, path: r.URL.Path,
		kind: api.AgentUndoKindOther, summary: r.Method + " " + r.URL.Path, reason: reasonUnknownRoute,
	}
	if r.Method == http.MethodDelete {
		p.kind = api.AgentUndoKindDelete
	}
	if begin, ok := routes[r.Pattern]; ok && e.Store != nil && e.Dispatch != nil {
		p.reason = ""
		begin(r.Context(), p, r)
	}
	return p
}

// Finish records the write once the handler answered status with body (as
// much of it as the middleware kept). A failed write changed nothing and is
// not recorded.
func (p *Pending) Finish(ctx context.Context, status int, body []byte, activityID string) error {
	if p == nil || p.e == nil || p.e.Store == nil || !is2xx(status) {
		return nil
	}
	rec := state.AgentUndoRecord{
		ActivityID: activityID, KeyID: p.key.ID, AccountID: p.acct.ID, OrgID: p.key.OrgID,
		Method: p.method, Path: truncate(p.path, 512), Kind: p.kind, Summary: truncate(p.summary, 512),
		CreatedAt: p.e.now(),
	}
	reason := p.reason
	if reason == "" && p.finish != nil {
		inv, chk, why := p.finish(ctx, status, body)
		switch {
		case why != "":
			reason = why
		case inv == nil:
			reason = reasonCapture
		default:
			rec.Inverse, _ = json.Marshal(inv)
			if chk != nil {
				rec.Guard, _ = json.Marshal(chk)
			}
		}
	}
	if len(rec.Inverse) == 0 {
		if reason == "" {
			reason = reasonCapture
		}
		rec.NotUndoableReason = truncate(reason, 512)
	}
	rec.Summary = truncate(p.summary, 512) // finish may name what was made
	return p.e.Store.RecordAgentUndo(ctx, rec)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// readBody reads the request body and puts it back for the handler.
func readBody(r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		return nil, true
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, maxCapturedBody+1))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(b), r.Body))
	return b, err == nil && len(b) <= maxCapturedBody
}

// target is path with r's query.
func target(path string, query url.Values) string {
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}

// getObject reads target as acct and decodes a JSON object.
func (p *Pending) getObject(ctx context.Context, path string) (map[string]json.RawMessage, bool) {
	return p.e.getObject(ctx, p.base, p.acct, path)
}

func (e *Engine) getObject(ctx context.Context, base *http.Request, acct state.Account, path string) (map[string]json.RawMessage, bool) {
	status, body := e.Dispatch(ctx, base, acct, http.MethodGet, path, nil)
	if status != http.StatusOK {
		return nil, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil || m == nil {
		return nil, false
	}
	return m, true
}

func appPath(r *http.Request) string { return "/v1/apps/" + url.PathEscape(r.PathValue("slug")) }

// --- app settings -----------------------------------------------------------

// undoableAppSettings are the PATCH /v1/apps/{slug} fields that read back
// from GET /v1/apps/{slug} in the form PATCH takes, so writing the earlier
// value restores it (TestAgentUndoAppSettingsRoundTrip). Others are not
// undoable: public_auth carries sealed credentials, service bindings read
// back in another shape, and app_protocol and the warm snapshot thresholds
// read back empty when unset, which PATCH cannot write.
var undoableAppSettings = []string{
	"autoscale_target_cpu_pct", "autoscale_target_rps", "consumer_auth_mode",
	"cors_default_enabled", "cors_default_origins", "cpu_millicores", "declared_routes",
	"egress_allowlist", "egress_ports", "eviction_priority", "function_concurrency",
	"idle_timeout_s", "maintenance_mode", "max_concurrency", "min_instances",
	"only_allow_declared_routes", "platform_tenant_required", "ram_mb", "release_policy",
	"release_probe_paths", "request_timeout_s", "require_authn", "require_signed",
	"resource_profile", "retry_policy", "revision_pin_ttl_seconds", "route_metrics_enabled",
	"session_affinity", "streaming_enabled", "version_affinity_cookie",
	"version_affinity_managed_cookie", "visibility", "warm_pool_size", "warm_snapshot_enabled",
	"websocket_enabled",
}

func beginAppSettings(ctx context.Context, p *Pending, r *http.Request) {
	p.kind = api.AgentUndoKindSettings
	slug := r.PathValue("slug")
	body, ok := readBody(r)
	var fields map[string]json.RawMessage
	if !ok || json.Unmarshal(body, &fields) != nil {
		p.summary, p.reason = "Changed settings of app "+slug, reasonCapture
		return
	}
	keys := sortedKeys(fields)
	p.summary = "Changed settings of app " + slug
	if len(keys) > 0 {
		p.summary += ": " + strings.Join(keys, ", ")
	}
	if len(keys) == 0 {
		p.reason = reasonNothing
		return
	}
	var other []string
	for _, k := range keys {
		if !slices.Contains(undoableAppSettings, k) {
			other = append(other, k)
		}
	}
	if len(other) > 0 {
		p.reason = "Gregale cannot undo changes to " + strings.Join(other, ", ") + " yet"
		return
	}
	get := target(appPath(r), r.URL.Query())
	before, ok := p.getObject(ctx, get)
	if !ok {
		p.reason = reasonCapture
		return
	}
	restore := map[string]json.RawMessage{}
	for _, k := range keys {
		restore[k] = valueOrZero(before, k)
	}
	p.finish = func(ctx context.Context, _ int, _ []byte) (*Inverse, *Check, string) {
		after, ok := p.getObject(ctx, get)
		if !ok {
			return nil, nil, reasonCapture
		}
		left := map[string]json.RawMessage{}
		for _, k := range keys {
			left[k] = valueOrNull(after, k)
		}
		inv := &Inverse{Method: http.MethodPatch, Path: get, Body: mustJSON(restore)}
		return inv, &Check{Kind: CheckFields, Path: get, Fields: left}, ""
	}
}

// valueOrZero is m[k], or the zero value of k's type in a PATCH when the
// read left it out (it was empty).
func valueOrZero(m map[string]json.RawMessage, k string) json.RawMessage {
	if v, ok := m[k]; ok {
		return v
	}
	return zeroAppSetting(k)
}

func valueOrNull(m map[string]json.RawMessage, k string) json.RawMessage {
	if v, ok := m[k]; ok {
		return v
	}
	return json.RawMessage("null")
}

// zeroAppSetting is the empty value of an UpdateAppRequest field, by JSON
// name: [] for a list, {} for a map, else the type's zero.
func zeroAppSetting(name string) json.RawMessage {
	t := reflect.TypeOf(api.UpdateAppRequest{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if jsonName(f) != name {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		var v reflect.Value
		switch ft.Kind() {
		case reflect.Slice:
			v = reflect.MakeSlice(ft, 0, 0)
		case reflect.Map:
			v = reflect.MakeMap(ft)
		default:
			v = reflect.Zero(ft)
		}
		b, err := json.Marshal(v.Interface())
		if err == nil {
			return b
		}
	}
	return json.RawMessage("null")
}

func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	name, _, _ := strings.Cut(tag, ",")
	return name
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// --- scaling ----------------------------------------------------------------

var scalingSettings = []string{"cpu_millicores", "max_concurrency", "min_instances", "ram_mb"}

func beginScaling(ctx context.Context, p *Pending, r *http.Request) {
	p.kind = api.AgentUndoKindSettings
	slug := r.PathValue("slug")
	p.summary = "Changed scaling of app " + slug
	body, ok := readBody(r)
	var req map[string]json.RawMessage
	if !ok || json.Unmarshal(body, &req) != nil {
		p.reason = reasonCapture
		return
	}
	var keys []string
	for _, k := range scalingSettings {
		if v, ok := req[k]; ok && string(v) != "null" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		p.reason = reasonNothing
		return
	}
	p.summary += ": " + strings.Join(keys, ", ")
	get := target(appPath(r)+"/scaling", r.URL.Query())
	before, ok := p.getObject(ctx, get)
	if !ok {
		p.reason = reasonCapture
		return
	}
	var beforeSettings map[string]json.RawMessage
	if json.Unmarshal(before["settings"], &beforeSettings) != nil {
		p.reason = reasonCapture
		return
	}
	restore := map[string]json.RawMessage{"expected_app_id": req["expected_app_id"]}
	if v, ok := req["expected_environment_id"]; ok {
		restore["expected_environment_id"] = v
	}
	for _, k := range keys {
		restore[k] = valueOrNull(beforeSettings, k)
	}
	p.finish = func(ctx context.Context, _ int, _ []byte) (*Inverse, *Check, string) {
		after, ok := p.getObject(ctx, get)
		var afterSettings map[string]json.RawMessage
		if !ok || json.Unmarshal(after["settings"], &afterSettings) != nil {
			return nil, nil, reasonCapture
		}
		left := map[string]json.RawMessage{}
		for _, k := range keys {
			left[k] = valueOrNull(afterSettings, k)
		}
		inv := &Inverse{Method: http.MethodPost, Path: get, Body: mustJSON(restore), NewRequestID: true}
		return inv, &Check{Kind: CheckFields, Path: get, Object: "settings", Fields: left, RevisionField: "revision"}, ""
	}
}

// --- prewarm ----------------------------------------------------------------

func beginPrewarm(_ context.Context, p *Pending, r *http.Request) {
	p.kind = api.AgentUndoKindSettings
	p.summary = "Prewarmed app " + r.PathValue("slug")
	app := appPath(r)
	p.finish = func(_ context.Context, _ int, body []byte) (*Inverse, *Check, string) {
		var out struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(body, &out) != nil || out.ID == "" {
			return nil, nil, reasonCapture
		}
		return &Inverse{Method: http.MethodDelete, Path: app + "/prewarms/" + url.PathEscape(out.ID), GoneIsDone: true}, nil, ""
	}
}

// --- env vars and secrets -----------------------------------------------------

func beginEnv(ctx context.Context, p *Pending, r *http.Request) {
	p.kind = api.AgentUndoKindSettings
	slug, key := r.PathValue("slug"), r.PathValue("key")
	switch r.Method {
	case http.MethodPut:
		p.summary = "Set env var " + key + " on app " + slug
	case http.MethodDelete:
		p.summary = "Removed env var " + key + " from app " + slug
	default:
		p.summary = "Changed env vars of app " + slug
	}
	// A guarded rollback names its target; a named environment's id is not
	// readable here yet, so only the app's own variables are undone.
	if scope := r.URL.Query().Get("scope"); scope != "" && scope != "default" {
		p.reason = "Gregale cannot undo an environment's env vars yet"
		return
	}
	revisions := revisionsPath(appPath(r), r.URL.Query())
	before, ok := p.newestRevision(ctx, revisions)
	if !ok {
		p.reason = reasonCapture
		return
	}
	if before == 0 {
		p.reason = reasonNoRevision
		return
	}
	app, ok := p.getObject(ctx, appPath(r))
	var appID string
	if !ok || json.Unmarshal(app["id"], &appID) != nil || appID == "" {
		p.reason = reasonCapture
		return
	}
	rollback := target(appPath(r)+"/config/rollback", r.URL.Query())
	p.finish = func(ctx context.Context, _ int, body []byte) (*Inverse, *Check, string) {
		var out struct {
			Revision int64 `json:"revision"`
		}
		after := int64(0)
		if json.Unmarshal(body, &out) == nil {
			after = out.Revision
		}
		if after == 0 {
			if after, ok = p.newestRevision(ctx, revisions); !ok {
				return nil, nil, reasonCapture
			}
		}
		if after == before {
			return nil, nil, reasonNothing
		}
		inv := &Inverse{Method: http.MethodPost, Path: rollback, Body: mustJSON(map[string]any{"revision": before, "expected_app_id": appID})}
		return inv, &Check{Kind: CheckConfig, Path: revisions, AfterRevision: after}, ""
	}
}

func beginSecret(_ context.Context, p *Pending, r *http.Request) {
	p.kind = api.AgentUndoKindSettings
	slug, key := r.PathValue("slug"), r.PathValue("key")
	switch {
	case r.Method == http.MethodDelete:
		p.summary = "Removed secret " + key + " from app " + slug
	case strings.HasSuffix(r.URL.Path, "/rotate"):
		p.summary = "Rotated secret " + key + " of app " + slug
	default:
		p.summary = "Set secret " + key + " on app " + slug
	}
	p.reason = reasonSecret
}

// revisionsPath lists a scope's newest config revision.
func revisionsPath(app string, query url.Values) string {
	q := url.Values{}
	for k, v := range query {
		q[k] = slices.Clone(v)
	}
	q.Set("limit", "1")
	return target(app+"/config/revisions", q)
}

func (p *Pending) newestRevision(ctx context.Context, revisions string) (int64, bool) {
	return p.e.newestRevision(ctx, p.base, p.acct, revisions)
}

// newestRevision is the scope's newest config revision, 0 when it has none.
func (e *Engine) newestRevision(ctx context.Context, base *http.Request, acct state.Account, revisions string) (int64, bool) {
	status, body := e.Dispatch(ctx, base, acct, http.MethodGet, revisions, nil)
	if status != http.StatusOK {
		return 0, false
	}
	var out api.ListConfigRevisionsResponse
	if json.Unmarshal(body, &out) != nil {
		return 0, false
	}
	if len(out.Revisions) == 0 {
		return 0, true
	}
	return out.Revisions[0].Revision, true
}

// revisionDetailPath is the detail of revision n of the scope revisions
// lists.
func revisionDetailPath(revisions string, n int64) string {
	path, query, _ := strings.Cut(revisions, "?")
	q, _ := url.ParseQuery(query)
	q.Del("limit")
	return target(path+"/"+strconv.FormatInt(n, 10), q)
}

// UndoableAppSettings lists the PATCH /v1/apps/{slug} fields undo restores.
func UndoableAppSettings() []string { return slices.Clone(undoableAppSettings) }

// ZeroAppSetting is the value undo writes for a field the read left out.
func ZeroAppSetting(name string) json.RawMessage { return zeroAppSetting(name) }
