// Push event payload (slice 7). Mirrors github's
// `push` webhook body — only the fields githubd cares about are
// decoded. The upstream GitHub schema is much richer; we keep the
// surface narrow so a schema change on GitHub's side is a local
// edit here.
package githubd

import (
	"encoding/json"
	"errors"
)

// PushEvent is the subset of the GitHub push webhook githubd parses
// to decide if the push lands on a bound app's branch.
//
// Decoded from the raw request body inside
// (Service).onPush. We deliberately skip the dozens of fields
// GitHub attaches (pusher, organization, sender) — they're audit
// signal only and end up in slog if requested.
type PushEvent struct {
	Ref        string         `json:"ref"`        // "refs/heads/main"
	After      string         `json:"after"`      // commit SHA the head now points at
	Repository PushRepository `json:"repository"` // repo identity
	Pusher     PushPusher     `json:"pusher"`     // optional audit
}

// PushRepository is the bits of `repository` the dispatch logic
// needs to look up the binding. FullName is the canonical
// "owner/name" handle used in the app bindings table.
type PushRepository struct {
	FullName string `json:"full_name"`
	Name     string `json:"name"`
	HTMLURL  string `json:"html_url"`
}

// PushPusher is the actor who triggered the push. Captured for the
// dashboard's audit log; never trusted for auth.
type PushPusher struct {
	Name string `json:"name"`
}

// DecodePush parses a raw GitHub push body into a PushEvent. The
// caller is responsible for verifying the signature BEFORE
// decoding; DecodePush only surfaces parse errors.
func DecodePush(body []byte) (PushEvent, error) {
	var ev PushEvent
	if len(body) == 0 {
		return ev, errors.New("githubd: empty push body")
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return ev, err
	}
	if ev.Ref == "" || ev.After == "" || ev.Repository.FullName == "" {
		return ev, errors.New("githubd: push missing required fields (ref/after/repository.full_name)")
	}
	return ev, nil
}
