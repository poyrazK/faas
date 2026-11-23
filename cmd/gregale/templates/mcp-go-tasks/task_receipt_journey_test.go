package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Run with the receipt-capable Gregale CLI and a disposable PostgreSQL database.
func TestTaskReceiptRecoversLostSubmission(t *testing.T) {
	testTaskReceiptJourney(t, false)
}

func TestTaskReceiptResumesReviewedInput(t *testing.T) {
	testTaskReceiptJourney(t, true)
}

// Each journey loses its first submission reply after committing the Task.
func testTaskReceiptJourney(t *testing.T, reviewedInput bool) {
	t.Helper()
	binary := os.Getenv("MCP_TASKS_TEST_GREGALE")
	if binary == "" {
		t.Skip("set MCP_TASKS_TEST_GREGALE to run the Gregale CLI receipt journey")
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
	owner := mint("alice", "mcp:tools reports:read")
	foreign := mint("bob", "mcp:tools reports:read")
	reduced := mint("alice", "mcp:tools")
	clientDir := t.TempDir()
	receiptPath := filepath.Join(clientDir, "task-receipt.json")
	ctx, cancel := context.WithTimeout(t.Context(), 85*time.Second)
	defer cancel()

	type droppedReply struct {
		id      string
		expires time.Time
		err     error
	}
	dropped := make(chan droppedReply, 1)
	var intercepted, changePrompt atomic.Bool
	var updates, changedReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.Header.Get("MCP-Method")
		if method == "tasks/update" {
			updates.Add(1)
		}
		if method == "tasks/get" && changePrompt.Load() {
			reply := httptest.NewRecorder()
			handleHTTP(config, auth, runtime, reply, r)
			// Change only the served prompt, leaving the durable form pending.
			original := []byte(`"message":"Approve generating this report?"`)
			modified := []byte(`"message":"Approve a different report?"`)
			body := reply.Body.Bytes()
			if bytes.Contains(body, original) {
				changedReads.Add(1)
				body = bytes.Replace(body, original, modified, 1)
			}
			for name, values := range reply.Header() {
				w.Header()[name] = values
			}
			w.WriteHeader(reply.Code)
			_, _ = w.Write(body)
			return
		}
		if method != "gregale/tasks/submit" || !intercepted.CompareAndSwap(false, true) {
			handleHTTP(config, auth, runtime, w, r)
			return
		}
		info, err := os.Lstat(receiptPath)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			dropped <- droppedReply{err: fmt.Errorf("private receipt was unavailable before submission")}
			return
		}
		reply := httptest.NewRecorder()
		handleHTTP(config, auth, runtime, reply, r)
		var response struct {
			Result struct {
				TaskID string `json:"taskId"`
			} `json:"result"`
		}
		if err := json.Unmarshal(reply.Body.Bytes(), &response); reply.Code != http.StatusOK || err != nil || response.Result.TaskID == "" {
			dropped <- droppedReply{err: fmt.Errorf("keyed request returned HTTP %d without a Task handle", reply.Code)}
			return
		}
		var result droppedReply
		err = fixture.pool.QueryRow(r.Context(), `SELECT task_id::text, expires_at FROM gregale_mcp_tasks WHERE namespace=$1`, fixture.namespace).Scan(&result.id, &result.expires)
		if err != nil {
			dropped <- droppedReply{err: fmt.Errorf("read committed Task: %w", err)}
			return
		}
		if result.id != response.Result.TaskID {
			dropped <- droppedReply{err: errors.New("keyed reply had no matching committed Task")}
			return
		}
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			dropped <- droppedReply{err: err}
			return
		}
		dropped <- result
		_ = conn.Close()
	}))
	t.Cleanup(server.Close)
	endpoint := server.URL + "/mcp"

	cli := func(verb, token string, wantCode int, args ...string) []byte {
		t.Helper()
		invocation := append([]string{"mcp", "client", verb, "--url", endpoint,
			"--token-env", "GREGALE_JOURNEY_TOKEN", "--timeout", "6s", "--json"}, args...)
		cmd := exec.CommandContext(ctx, binary, invocation...)
		cmd.Dir = clientDir
		cmd.Env = []string{"XDG_CONFIG_HOME=" + clientDir, "GREGALE_JOURNEY_TOKEN=" + token}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.Output()
		if bytes.Contains(stdout, []byte(token)) || bytes.Contains(stderr.Bytes(), []byte(token)) {
			t.Fatal("CLI leaked its bearer token")
		}
		if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != wantCode {
			select {
			case lost := <-dropped:
				if lost.err != nil {
					t.Fatal(lost.err)
				}
			default:
			}
			t.Fatalf("mcp client %s expected exit %d, got %v\n%s", verb, wantCode, err, &stderr)
		}
		if wantCode != 0 && len(stdout) != 0 {
			t.Fatalf("mcp client %s returned JSON in a failed request", verb)
		}
		return stdout
	}
	const originalArgs = `{"report":"receipt journey","steps":1}`
	const formArgs = `{"report":"receipt journey","steps":1,"request_approval":true}`
	submitArgs := func(path string) []string {
		args := []string{"--tool", "build_report", "--tasks", "--task-receipt", path, "--arguments"}
		if reviewedInput {
			return append(args, formArgs, "--defer-form-input")
		}
		return append(args, originalArgs)
	}
	cli("call", owner, 3, submitArgs(receiptPath)...)
	var committed droppedReply
	select {
	case committed = <-dropped:
		if committed.err != nil {
			t.Fatal(committed.err)
		}
	default:
		t.Fatal("no committed Task reply was dropped")
	}
	lookup := func(token string, code int) []byte {
		return cli("status", token, code, "--task-receipt", receiptPath, "--metadata-only")
	}

	file, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Version        int    `json:"version"`
		EndpointSHA256 string `json:"endpointSha256"`
		SubmissionKey  string `json:"submissionKey"`
	}
	decoder := json.NewDecoder(bytes.NewReader(file))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil || receipt.Version != 1 || receipt.EndpointSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(endpoint))) {
		t.Fatal("invalid private Task receipt")
	}
	if reviewedInput {
		waitForForm := func(id string) {
			t.Helper()
			formCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				var status string
				err := fixture.pool.QueryRow(formCtx, `SELECT status FROM gregale_mcp_tasks WHERE namespace=$1 AND task_id=$2`,
					fixture.namespace, id).Scan(&status)
				if err != nil {
					t.Fatal(err)
				}
				if status == "input_required" {
					return
				}
				select {
				case <-formCtx.Done():
					t.Fatalf("Task did not reach input_required, last status %q", status)
				case <-ticker.C:
				}
			}
		}
		waitForForm(committed.id)
		templatePath := filepath.Join(clientDir, "reviewed-input.json")
		var snapshot struct {
			TaskID     string `json:"taskId"`
			Status     string `json:"status"`
			NextAction string `json:"nextAction"`
		}
		if err := json.Unmarshal(cli("status", owner, 0, "--task-receipt", receiptPath,
			"--write-input-template", templatePath), &snapshot); err != nil ||
			snapshot.TaskID != committed.id || snapshot.Status != "input_required" || snapshot.NextAction != "provide_input" {
			t.Fatalf("receipt did not recover the original pending form: %+v, %v", snapshot, err)
		}
		info, err := os.Lstat(templatePath)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatal("review template was not saved as a private regular file")
		}
		type form struct {
			Message    string          `json:"message"`
			Schema     json.RawMessage `json:"schema"`
			FormSHA256 string          `json:"formSha256"`
			Response   json.RawMessage `json:"response"`
		}
		type review struct {
			Version        int             `json:"version"`
			EndpointSHA256 string          `json:"endpointSha256"`
			TaskID         string          `json:"taskId"`
			Forms          map[string]form `json:"forms"`
		}
		readReview := func(path string) review {
			t.Helper()
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var template review
			if err := json.Unmarshal(body, &template); err != nil {
				t.Fatal(err)
			}
			return template
		}
		template := readReview(templatePath)
		approval := template.Forms["approval"]
		if template.Version != 1 || template.EndpointSHA256 != receipt.EndpointSHA256 || template.TaskID != committed.id ||
			len(template.Forms) != 1 || approval.Message != "Approve generating this report?" || string(approval.Response) != "null" {
			t.Fatalf("unexpected review template: %+v", template)
		}
		wait := func(token string, code int, path string) []byte {
			return cli("wait", token, code, "--task-receipt", path, "--reviewed-input-file", templatePath)
		}
		wait(owner, 1, receiptPath)
		approval.Response = json.RawMessage(`{"action":"accept","content":{"approved":true}}`)
		template.Forms["approval"] = approval
		filled, err := json.Marshal(template)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(templatePath, filled, 0o600); err != nil {
			t.Fatal(err)
		}

		var other struct {
			Result struct {
				TaskID string `json:"taskId"`
			} `json:"result"`
		}
		otherReceipt := filepath.Join(clientDir, "other-receipt.json")
		if err := json.Unmarshal(cli("call", owner, 0, submitArgs(otherReceipt)...), &other); err != nil ||
			other.Result.TaskID == "" || other.Result.TaskID == committed.id {
			t.Fatalf("second submission did not return a distinct Task: %+v, %v", other, err)
		}
		waitForForm(other.Result.TaskID)
		otherPath := filepath.Join(clientDir, "other-input.json")
		cli("status", owner, 0, "--task-receipt", otherReceipt, "--write-input-template", otherPath)
		otherTemplate := readReview(otherPath)
		if otherTemplate.TaskID != other.Result.TaskID || approval.FormSHA256 == "" ||
			otherTemplate.Forms["approval"].FormSHA256 != approval.FormSHA256 {
			t.Fatal("second Task must request the same form under the same ID")
		}

		wait(owner, 1, otherReceipt)
		wait(reduced, 1, receiptPath)
		changePrompt.Store(true)
		wait(owner, 1, receiptPath)
		changePrompt.Store(false)
		if changedReads.Load() != 1 || updates.Load() != 0 {
			t.Fatalf("expected one changed form read and zero updates before approval, got %d reads, %d updates",
				changedReads.Load(), updates.Load())
		}

		var completed struct {
			TaskID string `json:"taskId"`
			Status string `json:"status"`
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(wait(owner, 0, receiptPath), &completed); err != nil ||
			completed.TaskID != committed.id || completed.Status != "completed" || len(completed.Result.Content) != 1 ||
			completed.Result.Content[0].Text != "Report receipt journey is ready after 1 steps." {
			t.Fatalf("review did not complete the original Task: %+v, %v", completed, err)
		}
		if updates.Load() != 1 {
			t.Fatalf("expected exactly one update for the approved form, got %d", updates.Load())
		}
		var rows, original, pending int
		if err := fixture.pool.QueryRow(ctx, `SELECT count(*),
		    count(*) FILTER (WHERE task_id=$2 AND submission_key=$3 AND expires_at=$4
		        AND status='completed' AND attempt_count=1 AND result_encrypted IS NOT NULL),
		    count(*) FILTER (WHERE task_id=$5 AND status='input_required' AND attempt_count=1 AND result_encrypted IS NULL)
		    FROM gregale_mcp_tasks WHERE namespace=$1`, fixture.namespace, committed.id, receipt.SubmissionKey,
			committed.expires, other.Result.TaskID).Scan(&rows, &original, &pending); err != nil ||
			rows != 2 || original != 1 || pending != 1 {
			t.Fatalf("expected the original durable result and an untouched pending Task, got rows=%d completed=%d pending=%d err=%v",
				rows, original, pending, err)
		}
		return
	}

	issuedAt, valid := taskSubmissionIssuedAt(receipt.SubmissionKey)
	if !valid {
		t.Fatal("receipt has no UUIDv7 submission key")
	}
	submit := func(token string, code int, args string) []byte {
		return cli("call", token, code, "--tool", "build_report", "--tasks", "--task-submission-key", receipt.SubmissionKey,
			"--arguments", args)
	}
	var replay struct {
		Result struct {
			ResultType string `json:"resultType"`
			TaskID     string `json:"taskId"`
		} `json:"result"`
	}
	if err := json.Unmarshal(submit(owner, 0, originalArgs), &replay); err != nil || replay.Result.ResultType != "task" || replay.Result.TaskID != committed.id {
		t.Fatal("matching replay did not return the committed Task handle")
	}
	submit(owner, 1, `{"report":"changed","steps":1}`)
	submit(foreign, 1, originalArgs)
	// Cross the real retry deadline without editing the key or the database clock.
	timer := time.NewTimer(time.Until(issuedAt.Add(time.Minute + 100*time.Millisecond)))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	submit(owner, 1, originalArgs)
	var status map[string]string
	if err := json.Unmarshal(lookup(owner, 0), &status); err != nil || len(status) != 4 || status["taskId"] != committed.id || status["status"] != "completed" || status["nextAction"] != "read_result" {
		t.Fatal("metadata-only lookup did not recover the completed Task")
	}
	if _, err := time.Parse(time.RFC3339Nano, status["lastUpdatedAt"]); err != nil {
		t.Fatal(err)
	}
	lookup(foreign, 1)
	lookup(reduced, 1)
	full := cli("status", owner, 0, "--task-id", committed.id)
	if !bytes.Contains(full, []byte("Report receipt journey is ready after 1 steps.")) {
		t.Fatal("explicit result read did not return the expected report")
	}
	var rows, results int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE task_id=$2 AND submission_key=$3
		AND expires_at=$4 AND status='completed' AND attempt_count=1 AND result_encrypted IS NOT NULL)
		FROM gregale_mcp_tasks WHERE namespace=$1`, fixture.namespace, committed.id, receipt.SubmissionKey, committed.expires).Scan(&rows, &results); err != nil || rows != 1 || results != 1 {
		t.Fatalf("expected one Task with one durable result, got tasks=%d results=%d error=%v", rows, results, err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE gregale_mcp_tasks SET expires_at=clock_timestamp() WHERE namespace=$1 AND task_id=$2`, fixture.namespace, committed.id); err != nil {
		t.Fatal(err)
	}
	lookup(owner, 1)
}

