package agentundo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
)

// Things an agent deleted (ADR-20261112080601 §2, slice 4). An app and a
// managed PostgreSQL database are brought back through their restore routes,
// within their 7-day windows. A cron or a custom domain is created again from
// what it read as before the delete (a domain verifies again). A bucket is
// not undoable: buckets are deleted only when empty, but its name, access and
// lifecycle settings are not all readable back yet.

const reasonBucket = "Gregale cannot recreate a deleted bucket yet; create it again"

// restored are deletes undone by a restore route.
var restored = map[string]struct {
	noun    string
	restore func(r *http.Request) string
}{
	"DELETE /v1/apps/{slug}": {"app", func(r *http.Request) string { return appPath(r) + "/restore" }},
	"DELETE /v1/postgres/databases/{id}": {"Postgres database", func(r *http.Request) string {
		return "/v1/postgres/databases/" + url.PathEscape(r.PathValue("id")) + "/undelete"
	}},
}

// recreated are deletes undone by creating the thing again from its read.
var recreated = map[string]struct {
	noun    string
	read    func(r *http.Request) string
	create  string
	request any // the create route's request type
	id      string
}{
	"DELETE /v1/crons/{id}": {noun: "cron", create: "/v1/crons", request: api.CreateCronRequest{}, id: "id",
		read: func(r *http.Request) string { return "/v1/crons/" + url.PathEscape(r.PathValue("id")) }},
	"DELETE /v1/domains/{domain}": {noun: "domain", create: "/v1/domains", request: api.CreateCustomDomainRequest{}, id: "domain",
		read: func(r *http.Request) string { return "/v1/domains/" + url.PathEscape(r.PathValue("domain")) }},
}

func beginRestore(_ context.Context, p *Pending, r *http.Request) {
	kind := restored[r.Pattern]
	p.kind = api.AgentUndoKindDelete
	name := r.PathValue("slug")
	if name == "" {
		name = r.PathValue("id")
	}
	p.summary = "Deleted " + kind.noun + " " + name
	path := kind.restore(r)
	p.finish = func(context.Context, int, []byte) (*Inverse, *Check, string) {
		return &Inverse{Method: http.MethodPost, Path: path}, nil, ""
	}
}

func beginRecreate(ctx context.Context, p *Pending, r *http.Request) {
	kind := recreated[r.Pattern]
	p.kind = api.AgentUndoKindDelete
	before, ok := p.getObject(ctx, kind.read(r))
	name := r.PathValue("id") + r.PathValue("domain")
	p.summary = "Deleted " + kind.noun + " " + name
	if !ok {
		p.reason = reasonCapture
		return
	}
	body := map[string]json.RawMessage{}
	for _, field := range jsonFields(kind.request) {
		if v, ok := before[field]; ok && string(v) != "null" {
			body[field] = v
		}
	}
	p.finish = func(context.Context, int, []byte) (*Inverse, *Check, string) {
		return &Inverse{Method: http.MethodPost, Path: kind.create, Body: mustJSON(body)}, nil, ""
	}
}

func beginBucketDelete(_ context.Context, p *Pending, r *http.Request) {
	p.kind = api.AgentUndoKindDelete
	p.summary = "Deleted bucket " + r.PathValue("bucket") + " of app " + r.PathValue("slug")
	p.reason = reasonBucket
}

// jsonFields lists a struct's JSON field names.
func jsonFields(v any) []string {
	t := reflect.TypeOf(v)
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		if name := jsonName(t.Field(i)); name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}
