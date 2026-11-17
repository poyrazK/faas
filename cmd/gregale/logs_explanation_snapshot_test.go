package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const explanationDeploymentID = "7971fae0-6fd8-4425-b65e-e0c231ad1155"

type explanationReceipt struct {
	Type          string `json:"type"`
	App           string `json:"app_slug"`
	Deployment    string `json:"deployment_id"`
	Status        string `json:"deployment_status"`
	Source        string `json:"source"`
	FailureReason string `json:"failure_reason"`
	FailureCode   string `json:"failure_code"`
	ErrorCount    int    `json:"error_count"`
	WarnCount     int    `json:"warn_count"`
	InfoCount     int    `json:"info_count"`
	LastError     string `json:"last_error"`
	Patterns      []struct {
		Pattern string `json:"pattern"`
		Count   int    `json:"count"`
	} `json:"top_patterns"`
	Observed     int      `json:"observed_lines"`
	Skipped      int      `json:"skipped_records"`
	Shortened    bool     `json:"messages_shortened"`
	Complete     bool     `json:"complete"`
	EndReason    string   `json:"end_reason"`
	ReadError    string   `json:"read_error"`
	Warnings     []string `json:"warnings"`
	NextCommands []string `json:"next_commands"`
}

func readExplanationReceipt(t *testing.T, output string) explanationReceipt {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(output))
	var receipt explanationReceipt
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatalf("invalid explanation JSON: %v: %s", err, output)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("stdout contains more than one JSON object: %v", err)
	}
	return receipt
}

func runExplanationCLI(t *testing.T, jsonMode bool, args ...string) (int, string, string) {
	t.Helper()
	var output, stderr bytes.Buffer
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	*main.OsStdout, *main.OsStderr = &output, &stderr
	defer func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr }()
	command := append([]string{"logs"}, args...)
	if jsonMode {
		command = append([]string{"--json"}, command...)
	}
	code := main.Run(command)
	return code, output.String(), stderr.String()
}

func explanationServer(t *testing.T, status string, stream func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("explanation attempted a write: %s", r.Method)
			w.WriteHeader(405)
			return
		}
		switch r.URL.Path {
		case "/v1/deployments/" + explanationDeploymentID:
			main.WriteJSONTest(w, api.DeploymentResponse{ID: explanationDeploymentID, AppID: "app-demo", Status: status, Error: "build command failed", ErrorCode: "build_failed", ErrorHint: "inspect the release"})
		case "/v1/apps/api":
			main.WriteJSONTest(w, api.AppResponse{ID: "app-demo", Slug: "api"})
		case "/v1/deployments/" + explanationDeploymentID + "/logs", "/v1/apps/api/logs":
			if status == "failed" && r.URL.Path != "/v1/deployments/"+explanationDeploymentID+"/logs" || status == "live" && r.URL.Path != "/v1/apps/api/logs" {
				t.Error("incorrect log source")
				w.WriteHeader(500)
				return
			}
			if r.URL.Query().Get("follow") == "1" {
				t.Error("snapshot followed live logs")
			}
			if status == "live" && r.URL.Query().Get("deployment") != explanationDeploymentID {
				t.Error("runtime source lost deployment filter")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			stream(w, r)
		default:
			t.Errorf("unexpected read: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_fixture")
	t.Setenv("FAAS_JSON", "0")
	t.Chdir(t.TempDir())
	return server
}

const explanationLogFrames = "event: log\ndata: {\"line\":\"booting\",\"stream\":\"stdout\",\"level\":\"info\"}\n\n" +
	"event: log\ndata: {\"line\":\"connection refused\",\"stream\":\"stderr\",\"level\":50}\n\n" +
	"event: log\ndata: {\"line\":\"connection refused\",\"level\":\"error\"}\n\n" +
	"event: log\ndata: 2026-11-12T18:00:00Z warn queue delayed\n\n"

func TestLogExplanationJSONAndTextShareSummary(t *testing.T) {
	for _, status := range []string{"failed", "live"} {
		t.Run(status, func(t *testing.T) {
			explanationServer(t, status, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, explanationLogFrames+"event: end\ndata: {}\n\n")
			})
			args := []string{"api", "--deployment", explanationDeploymentID, "--explain"}
			code, output, stderr := runExplanationCLI(t, true, args...)
			if code != 0 {
				t.Fatalf("JSON snapshot failed: %d: %s", code, stderr)
			}
			receipt := readExplanationReceipt(t, output)
			if receipt.Type != "explanation" || receipt.App != "api" || receipt.Deployment != explanationDeploymentID || receipt.Status != status || !receipt.Complete || receipt.EndReason != "end" || receipt.Observed != 4 || receipt.ErrorCount != 2 || receipt.WarnCount != 1 || receipt.InfoCount != 1 {
				t.Fatalf("incorrect summary: %+v", receipt)
			}
			if status == "failed" && receipt.Source != "build/deploy" || status == "live" && receipt.Source != "runtime" {
				t.Fatal("source identity was lost")
			}
			if receipt.FailureReason != "build command failed" || receipt.FailureCode != "build_failed" || len(receipt.Patterns) != 1 || receipt.Patterns[0].Count != 2 || len(receipt.Warnings) == 0 || len(receipt.NextCommands) != 2 {
				t.Fatalf("missing failure/coverage detail: %+v", receipt)
			}
			if status == "live" {
				code, text, stderr := runExplanationCLI(t, false, args...)
				if code != 0 || !strings.Contains(text, "levels: error=2 warn=1 info=1") || !strings.Contains(text, "error:  "+receipt.LastError) || !strings.Contains(text, "connection refused (N2)") {
					t.Fatalf("text/JSON summaries differ: %d: %s; %s", code, text, stderr)
				}
			}
		})
	}
}

