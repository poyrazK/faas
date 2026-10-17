package liveserver

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/livequery"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/livequery/rest"
)

// RLS mode (ADR-1384): a client subscribes with a Data API read and its
// user's token; realtimed verifies the token and forwards its claims with
// the endpoint app's Data API binding login. The hub compiles the read and
// maintains it as that login with the claims, as the Data API gateway runs
// it (ADR-650), so the database's row-level security decides every row.
//
// Sealed queries from realtimed also read as a login: the app's read-only
// (else read-write) binding, whose grants the customer controls.

// volatileClaims change with every token refresh and say nothing about who
// the user is; leaving them out lets a refreshed token keep its plan group.
var volatileClaims = []string{"exp", "iat", "nbf", "jti"}

// defaultDataAPISchema is the schema a read names none of: the Data API's
// first exposed schema by default (ADR-650), the only one its login may
// read unless DATA_API_SCHEMAS adds others.
const defaultDataAPISchema = "api"

// rlsRefusal is a live.error code and message refusing an RLS-mode frame.
type rlsRefusal struct{ code, msg string }

// sealedSession is who a sealed query reads as: the login realtimed names
// (the app's binding), or the engine's own login for a direct development
// session, which may not name one.
func sealedSession(sess *Session, f liveproto.ClientFrame) (*livequery.Session, *rlsRefusal) {
	switch {
	case !sess.trusted && f.Role != "":
		return nil, &rlsRefusal{liveproto.CodeForbidden, "only realtimed names the login a query reads as"}
	case !sess.trusted:
		return nil, nil
	case f.Role == "":
		return nil, &rlsRefusal{liveproto.CodeForbidden, "the query names no login to read as"}
	}
	return &livequery.Session{Role: f.Role, Claims: "{}"}, nil
}

// session turns a frame's verified claims into the session the engine reads
// as, and when the claims expire.
func (h *Hub) session(sess *Session, f liveproto.ClientFrame) (*livequery.Session, time.Time, *rlsRefusal) {
	if !sess.trusted {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeForbidden, "RLS-mode live queries arrive only through realtimed, which verifies the user's token"}
	}
	if h.cfg.Pool == nil {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeUnavailable, "RLS-mode live queries are not enabled on this database"}
	}
	if f.Role == "" {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeForbidden, "the read names no Data API login to read as"}
	}
	var claims map[string]any
	if err := json.Unmarshal(f.Claims, &claims); err != nil || claims == nil {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeProtocol, "the claims are not a JSON object"}
	}
	expires := time.Unix(f.ClaimsExpires, 0)
	if f.ClaimsExpires <= 0 || !h.cfg.Now().Before(expires) {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeSealExpired, "the token has expired; renew it"}
	}
	// The SQL role is the app's Data API login whatever the token's role
	// claim says, as the Data API gateway overwrites it (ADR-650).
	role := f.Role
	for _, k := range volatileClaims {
		delete(claims, k)
	}
	canonical, err := json.Marshal(claims) // map keys sort: one text per claim set
	if err != nil {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeInternal, "encode the claims"}
	}
	return &livequery.Session{Role: role, Claims: string(canonical)}, expires, nil
}

// compileREST compiles a Data API read against the database's catalog.
func (h *Hub) compileREST(ctx context.Context, f liveproto.ClientFrame) (*rest.Compiled, *rlsRefusal) {
	if f.Database != h.cfg.Database {
		return nil, &rlsRefusal{liveproto.CodeForbidden, "the read names another database"}
	}
	req, err := rest.Parse(f.Rest)
	if err != nil {
		return nil, &rlsRefusal{liveproto.CodeInvalidQuery, err.Error()}
	}
	schema := f.Schema
	if schema == "" {
		schema = defaultDataAPISchema
	}
	conn, err := h.cfg.Pool.Acquire(ctx)
	if err != nil {
		return nil, &rlsRefusal{liveproto.CodeUnavailable, "the database is unavailable"}
	}
	defer conn.Release()
	compiled, err := rest.Compile(ctx, rest.PG{Conn: conn.Conn()}, schema, req)
	switch {
	case errors.Is(err, rest.ErrInvalid):
		return nil, &rlsRefusal{liveproto.CodeInvalidQuery, err.Error()}
	case err != nil:
		return nil, &rlsRefusal{liveproto.CodeInternal, "read the catalog"}
	}
	return compiled, nil
}

func rlsFingerprint(f liveproto.ClientFrame, s *livequery.Session) string {
	return "rls\x00" + f.Database + "\x00" + f.Schema + "\x00" + f.Rest + "\x00" + s.Role + "\x00" + s.Claims
}
