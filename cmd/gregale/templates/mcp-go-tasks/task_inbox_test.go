package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskInboxPaginationPreservesAccessAndMicroseconds(t *testing.T) {
	runtime, ctx := taskExecutionTestRuntime(t, mcpTaskConfig{})
	owner := &authContext{subject: "alice", resource: "https://example.test/mcp", clientID: "test"}
	foreign := &authContext{subject: "bob", resource: owner.resource, clientID: owner.clientID}
	create := func(identity *authContext) string {
		t.Helper()
		task, err := runtime.create(ctx, "build_report", map[string]any{"report": "inbox", "steps": float64(1)}, identity, nil)
		if err != nil {
			t.Fatal(err)
		}
		return task["taskId"].(string)
	}
	earlier, later := create(owner), create(owner)
	other := create(foreign)
	if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET expires_at=CASE task_id
		WHEN $2::uuid THEN date_trunc('second',statement_timestamp()) + interval '1 minute 1 microsecond'
		WHEN $3::uuid THEN date_trunc('second',statement_timestamp()) + interval '1 minute 2 microseconds'
		ELSE expires_at END WHERE namespace=$1`, runtime.namespace, earlier, later); err != nil {
		t.Fatal(err)
	}
	query := taskInboxQuery{Limit: 1, Status: "working"}
	statuses, tools := []string{"queued", "running"}, []string{"build_report"}
	read := func(identity *authContext, allowed []string, anchor *durableMCPTask) ([]map[string]any, string) {
		t.Helper()
		page, err := runtime.listTaskInbox(ctx, identity, query, statuses, allowed, anchor)
		if err != nil {
			t.Fatal(err)
		}
		cursor, _ := page["nextCursor"].(string)
		return page["tasks"].([]map[string]any), cursor
	}
	page, cursor := read(owner, tools, nil)
	if len(page) != 1 || page[0]["taskId"] != later || cursor == "" {
		t.Fatalf("unexpected first page: %v", page)
	}
	if page, _ := read(owner, []string{}, nil); len(page) != 0 {
		t.Fatal("explicit tool denial returned Tasks")
	}
	if page, _ := read(foreign, nil, nil); len(page) != 1 || page[0]["taskId"] != other {
		t.Fatal("another caller received unexpected Tasks")
	}
	if _, err := runtime.readTaskInboxCursor(foreign, query.Status, cursor); !errors.Is(err, errTaskInboxCursor) {
		t.Fatal("accepted a foreign cursor")
	}
	if _, err := runtime.readTaskInboxCursor(owner, "input_required", cursor); !errors.Is(err, errTaskInboxCursor) {
		t.Fatal("accepted a cursor for a different filter")
	}
	anchor, err := runtime.readTaskInboxCursor(owner, query.Status, cursor)
	if err != nil || anchor.ExpiresAt.Nanosecond() != 2000 {
		t.Fatalf("cursor lost its original precision: %v, %v", anchor, err)
	}
	if _, err := runtime.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET expires_at=clock_timestamp() WHERE namespace=$1 AND task_id=$2`, runtime.namespace, later); err != nil {
		t.Fatal(err)
	}
	page, cursor = read(owner, tools, anchor)
	if len(page) != 1 || page[0]["taskId"] != earlier || cursor != "" {
		t.Fatalf("could not continue past the expired anchor: %v", page)
	}
}

