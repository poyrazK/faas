package livequery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Remote compilation (ADR-20261016151947). Compiling a query needs PostgreSQL's
// parser, which runs as WebAssembly: starting it costs tens of MiB of
// resident memory, and a database VM's memory is the customer's. Rounds
// never parse; they run statements compiled ahead of time. So an engine
// inside a database VM hands compilation to a compiler outside it
// (livequeryd on the host), and its parser never starts. Plans travel as
// JSON.

// Compiler compiles a query's SQL into a plan.
type Compiler func(ctx context.Context, sql string) (*Plan, error)

// ErrCompilerUnavailable means the compiler could not answer; the query
// itself may be fine, so nothing is closed for it.
var ErrCompilerUnavailable = errors.New("livequery: compiler unavailable")

// compile uses the engine's compiler, or compiles in process with conn.
func (e *Engine) compile(ctx context.Context, conn *pgx.Conn, sql string) (*Plan, error) {
	if e.Compiler != nil {
		return e.Compiler(ctx, sql)
	}
	return Compile(ctx, conn, sql)
}

// compileRequest and compileResponse are the compiler's wire format.
type compileRequest struct {
	SQL string `json:"sql"`
}

type compileResponse struct {
	Plan  *Plan         `json:"plan,omitempty"`
	Error *compileError `json:"error,omitempty"`
}

type compileError struct {
	// Kind is "unsupported" (no live strategy accepts the query) or
	// "invalid" (PostgreSQL refused it).
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// CompilerHandler serves POST /compile, compiling against the database the
// pool reaches.
func CompilerHandler(pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req compileRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || strings.TrimSpace(req.SQL) == "" {
			http.Error(w, "a JSON body with sql is required", http.StatusBadRequest)
			return
		}
		conn, err := pool.Acquire(r.Context())
		if err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		defer conn.Release()
		var resp compileResponse
		plan, err := Compile(r.Context(), conn.Conn(), req.SQL)
		switch {
		case err == nil:
			resp.Plan = plan
		case errors.Is(err, ErrUnsupported):
			resp.Error = &compileError{Kind: "unsupported", Message: err.Error()}
		case r.Context().Err() != nil || conn.Conn().IsClosed():
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		default:
			resp.Error = &compileError{Kind: "invalid", Message: err.Error()}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// RemoteCompiler compiles through a CompilerHandler at baseURL.
func RemoteCompiler(baseURL string, client *http.Client) Compiler {
	if client == nil {
		client = http.DefaultClient
	}
	url := strings.TrimSuffix(baseURL, "/") + "/compile"
	return func(ctx context.Context, sql string) (*Plan, error) {
		body, err := json.Marshal(compileRequest{SQL: sql})
		if err != nil {
			return nil, fmt.Errorf("livequery: encode compile request: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("livequery: compile request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCompilerUnavailable, err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
			return nil, fmt.Errorf("%w: %s: %s", ErrCompilerUnavailable, res.Status, strings.TrimSpace(string(msg)))
		}
		var resp compileResponse
		if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&resp); err != nil {
			return nil, fmt.Errorf("%w: decode plan: %w", ErrCompilerUnavailable, err)
		}
		switch {
		case resp.Error != nil && resp.Error.Kind == "unsupported":
			return nil, fmt.Errorf("%w: %s", ErrUnsupported, strings.TrimPrefix(resp.Error.Message, ErrUnsupported.Error()+": "))
		case resp.Error != nil:
			return nil, errors.New(resp.Error.Message)
		case resp.Plan == nil:
			return nil, fmt.Errorf("%w: empty response", ErrCompilerUnavailable)
		}
		return resp.Plan, nil
	}
}
