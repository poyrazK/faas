package main

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	taskInboxExtension       = "io.gregale/task-inbox"
	taskInputReviewExtension = "io.gregale/task-input-review"
	taskInboxTimestamp       = "2006-01-02T15:04:05.000000Z"
)

var (
	taskInboxCursorPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}\.[A-Za-z0-9_-]{43}$`)
	errTaskInboxCursor     = errors.New("invalid MCP Task inbox cursor")
)

func checkTaskInboxIndex(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var ready bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_catalog.pg_index
		 WHERE indexrelid=to_regclass('gregale_mcp_tasks_inbox_idx')
		   AND indrelid='gregale_mcp_tasks'::regclass AND indisvalid AND indisready)`).Scan(&ready)
	return ready, err
}

func (runtime *mcpTaskRuntime) taskInboxAuthenticated(auth *authenticator) bool {
	return runtime != nil && runtime.pool != nil && len(runtime.taskKeys) > 0 && auth.mode == "external-oauth"
}

func (runtime *mcpTaskRuntime) addTaskInboxCapabilities(auth *authenticator, extensions map[string]any) {
	if runtime.taskInboxAuthenticated(auth) {
		extensions[taskInputReviewExtension] = map[string]any{}
		if runtime.taskInboxReady {
			extensions[taskInboxExtension] = map[string]any{}
		}
	}
}

// A nil list represents an unconfigured policy. A configured policy with no
// allowed tools must produce a non-nil empty list so PostgreSQL denies all.
func taskInboxToolNames(auth *authenticator, identity *authContext) []string {
	if !auth.toolPolicy.configured {
		return nil
	}
	names := make([]string, 0, len(auth.toolPolicy.rules))
	for name := range auth.toolPolicy.rules {
		if auth.toolAllowed(name, identity) == nil {
			names = append(names, name)
		}
	}
	return names
}

func serveTaskInboxRequest(w http.ResponseWriter, r *http.Request, request rpcRequest, auth *authenticator, identity *authContext, runtime *mcpTaskRuntime, version string) {
	if version != currentProtocol || !runtime.taskInboxAuthenticated(auth) ||
		(request.Method == taskInboxMethod && !runtime.taskInboxReady) {
		writeRPCError(w, request.ID, -32601, "method not found", http.StatusNotFound)
		return
	}
	if identity == nil || identity.subject == "" {
		writeRPCError(w, request.ID, -32002, "task not found", http.StatusNotFound)
		return
	}
	allowed := taskInboxToolNames(auth, identity)
	if request.Method == taskInboxMethod {
		runtime.serveTaskInbox(w, r, request, identity, allowed)
		return
	}
	runtime.serveTaskInputReview(w, r, request, identity, allowed)
}

type taskInboxQuery struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

func (runtime *mcpTaskRuntime) serveTaskInbox(w http.ResponseWriter, r *http.Request, request rpcRequest, identity *authContext, tools []string) {
	query := taskInboxQuery{Limit: 20}
	if (len(request.Params) != 0 && json.Unmarshal(request.Params, &query) != nil) || query.Limit < 1 || query.Limit > 50 {
		writeRPCError(w, request.ID, -32602, "invalid Task inbox parameters", http.StatusBadRequest)
		return
	}
	var statuses []string
	switch query.Status {
	case "":
	case "working":
		statuses = []string{"queued", "running"}
	case "input_required", "completed", "failed", "cancelled":
		statuses = []string{query.Status}
	default:
		writeRPCError(w, request.ID, -32602, "invalid Task inbox status", http.StatusBadRequest)
		return
	}

	anchor, err := runtime.readTaskInboxCursor(identity, query.Status, query.Cursor)
	if err != nil {
		writeRPCError(w, request.ID, -32602, errTaskInboxCursor.Error(), http.StatusBadRequest)
		return
	}
	result, err := runtime.listTaskInbox(r.Context(), identity, query, statuses, tools, anchor)
	if err != nil {
		writeRPCError(w, request.ID, -32603, "could not read Task inbox", http.StatusInternalServerError)
		return
	}
	writeRPCResult(w, request.ID, result)
}