func TestLogExplanationPartialReads(t *testing.T) {
	for _, test := range []struct {
		name, suffix, reason string
		skipped              int
	}{
		{"eof", "", "eof", 0},
		{"degraded", "event: degraded\ndata: {\"code\":\"unavailable\"}\n\n", "degraded", 0},
		{"error", "event: error\ndata: {\"title\":\"stream rejected\",\"detail\":\"permission denied\"}\n\n", "error", 0},
		{"gap", "event: gap\ndata: {\"reason\":\"seq_below_retained\"}\n\nevent: end\ndata: {}\n\n", "end", 0},
		{"malformed", "event: log\ndata: {bad}\n\nevent: end\ndata: {}\n\n", "end", 1},
		{"missing_line", "event: log\ndata: {\"other\":\"field\"}\n\nevent: end\ndata: {}\n\n", "end", 1},
		{"invalid_end", "event: end\ndata: malformed\n\n", "invalid_end", 0},
		{"null_end", "event: end\ndata: null\n\n", "invalid_end", 0},
		{"server_timeout", "event: end\ndata: {\"reason\":\"timeout\"}\n\n", "incomplete_end", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			explanationServer(t, "live", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, explanationLogFrames+test.suffix) })
			code, output, _ := runExplanationCLI(t, true, "api", "--deployment", explanationDeploymentID, "--explain")
			receipt := readExplanationReceipt(t, output)
			if code == 0 || receipt.Complete || receipt.EndReason != test.reason || receipt.Observed != 4 || receipt.Skipped != test.skipped {
				t.Fatalf("partial read was hidden: exit=%d, %+v", code, receipt)
			}
		})
	}
}

func TestLogExplanationEmptyAndEntryLimits(t *testing.T) {
	for _, count := range []int{0, api.CLILogExplanationEntries, api.CLILogExplanationEntries + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			explanationServer(t, "failed", func(w http.ResponseWriter, _ *http.Request) {
				for range count {
					_, _ = fmt.Fprint(w, "event: log\ndata: {\"line\":\"repeated error\",\"level\":\"error\"}\n\n")
				}
				_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
			})
			code, output, _ := runExplanationCLI(t, true, "api", "--deployment", explanationDeploymentID, "--explain")
			receipt := readExplanationReceipt(t, output)
			limited := count > api.CLILogExplanationEntries
			if receipt.Observed != min(count, api.CLILogExplanationEntries) || receipt.ErrorCount != receipt.Observed || receipt.Complete == limited || (code != 0) != limited {
				t.Fatalf("incorrect entry bound: %d, %+v", code, receipt)
			}
			if limited && receipt.EndReason != "entry_limit" {
				t.Fatal("entry cutoff is not explicit")
			}
			if count == 0 && receipt.Patterns == nil {
				t.Fatal("empty pattern list must be []")
			}
		})
	}
}

