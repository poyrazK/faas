package liveserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/livequery"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/livequery/rest"
)

// RLS mode (ADR-1384): a client subscribes with a Data API read and its
// user's token; realtimed verifies the token and forwards its claims. The
// hub compiles the read and maintains it as the claims' role, so the
// database's row-level security decides every row.

// volatileClaims change with every token refresh and say nothing about who
// the user is; leaving them out lets a refreshed token keep its plan group.
var volatileClaims = []string{"exp", "iat", "nbf", "jti"}

// rlsRefusal is a live.error code and message refusing an RLS-mode frame.
type rlsRefusal struct{ code, msg string }

// session turns a frame's verified claims into the session the engine reads
// as, and when the claims expire.
func (h *Hub) session(sess *Session, f liveproto.ClientFrame) (*livequery.Session, time.Time, *rlsRefusal) {
	if !sess.trusted {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeForbidden, "RLS-mode live queries arrive only through realtimed, which verifies the user's token"}
	}
	if len(h.cfg.RLSRoles) == 0 || h.cfg.Pool == nil {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeUnavailable, "RLS-mode live queries are not enabled on this database"}
	}
	var claims map[string]any
	if err := json.Unmarshal(f.Claims, &claims); err != nil || claims == nil {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeProtocol, "the claims are not a JSON object"}
	}
	expires := time.Unix(f.ClaimsExpires, 0)
	if f.ClaimsExpires <= 0 || !h.cfg.Now().Before(expires) {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeSealExpired, "the token has expired; renew it"}
	}
	role := h.cfg.RLSAnonRole
	if r, ok := claims["role"].(string); ok {
		role = r
	}
	if role == "" || !slices.Contains(h.cfg.RLSRoles, role) {
		return nil, time.Time{}, &rlsRefusal{liveproto.CodeForbidden, fmt.Sprintf("the token's role %q may not read live queries", role)}
	}
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
		schema = "public"
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
