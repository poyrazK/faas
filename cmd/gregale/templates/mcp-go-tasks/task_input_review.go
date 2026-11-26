package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

const maxReviewedTaskForms = 16

var taskReviewRevisionPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type reviewedTaskInputParams struct {
	TaskID    string         `json:"taskId"`
	Revision  string         `json:"reviewRevision"`
	Responses map[string]any `json:"responses"`
}

func (runtime *mcpTaskRuntime) serveTaskInputReview(w http.ResponseWriter, r *http.Request, request rpcRequest, identity *authContext, tools []string) {
	var params reviewedTaskInputParams
	decoder := json.NewDecoder(bytes.NewReader(request.Params))
	decoder.UseNumber()
	if decoder.Decode(&params) != nil || !taskIDPattern.MatchString(params.TaskID) {
		writeRPCError(w, request.ID, -32602, "taskId must be a UUID", http.StatusBadRequest)
		return
	}
	params.TaskID = strings.ToLower(params.TaskID)
	var result map[string]any
	var err error
	if request.Method == taskInputReviewMethod {
		result, err = runtime.getTaskInputReview(r.Context(), params.TaskID, identity, tools)
	} else {
		if !taskReviewRevisionPattern.MatchString(params.Revision) || !validReviewedTaskResponses(params.Responses) {
			writeRPCError(w, request.ID, -32602, "invalid reviewed Task input revision or responses", http.StatusBadRequest)
			return
		}
		if err := validateTaskResponseSize(params.Responses); err != nil {
			writeRPCError(w, request.ID, -32602, err.Error(), http.StatusBadRequest)
			return
		}
		result, err = runtime.reviewTaskResponse(r.Context(), params, identity, tools, request.Method == taskRespondReviewedMethod)
	}
	switch {
	case errors.Is(err, errMCPTaskNotFound):
		writeRPCError(w, request.ID, -32002, "task not found", http.StatusNotFound)
	case errors.Is(err, errInvalidMCPTaskInput):
		writeRPCError(w, request.ID, -32602, err.Error(), http.StatusBadRequest)
	case err != nil:
		writeRPCError(w, request.ID, -32603, "could not process reviewed Task input", http.StatusInternalServerError)
	default:
		writeRPCResult(w, request.ID, result)
	}
}