func TestLogExplanationRedactsBeforeTruncation(t *testing.T) {
	secret := "sk_live_" + strings.Repeat("a", 32)
	line := strings.Repeat("界", api.CLILogExplanationMessageBytes/3-1) + secret + " Bearer fp_live_fixture"
	explanationServer(t, "live", func(w http.ResponseWriter, _ *http.Request) {
		data, _ := json.Marshal(map[string]any{"line": line, "level": "error"})
		_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\nevent: end\ndata: {}\n\n", data)
	})
	code, output, _ := runExplanationCLI(t, true, "api", "--deployment", explanationDeploymentID, "--explain")
	receipt := readExplanationReceipt(t, output)
	if code != 0 || !receipt.Shortened || len(receipt.LastError) > api.CLILogExplanationMessageBytes || !utf8.ValidString(receipt.LastError) || strings.Contains(output, secret) || strings.Contains(output, "sk_l") || strings.Contains(output, "fp_live_fixture") {
		t.Fatal("summary truncation leaked or split a credential/UTF-8 message")
	}
}

func TestLogExplanationRejectsUnsupportedFlagsBeforeRequests(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_fixture")
	t.Setenv("FAAS_JSON", "0")
	for _, extra := range [][]string{{"--follow"}, {"--grep", "error"}, {"--since", "1h"}, {"--level", "error"}, {"--source", "http"}, {"--archive", "--instance", "one", "--date", "today"}} {
		args := append([]string{"api", "--deployment", explanationDeploymentID, "--explain"}, extra...)
		if code, _, _ := runExplanationCLI(t, true, args...); code == 0 {
			t.Fatalf("unsupported combination accepted: %v", extra)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid options reached the API")
	}
}

func TestLogExplanationRejectsWrongAppBeforeStreaming(t *testing.T) {
	var streams atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/" + explanationDeploymentID:
			main.WriteJSONTest(w, api.DeploymentResponse{ID: explanationDeploymentID, AppID: "other", Status: "failed"})
		case "/v1/apps/api":
			main.WriteJSONTest(w, api.AppResponse{ID: "app-demo", Slug: "api"})
		default:
			streams.Add(1)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_fixture")
	t.Setenv("FAAS_JSON", "0")
	if code, _, _ := runExplanationCLI(t, true, "api", "--deployment", explanationDeploymentID, "--explain"); code == 0 || streams.Load() != 0 {
		t.Fatal("mismatched deployment was streamed")
	}
}

func TestLogExplanationCancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), time.Second)
			}
			defer cancel()
			srv := explanationServer(t, "live", func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprint(w, explanationLogFrames)
				w.(http.Flusher).Flush()
				if !deadline {
					cancel()
				}
				<-r.Context().Done()
			})
			main.ResetJSONOut(t)
			*main.JsonOutput = true
			var output bytes.Buffer
			old := *main.OsStdout
			*main.OsStdout = &output
			defer func() { *main.OsStdout = old }()
			code := main.RunDeploymentLogExplanationForTest(ctx, main.NewClient(srv.URL, "fp_live_fixture"), "api", explanationDeploymentID)
			receipt := readExplanationReceipt(t, output.String())
			want, reason := 130, "cancelled"
			if deadline {
				want, reason = 3, "deadline"
			}
			if code != want || receipt.Complete || receipt.EndReason != reason {
				t.Fatalf("interruption was hidden: %d, %+v", code, receipt)
			}
		})
	}
}