func TestTaskInboxWatchAndReviewedInputJourney(t *testing.T) {
	binary := os.Getenv("MCP_TASKS_TEST_GREGALE")
	if binary == "" {
		t.Skip("set MCP_TASKS_TEST_GREGALE to run the Gregale CLI inbox journey")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := taskExecutionTestRuntime(t, mcpTaskConfig{})
	database, err := url.Parse(os.Getenv("MCP_TASKS_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid PostgreSQL fixture URL")
	}
	query := database.Query()
	query.Set("search_path", fixture.pool.Config().ConnConfig.RuntimeParams["search_path"])
	database.RawQuery = query.Encode()
	t.Setenv("DATABASE_URL", database.String())
	t.Setenv("MCP_TASK_NAMESPACE", fixture.namespace)
	t.Setenv("MCP_TASK_OWNER_KEY", strings.Repeat("x", 32))
	config := mcpConfig{Endpoint: "/mcp", Tasks: json.RawMessage(`{"enabled":true,"poll_interval_ms":500}`)}
	runtime, err := startMCPTaskRuntime(config, "combined")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.close)
	auth, mint := taskReceiptJourneyAuth(t)
	owner, foreign, reduced := mint("alice", "mcp:tools reports:read"), mint("bob", "mcp:tools reports:read"), mint("alice", "mcp:tools")
	submitProfile, freshProfile := t.TempDir(), t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	const (
		dropBefore = iota + 1
		dropAfter
		changeBeforeWrite
	)
	var fault, writes, checks atomic.Int32
	var racingTask string
	emptyInbox := make(chan struct{})
	var initial sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.Header.Get("MCP-Method")
		switch method {
		case taskRespondReviewedMethod:
			writes.Add(1)
			switch fault.Swap(0) {
			case dropBefore:
				panic(http.ErrAbortHandler)
			case dropAfter:
				reply := httptest.NewRecorder()
				handleHTTP(config, auth, runtime, reply, r)
				if reply.Code != http.StatusOK || !bytes.Contains(reply.Body.Bytes(), []byte(`"outcome":"applied"`)) {
					t.Error("reviewed answer was not acknowledged after commit")
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				panic(http.ErrAbortHandler)
			case changeBeforeWrite:
				identity := &authContext{subject: "alice", resource: auth.resource}
				if err := runtime.updateInputs(r.Context(), racingTask, identity, map[string]any{"approval": map[string]any{"action": "decline"}}); err != nil {
					t.Error("could not record the concurrent answer")
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}
		case taskCheckReviewedMethod:
			checks.Add(1)
		}
		handleHTTP(config, auth, runtime, w, r)
		if method == taskInboxMethod {
			initial.Do(func() { close(emptyInbox) })
		}
	}))
	t.Cleanup(server.Close)
	baseArgs := func(verb string) []string {
		return []string{"mcp", "client", verb, "--url", server.URL + "/mcp", "--token-env", "GREGALE_JOURNEY_TOKEN", "--timeout", "8s", "--json"}
	}
	command := func(verb, profile, token string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, binary, append(baseArgs(verb), args...)...)
		cmd.Dir = profile
		cmd.Env = []string{"XDG_CONFIG_HOME=" + profile, "GREGALE_JOURNEY_TOKEN=" + token}
		return cmd
	}
	cli := func(verb, profile, token string, wantCode int, args ...string) []byte {
		t.Helper()
		cmd := command(verb, profile, token, args...)
		stdout, err := cmd.Output()
		if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != wantCode || (wantCode == 0 && err != nil) {
			t.Fatalf("%s did not exit with code %d: %v", verb, wantCode, err)
		}
		if bytes.Contains(stdout, []byte(token)) {
			t.Fatal("CLI leaked the bearer token")
		}
		return stdout
	}
	submit := func(report string) string {
		t.Helper()
		args, err := json.Marshal(map[string]any{"report": report, "request_approval": true, "steps": 1})
		if err != nil {
			t.Fatal(err)
		}
		out := cli("call", submitProfile, owner, 0, "--tool", "build_report", "--arguments", string(args), "--tasks",
			"--defer-form-input", "--task-receipt", filepath.Join(submitProfile, report+".json"))
		var handle struct {
			Result struct {
				TaskID string `json:"taskId"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out, &handle); err != nil || !taskIDPattern.MatchString(handle.Result.TaskID) {
			t.Fatal("submission did not return a Task ID")
		}
		return handle.Result.TaskID
	}

	watch := command("inbox", freshProfile, owner, "--status", "input_required", "--watch", "--interval", "1s", "--limit", "1")
	stream, err := watch.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := watch.Start(); err != nil {
		t.Fatal(err)
	}
	watchWaited := false
	defer func() {
		if !watchWaited {
			_ = watch.Process.Kill()
			_ = watch.Wait()
		}
	}()
	events := make(chan []byte, 2)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(stream)
		for scanner.Scan() {
			select {
			case events <- bytes.Clone(scanner.Bytes()):
			case <-ctx.Done():
				return
			}
		}
	}()
	select {
	case <-emptyInbox:
	case <-ctx.Done():
		t.Fatal("inbox watch did not start")
	}
	taskID := submit("inbox journey")
	select {
	case event := <-events:
		var metadata struct {
			TaskID        string `json:"taskId"`
			Status        string `json:"status"`
			NextAction    string `json:"nextAction"`
			CreatedAt     string `json:"createdAt"`
			LastUpdatedAt string `json:"lastUpdatedAt"`
		}
		decoder := json.NewDecoder(bytes.NewReader(event))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&metadata); err != nil || metadata.TaskID != taskID ||
			metadata.Status != "input_required" || metadata.NextAction != "provide_input" ||
			metadata.CreatedAt == "" || metadata.LastUpdatedAt == "" {
			t.Fatalf("watch did not report the original paused Task: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("watch did not discover the paused Task")
	}
	if err := watch.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err = watch.Wait()
	watchWaited = true
	if err != nil {
		t.Fatalf("watch did not stop cleanly: %v", err)
	}
	if entries, err := os.ReadDir(freshProfile); err != nil || len(entries) != 0 {
		t.Fatal("the fresh watch profile unexpectedly contains a receipt")
	}

	draft := func(id, name string) string {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for {
			var status string
			if err := runtime.pool.QueryRow(ctx, `SELECT status FROM gregale_mcp_tasks WHERE namespace=$1 AND task_id=$2::uuid`, runtime.namespace, id).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status == "input_required" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("Task did not pause, last status %s", status)
			}
			time.Sleep(25 * time.Millisecond)
		}
		path := filepath.Join(freshProfile, name+".json")
		cli("status", freshProfile, owner, 0, "--task-id", id, "--write-input-template", path)
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("reviewed input template is not private")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var review struct {
			TaskID      string                    `json:"taskId"`
			Version     int                       `json:"version"`
			Revision    string                    `json:"reviewRevision"`
			EndpointSHA string                    `json:"endpointSha256"`
			Forms       map[string]map[string]any `json:"forms"`
		}
		if err := json.Unmarshal(body, &review); err != nil || review.TaskID != id ||
			review.Version != 2 || !taskReviewRevisionPattern.MatchString(review.Revision) || review.Forms["approval"] == nil {
			t.Fatal("invalid revision-bound form template")
		}
		review.Forms["approval"]["response"] = map[string]any{"action": "accept", "content": map[string]any{"approved": true}}
		body, err = json.Marshal(review)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	respond := func(id, path, token string, code int) []byte {
		return cli("respond", freshProfile, token, code, "--task-id", id, "--reviewed-input-file", path)
	}

	review := draft(taskID, "reviewed")
	respond(taskID, review, foreign, 1)
	respond(taskID, review, reduced, 1)
	if writes.Load() != 0 {
		t.Fatal("inaccessible Task reached the reviewed mutation")
	}
	fault.Store(dropBefore)
	if result := respond(taskID, review, owner, 3); !bytes.Contains(result, []byte(`"unconfirmed"`)) {
		t.Fatal("lost reply before commit was not reported as unconfirmed")
	}
	fault.Store(dropAfter)
	if result := respond(taskID, review, owner, 0); !bytes.Contains(result, []byte(`"applied"`)) {
		t.Fatal("could not confirm the original answers after commit")
	}
	if result := cli("wait", freshProfile, owner, 0, "--task-id", taskID); !bytes.Contains(result, []byte("Report inbox journey is ready")) {
		t.Fatal("could not retrieve the original Task result")
	}
	respond(taskID, review, owner, 0)
	if writes.Load() != 2 || checks.Load() != 3 {
		t.Fatalf("review reconciliation repeated a mutation or missed a check: writes=%d checks=%d", writes.Load(), checks.Load())
	}

	racingTask = submit("racing")
	stale := draft(racingTask, "stale")
	fault.Store(changeBeforeWrite)
	respond(racingTask, stale, owner, 1)
	if result := cli("wait", freshProfile, owner, 0, "--task-id", racingTask); !bytes.Contains(result, []byte("Report was not approved.")) {
		t.Fatal("the stale review overrode the concurrent answer")
	}
}

func TestTaskInputReviewPreservesExactFormAndAnswerNumbers(t *testing.T) {
	key := []byte(strings.Repeat("x", 32))
	runtime := &mcpTaskRuntime{namespace: "review", payloadEncryptionVersion: 1, taskKeys: []mcpTaskKey{{
		ownerHashKey: deriveTaskKey(key, "gregale-mcp-task-owner:v1"),
		payloadKey:   deriveTaskKey(key, "gregale-mcp-task-payload:v1"),
	}}}
	task := durableMCPTask{ID: "786512e2-9e0d-44bd-8f29-789f320fe840", Status: "input_required"}
	request := func(number json.Number) map[string]any {
		return map[string]any{"method": "elicitation/create", "params": map[string]any{
			"message": "Review an amount", "requestedSchema": map[string]any{"type": "object",
				"properties": map[string]any{"amount": map[string]any{"const": number}}},
		}}
	}
	answer := func(number json.Number) map[string]any {
		return map[string]any{"action": "accept", "content": map[string]any{"amount": number}}
	}
	const reviewed, rounded = json.Number("9007199254740993"), json.Number("9007199254740992")
	state := taskInputState{Requests: map[string]any{"approval": request(reviewed)}, Responses: map[string]any{}}
	revision, err := runtime.taskInputRevision(task.ID, state.Requests)
	if err != nil {
		t.Fatal(err)
	}
	params := reviewedTaskInputParams{TaskID: task.ID, Revision: revision, Responses: map[string]any{"approval": answer(reviewed)}}
	expect := func(state taskInputState, apply bool, want string) {
		t.Helper()
		sealed, err := runtime.encrypt(state, task.ID, "input-state", maxTaskInputStateBytes)
		if err != nil {
			t.Fatal(err)
		}
		read, err := runtime.decryptReviewedTaskState(task.ID, sealed)
		if err != nil {
			t.Fatal(err)
		}
		got, err := runtime.matchReviewedTaskResponse(task, read, params, apply)
		if err != nil || got != want {
			t.Fatalf("outcome=%q want=%q err=%v", got, want, err)
		}
	}
	expect(state, true, "write")
	state.Requests["approval"] = request(rounded)
	expect(state, true, "stale")
	state.Requests["approval"] = request(reviewed)
	state.Responses["approval"] = answer(reviewed)
	expect(state, false, "applied")
	params.Responses["approval"] = answer(rounded)
	expect(state, false, "unconfirmed")
}