func validReviewedTaskResponses(responses map[string]any) bool {
	if len(responses) < 1 || len(responses) > maxReviewedTaskForms {
		return false
	}
	for id, value := range responses {
		response, ok := value.(map[string]any)
		if !ok || id == "" || taskStringLength(id) > 128 {
			return false
		}
		switch response["action"] {
		case "accept":
			if content, ok := response["content"].(map[string]any); !ok || content == nil || len(response) != 2 {
				return false
			}
		case "decline", "cancel":
			if len(response) != 1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func reviewableTaskRequests(requests map[string]any) bool {
	if len(requests) < 1 || len(requests) > maxReviewedTaskForms {
		return false
	}
	for _, value := range requests {
		request, _ := value.(map[string]any)
		params, ok := request["params"].(map[string]any)
		if request["method"] != "elicitation/create" || !ok || taskElicitationMode(params) != "form" {
			return false
		}
	}
	return true
}

type taskInputReviewReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (runtime *mcpTaskRuntime) readTaskReviewRow(ctx context.Context, db taskInputReviewReader, id string, identity *authContext, tools []string, lock bool) (durableMCPTask, []byte, error) {
	query := `SELECT task_id::text,status,created_at,updated_at,expires_at,input_state_encrypted
		FROM gregale_mcp_tasks
		WHERE namespace=$1 AND task_id=$2::uuid AND owner_hash=ANY($3::bytea[])
		  AND ($4::text[] IS NULL OR tool_name=ANY($4)) AND expires_at>clock_timestamp()`
	if lock {
		// A row may expire while PostgreSQL waits for another writer.
		query = `WITH locked AS MATERIALIZED (` + query + ` FOR UPDATE)
			SELECT * FROM locked WHERE expires_at>clock_timestamp()`
	}
	var task durableMCPTask
	var encrypted []byte
	err := db.QueryRow(ctx, query, runtime.namespace, id, runtime.ownerHashes(identity), tools).
		Scan(&task.ID, &task.Status, &task.CreatedAt, &task.UpdatedAt, &task.ExpiresAt, &encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		err = errMCPTaskNotFound
	}
	return task, encrypted, err
}

func (runtime *mcpTaskRuntime) decryptReviewedTaskState(taskID string, encrypted []byte) (taskInputState, error) {
	if len(encrypted) == 0 {
		return taskInputState{}, nil
	}
	plain, err := runtime.decryptPayload(encrypted, taskID, "input-state")
	if err != nil {
		return taskInputState{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	// Keep exact JSON number values in form signatures and recorded answers.
	decoder.UseNumber()
	var state taskInputState
	if err := decoder.Decode(&state); err != nil || state.Requests == nil || state.Responses == nil {
		return taskInputState{}, errors.New("invalid encrypted Task input state")
	}
	return state, nil
}

func (runtime *mcpTaskRuntime) taskInputRevision(id string, requests map[string]any) (string, error) {
	body, err := json.Marshal([]any{runtime.namespace, id, requests})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(deriveTaskKey(runtime.taskKeys[0].ownerHashKey, "gregale-mcp-task-input-review:v1:"+string(body))), nil
}

func (runtime *mcpTaskRuntime) getTaskInputReview(ctx context.Context, id string, identity *authContext, tools []string) (map[string]any, error) {
	task, encrypted, err := runtime.readTaskReviewRow(ctx, runtime.pool, id, identity, tools, false)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"resultType": "complete", "task": runtime.wireMetadata(&task)}
	if task.Status != "input_required" {
		return out, nil
	}
	state, err := runtime.decryptReviewedTaskState(id, encrypted)
	if err != nil {
		return nil, err
	}
	requests := outstandingTaskInputRequests(state)
	if reviewableTaskRequests(requests) {
		revision, err := runtime.taskInputRevision(id, requests)
		if err != nil {
			return nil, err
		}
		out["reviewRevision"], out["inputRequests"] = revision, requests
	}
	return out, nil
}

func (runtime *mcpTaskRuntime) reviewTaskResponse(ctx context.Context, params reviewedTaskInputParams, identity *authContext, tools []string, apply bool) (map[string]any, error) {
	var db taskInputReviewReader = runtime.pool
	var tx pgx.Tx
	if apply {
		var err error
		tx, err = runtime.pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		db = tx
	}
	task, encrypted, err := runtime.readTaskReviewRow(ctx, db, params.TaskID, identity, tools, apply)
	if err != nil {
		return nil, err
	}
	state, err := runtime.decryptReviewedTaskState(task.ID, encrypted)
	if err != nil {
		return nil, err
	}
	outcome, err := runtime.matchReviewedTaskResponse(task, state, params, apply)
	if err != nil {
		return nil, err
	}
	if outcome == "write" {
		for id, response := range params.Responses {
			state.Responses[id] = response
		}
		task, err = runtime.persistTaskResponses(ctx, tx, task.ID, state)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		outcome = "applied"
	}
	return map[string]any{"resultType": "complete", "task": runtime.wireMetadata(&task), "outcome": outcome}, nil
}

func (runtime *mcpTaskRuntime) matchReviewedTaskResponse(task durableMCPTask, state taskInputState, params reviewedTaskInputParams, apply bool) (string, error) {
	// Past requests are immutable by ID. Confirm exact recorded answers even
	// after the worker resumes or parks on a different set of forms.
	recorded := make(map[string]any, len(params.Responses))
	for id, value := range params.Responses {
		request, known := state.Requests[id]
		if known && reflect.DeepEqual(value, state.Responses[id]) {
			recorded[id] = request
		}
	}
	if len(recorded) == len(params.Responses) && reviewableTaskRequests(recorded) {
		revision, err := runtime.taskInputRevision(task.ID, recorded)
		if err != nil {
			return "", err
		}
		if hmac.Equal([]byte(revision), []byte(params.Revision)) {
			return "applied", nil
		}
	}
	if !apply {
		return "unconfirmed", nil
	}

	pending := outstandingTaskInputRequests(state)
	if task.Status != "input_required" || len(pending) != len(params.Responses) || !reviewableTaskRequests(pending) {
		return "stale", nil
	}
	for id := range params.Responses {
		if _, ok := pending[id]; !ok {
			return "stale", nil
		}
	}
	revision, err := runtime.taskInputRevision(task.ID, pending)
	if err != nil {
		return "", err
	}
	if !hmac.Equal([]byte(revision), []byte(params.Revision)) {
		return "stale", nil
	}
	return "write", nil
}