func taskReceiptJourneyAuth(t *testing.T) (*authenticator, func(string, string) string) {
	t.Helper()
	const issuer, audience = "https://issuer.example", "https://mcp.example/mcp"
	auth := testScopedAuthenticator(t, `{
		"mode":"external-oauth","issuer":"https://issuer.example",
		"jwks_url":"https://issuer.example/jwks","resource":"https://mcp.example/mcp",
		"scopes":["mcp:tools"],"tool_scopes":{"build_report":["reports:read"]}}`)
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	auth.keyResolver.keys = []jsonWebKey{{Kty: "RSA", Kid: "journey",
		N: encode(private.N.Bytes()), E: encode(big.NewInt(int64(private.E)).Bytes())}}
	auth.keyResolver.loadedAt = time.Now()
	auth.keyResolver.lastRefresh = time.Now()
	return auth, func(subject, scopes string) string {
		claims, err := json.Marshal(map[string]any{"iss": issuer, "aud": audience,
			"sub": subject, "scope": scopes, "exp": time.Now().Add(5 * time.Minute).Unix()})
		if err != nil {
			t.Fatal(err)
		}
		body := encode([]byte(`{"alg":"RS256","kid":"journey"}`)) + "." + encode(claims)
		digest := sha256.Sum256([]byte(body))
		signature, err := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		return body + "." + encode(signature)
	}
}