func (runtime *mcpTaskRuntime) listTaskInbox(ctx context.Context, identity *authContext, query taskInboxQuery, statuses, tools []string, anchor *durableMCPTask) (map[string]any, error) {
	var expiry *time.Time
	var id *string
	if anchor != nil {
		expiry, id = &anchor.ExpiresAt, &anchor.ID
	}
	rows, err := runtime.pool.Query(ctx, `SELECT task_id::text,status,created_at,updated_at,expires_at
		  FROM gregale_mcp_tasks
		 WHERE namespace=$1 AND owner_hash=ANY($2::bytea[]) AND expires_at>clock_timestamp()
		   AND ($3::text[] IS NULL OR status=ANY($3)) AND ($4::text[] IS NULL OR tool_name=ANY($4))
		   AND ($5::timestamptz IS NULL OR (expires_at,task_id)<($5,$6::uuid))
		 ORDER BY expires_at DESC,task_id DESC LIMIT $7`,
		runtime.namespace, runtime.ownerHashes(identity), statuses, tools, expiry, id, query.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]map[string]any, 0, query.Limit)
	result := map[string]any{"resultType": "complete"}
	var last durableMCPTask
	for rows.Next() {
		var task durableMCPTask
		if err := rows.Scan(&task.ID, &task.Status, &task.CreatedAt, &task.UpdatedAt, &task.ExpiresAt); err != nil {
			return nil, err
		}
		if len(tasks) == query.Limit {
			result["nextCursor"] = runtime.taskInboxCursor(identity, query.Status, last)
			break
		}
		last = task
		tasks = append(tasks, runtime.wireMetadata(&task))
	}
	result["tasks"] = tasks
	return result, rows.Err()
}

func (runtime *mcpTaskRuntime) taskInboxMAC(identity *authContext, status, payload string) []byte {
	body, _ := json.Marshal([]string{runtime.namespace, identity.resource, identity.subject, identity.clientID, status, payload})
	return deriveTaskKey(runtime.taskKeys[0].ownerHashKey, "gregale-mcp-task-inbox:v1:"+string(body))
}

func (runtime *mcpTaskRuntime) taskInboxCursor(identity *authContext, status string, task durableMCPTask) string {
	// Preserve PostgreSQL's microseconds and avoid looking up the anchor row
	// again if it expires or is deleted before the caller continues.
	payload := base64.RawURLEncoding.EncodeToString([]byte(task.ExpiresAt.UTC().Format(taskInboxTimestamp) + "," + task.ID))
	return payload + "." + base64.RawURLEncoding.EncodeToString(runtime.taskInboxMAC(identity, status, payload))
}

func (runtime *mcpTaskRuntime) readTaskInboxCursor(identity *authContext, status, cursor string) (*durableMCPTask, error) {
	if cursor == "" {
		return nil, nil
	}
	if !taskInboxCursorPattern.MatchString(cursor) {
		return nil, errTaskInboxCursor
	}
	payload, signature, _ := strings.Cut(cursor, ".")
	encoding := base64.RawURLEncoding.Strict()
	mac, err := encoding.DecodeString(signature)
	if err != nil || !hmac.Equal(mac, runtime.taskInboxMAC(identity, status, payload)) {
		return nil, errTaskInboxCursor
	}
	body, err := encoding.DecodeString(payload)
	expiry, id, _ := strings.Cut(string(body), ",")
	at, timeErr := time.Parse(taskInboxTimestamp, expiry)
	if err != nil || timeErr != nil || !taskIDPattern.MatchString(id) {
		return nil, errTaskInboxCursor
	}
	return &durableMCPTask{ID: id, ExpiresAt: at}, nil
}
