package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const taskSubmissionWindow = time.Minute

var errTaskSubmissionUnavailable = errors.New("MCP Task submission key is unavailable")

// The additive migration is optional for ordinary Tasks during a rolling
// upgrade. Advertise keyed submission only when its uniqueness fence exists.
func checkTaskSubmissionIndex(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var ready bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_catalog.pg_index i
		JOIN pg_catalog.pg_attribute ns ON ns.attrelid=i.indrelid AND ns.attname='namespace' AND NOT ns.attisdropped
		JOIN pg_catalog.pg_attribute key ON key.attrelid=i.indrelid AND key.attname='submission_key' AND NOT key.attisdropped
		WHERE i.indexrelid=to_regclass('gregale_mcp_tasks_submission_key_idx')
			AND i.indrelid='gregale_mcp_tasks'::regclass
			AND i.indisvalid AND i.indisready AND i.indisunique AND i.indimmediate
			AND i.indnkeyatts=2 AND i.indkey[0]=ns.attnum AND i.indkey[1]=key.attnum
			AND pg_catalog.pg_get_expr(i.indpred,i.indrelid)='(submission_key IS NOT NULL)')`).Scan(&ready)
	return ready, err
}

func (runtime *mcpTaskRuntime) supportsTaskSubmission(auth *authenticator) bool {
	return runtime != nil && runtime.taskSubmissionReady && auth.mode == "external-oauth"
}

func (runtime *mcpTaskRuntime) submittedTaskHandle(id string, created, updated, expires time.Time) map[string]any {
	out := runtime.wireMetadata(&durableMCPTask{
		ID: id, Status: "queued", CreatedAt: created, UpdatedAt: updated, ExpiresAt: expires,
	})
	out["resultType"] = "task"
	out["statusMessage"] = "Task accepted for processing."
	return out
}

// The caller holds the same namespace admission lock used for new Tasks and
// expired-row cleanup. A match returns the original handle without writing.
func (runtime *mcpTaskRuntime) replayTaskSubmission(ctx context.Context, tx pgx.Tx, key string, issuedAt time.Time, name string, args map[string]any, inputMethods []string, owners [][]byte) (map[string]any, error) {
	var withinWindow bool
	deadline := issuedAt.Add(taskSubmissionWindow)
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()>=$1 AND clock_timestamp()<$2`, issuedAt, deadline).Scan(&withinWindow); err != nil {
		return nil, err
	}
	if !withinWindow {
		return nil, errTaskSubmissionUnavailable
	}
	var task durableMCPTask
	var accessible bool
	var encrypted []byte
	err := tx.QueryRow(ctx, `SELECT task_id::text,tool_name,created_at,updated_at,expires_at,input_methods,arguments_encrypted,
		owner_hash=ANY($3::bytea[]) AND expires_at>clock_timestamp()
		FROM gregale_mcp_tasks WHERE namespace=$1 AND submission_key=$2::uuid`, runtime.namespace, key, owners).
		Scan(&task.ID, &task.ToolName, &task.CreatedAt, &task.UpdatedAt, &task.ExpiresAt, &task.InputMethods, &encrypted, &accessible)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !accessible || task.ToolName != name || !slices.Equal(task.InputMethods, inputMethods) {
		return nil, errTaskSubmissionUnavailable
	}
	var original map[string]any
	if err := runtime.decrypt(encrypted, task.ID, "arguments", &original); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(original, args) {
		return nil, errTaskSubmissionUnavailable
	}
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()<LEAST($1::timestamptz,$2::timestamptz)`, deadline, task.ExpiresAt).Scan(&withinWindow); err != nil {
		return nil, err
	}
	if !withinWindow {
		return nil, errTaskSubmissionUnavailable
	}
	return runtime.submittedTaskHandle(task.ID, task.CreatedAt, task.UpdatedAt, task.ExpiresAt), nil
}

func (runtime *mcpTaskRuntime) recordTaskSubmission(ctx context.Context, tx pgx.Tx, id, key string, issuedAt time.Time) error {
	// Check the database clock again after any admission or owner lock wait.
	tag, err := tx.Exec(ctx, `UPDATE gregale_mcp_tasks SET submission_key=$3::uuid
		WHERE namespace=$1 AND task_id=$2::uuid AND clock_timestamp()>=$4 AND clock_timestamp()<$5`,
		runtime.namespace, id, key, issuedAt, issuedAt.Add(taskSubmissionWindow))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errTaskSubmissionUnavailable
	}
	return nil
}

func serveTaskSubmissionLookup(w http.ResponseWriter, r *http.Request, request rpcRequest, auth *authenticator, identity *authContext, runtime *mcpTaskRuntime, version string) {
	if version != currentProtocol || !runtime.supportsTaskSubmission(auth) {
		writeRPCError(w, request.ID, -32601, "method not found", http.StatusNotFound)
		return
	}
	var params struct {
		Key string `json:"submissionKey"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		writeRPCError(w, request.ID, -32602, "submissionKey must be a lowercase UUIDv7", http.StatusBadRequest)
		return
	}
	if _, valid := taskSubmissionIssuedAt(params.Key); !valid {
		writeRPCError(w, request.ID, -32602, "submissionKey must be a lowercase UUIDv7", http.StatusBadRequest)
		return
	}
	var task durableMCPTask
	// This path reads metadata columns only. It does not open sealed payloads
	// or associate owner aliases, and still checks the caller's current scope.
	err := runtime.pool.QueryRow(r.Context(), `SELECT task_id::text,tool_name,status,created_at,updated_at,expires_at
		FROM gregale_mcp_tasks WHERE namespace=$1 AND submission_key=$2::uuid
			AND owner_hash=ANY($3::bytea[]) AND expires_at>clock_timestamp()`,
		runtime.namespace, params.Key, runtime.ownerHashes(identity)).
		Scan(&task.ID, &task.ToolName, &task.Status, &task.CreatedAt, &task.UpdatedAt, &task.ExpiresAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeRPCError(w, request.ID, -32603, "could not read Task status", http.StatusInternalServerError)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) || auth.toolAllowed(task.ToolName, identity) != nil {
		writeRPCError(w, request.ID, -32002, "task not found", http.StatusNotFound)
		return
	}
	writeRPCResult(w, request.ID, runtime.wireMetadata(&task))
}
