package middleware

import (
	"context"
	"net/http"
)

// AccountNote lets the daemon that mounts RequireSession learn which account
// a request's credential belongs to, even when the middleware refuses the
// request before calling the account handler (ADR-20261114090000): a revoked
// or expired API key, a suspended account, a key limited to other projects.
// apid records those refusals for the account as it records the ones its
// handlers return.
//
// The daemon puts a note on the request context with WithAccountNote before
// RequireSession runs; the middleware fills AccountID as soon as a
// credential resolves to an account. Reached is for the daemon: its
// account-handler chain sets it when the request got past authentication, so
// the outer layer does not record a refusal twice.
type AccountNote struct {
	AccountID string
	Reached   bool
}

type accountNoteKey struct{}

// WithAccountNote returns ctx carrying note.
func WithAccountNote(ctx context.Context, note *AccountNote) context.Context {
	return context.WithValue(ctx, accountNoteKey{}, note)
}

// AccountNoteFrom returns the note on ctx, or nil.
func AccountNoteFrom(ctx context.Context) *AccountNote {
	note, _ := ctx.Value(accountNoteKey{}).(*AccountNote)
	return note
}

// noteAccount records the account a credential resolved to on the request's
// note, when there is one. An empty id leaves the note unchanged.
func noteAccount(r *http.Request, accountID string) {
	if note := AccountNoteFrom(r.Context()); note != nil && accountID != "" {
		note.AccountID = accountID
	}
}
