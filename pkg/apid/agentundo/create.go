package agentundo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
)

// Things an agent created (ADR-20261112080601 §2, slice 3). Undoing one
// deletes it through its own delete route, so the delete keeps that route's
// safety: an app's delete parks it for 7 days (restorable), a bucket that
// holds objects is refused, and a database's delete can be undone for 7
// days. A thing already gone counts as undone.

// created describes how to name and delete one kind of thing.
type created struct {
	noun string
	// idField names the answer's field that identifies the new thing.
	idField string
	// deletePath is the route that deletes it, given the request and its id.
	deletePath func(r *http.Request, id string) string
}

var createdKinds = map[string]created{
	"POST /v1/apps": {noun: "app", idField: "slug", deletePath: func(_ *http.Request, id string) string {
		return "/v1/apps/" + url.PathEscape(id)
	}},
	"POST /v1/apps/{slug}/buckets": {noun: "bucket", idField: "id", deletePath: func(r *http.Request, id string) string {
		return appPath(r) + "/buckets/" + url.PathEscape(id)
	}},
	"POST /v1/crons": {noun: "cron", idField: "id", deletePath: func(_ *http.Request, id string) string {
		return "/v1/crons/" + url.PathEscape(id)
	}},
	"POST /v1/domains": {noun: "domain", idField: "domain", deletePath: func(_ *http.Request, id string) string {
		return "/v1/domains/" + url.PathEscape(id)
	}},
	"POST /v1/postgres/databases": {noun: "Postgres database", idField: "id", deletePath: func(_ *http.Request, id string) string {
		return "/v1/postgres/databases/" + url.PathEscape(id)
	}},
}

func beginCreate(_ context.Context, p *Pending, r *http.Request) {
	kind := createdKinds[r.Pattern]
	p.kind = api.AgentUndoKindCreate
	p.summary = "Created " + kind.noun
	if slug := r.PathValue("slug"); slug != "" {
		p.summary += " on app " + slug
	}
	p.finish = func(_ context.Context, _ int, body []byte) (*Inverse, *Check, string) {
		var out map[string]json.RawMessage
		var id string
		if json.Unmarshal(body, &out) != nil || json.Unmarshal(out[kind.idField], &id) != nil || id == "" {
			return nil, nil, reasonCapture
		}
		p.summary = "Created " + kind.noun + " " + id
		if slug := r.PathValue("slug"); slug != "" {
			p.summary += " on app " + slug
		}
		return &Inverse{Method: http.MethodDelete, Path: kind.deletePath(r, id), GoneIsDone: true}, nil, ""
	}
}
