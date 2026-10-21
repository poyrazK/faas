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

// RLS mode (ADR-20261016151947): a client subscribes with a Data API read and its
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

// gatewayClaims are owned by the Data API gateway (ADR-20261018170742), which
// overwrites them on every REST request. A live read must present the same
// claims, or a policy on gregale_access would show different rows live than
// over REST; an identity provider's token cannot set them either.
var gatewayClaims = []string{"gregale_access", "gregale_server_key", "gregale_sql_deadline", "gregale_refresh_hash", "gregale_serving_fingerprint"}

// rlsClaims canonicalizes verified token claims for an RLS-mode session: the
// claims a signed-in Data API request carries, without the volatile ones.
func rlsClaims(raw json.RawMessage) (string, bool) {
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil || claims == nil {
		return "", false
	}
	for _, k := range volatileClaims {
		delete(claims, k)
	}
	for _, k := range gatewayClaims {
		delete(claims, k)
	}
	claims["gregale_access"] = "user"
	canonical, err := json.Marshal(claims) // map keys sort: one text per claim set
	return string(canonical), err == nil
}

// sessionClaims canonicalizes the claims of an RLS-mode frame. A user's
// verified token claims become a signed-in session (rlsClaims). Anonymous and
// server-key reads arrive with the claims realtimed built after admitting
// them, which must be exactly the Data API gateway's claims for that kind.
func sessionClaims(access string, raw json.RawMessage) (string, bool) {
	if access == "" {
		return rlsClaims(raw)
	}
	if access != liveproto.AccessAnonymous && access != liveproto.AccessServer {
		return "", false
	}
	var claims map[string]string
	if err := json.Unmarshal(raw, &claims); err != nil || claims["gregale_access"] != access {
		return "", false
	}
	for k, v := range claims {
		switch {
		case k == "gregale_access":
		case access == liveproto.AccessServer && k == "gregale_server_key" && (v == "a" || v == "b"):
		case access == liveproto.AccessServer && k == "sub" && v != "":
		default:
			return "", false
		}
	}
	if access == liveproto.AccessServer && claims["gregale_server_key"] == "" {
		return "", false
	}
	canonical, err := json.Marshal(claims) // map keys sort: one text per claim set
	return string(canonical), err == nil
}

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
	if f.Anonymous || f.ServerKey != "" {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeProtocol, "realtimed admits anonymous and server-key reads before they reach the hub"}
	}
	canonical, ok := sessionClaims(f.Access, f.Claims)
	if !ok {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeProtocol, "the claims do not match the read's access kind"}
	}
	expires := time.Unix(f.ClaimsExpires, 0)
	if f.ClaimsExpires <= 0 || !h.cfg.Now().Before(expires) {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeSealExpired, "the token has expired; renew it"}
	}
	// The SQL role is the app's Data API login whatever the token's role
	// claim says, as the Data API gateway overwrites it (ADR-650).
	return &livequery.Session{Role: f.Role, Claims: canonical}, expires, nil
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
