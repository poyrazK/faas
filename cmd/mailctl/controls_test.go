package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func controlAuditFlags() []string {
	return []string{"--request-id", uuid.NewString(), "--actor", "operator.test", "--reason", "maintenance", "--evidence", "ticket:123"}
}

func controlCLIEnv(t *testing.T, handler http.Handler) func(string) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "maild.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return func(key string) string {
		switch key {
		case internalmail.EnableEnv, internalmail.OperationsEnableEnv, internalmail.ControlsEnableEnv:
			return "1"
		case "FAAS_INTERNAL_MAIL_SOCKET":
			return socket
		case "FAAS_INTERNAL_MAIL_OPERATIONS_TOKEN":
			return "private-operator-token-at-least-32-bytes"
		}
		t.Error("control CLI accessed unrelated configuration", key)
		return ""
	}
}

func TestCLIControlsDefaultOffBeforeCredentialOrSocket(t *testing.T) {
	for _, value := range []string{"", "0", "true", " 1", "1 "} {
		var out, diag bytes.Buffer
		code := run(t.Context(), func(key string) string {
			switch key {
			case internalmail.EnableEnv, internalmail.OperationsEnableEnv:
				return "1"
			case internalmail.ControlsEnableEnv:
				return value
			}
			t.Fatal("disabled controls read credentials or socket", key)
			return ""
		}, []string{"delivery", "status", "--namespace"}, &out, &diag)
		if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "disabled") {
			t.Fatal(code, diag.String())
		}
	}
}

func TestCLIControlRequiresExplicitScopeRevisionAndAudit(t *testing.T) {
	id := uuid.NewString()
	for _, args := range [][]string{
		{"delivery", "status"}, {"delivery", "status", "--namespace", "--domain-id", id},
		{"delivery", "status", "--domain-id", "bad"},
		append([]string{"delivery", "pause", "--namespace"}, controlAuditFlags()...),
		append([]string{"delivery", "resume", "--expected-revision", "0"}, controlAuditFlags()...),
		{"delivery", "pause", "--namespace", "--expected-revision", "0"},
		append([]string{"delivery", "drain", "--namespace", "--expected-revision", "0", "--timeout", "6m"}, controlAuditFlags()...),
		append([]string{"delivery", "drain", "--namespace", "--expected-revision", "0", "--timeout", "0"}, controlAuditFlags()...),
		append([]string{"messages", "cancel", "--id", "bad"}, controlAuditFlags()...),
		{"recipients", "cancel", "--id", id},
	} {
		var out, diag bytes.Buffer
		code := run(t.Context(), func(key string) string {
			if key == internalmail.EnableEnv || key == internalmail.OperationsEnableEnv {
				return "1"
			}
			t.Fatal("invalid controls read private configuration", key)
			return ""
		}, args, &out, &diag)
		if code != 2 || out.Len() != 0 {
			t.Fatal(args, code, diag.String())
		}
	}
}

func TestCLIControlScopeCASAndPartialCancellation(t *testing.T) {
	domainID, recipientID := uuid.NewString(), uuid.NewString()
	var requests atomic.Int32
	getenv := controlCLIEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer private-operator-token-at-least-32-bytes" {
			t.Error("incorrect operator credential")
		}
		switch r.URL.Path {
		case internalmail.DeliveryControlsPath:
			if r.Method != http.MethodGet || r.URL.Query().Get("domain_id") != domainID {
				t.Error("incorrect status scope")
			}
			_, _ = io.WriteString(w, `{"mode":"running","revision":0}`)
		case internalmail.DeliveryControlsPath + "/pause":
			var input map[string]any
			if json.NewDecoder(r.Body).Decode(&input) != nil || input["scope"] != "domain" || input["domain_id"] != domainID || input["expected_revision"] != float64(0) {
				t.Error("control lost scope or CAS revision")
			}
			for _, key := range []string{"request_id", "actor", "reason", "evidence"} {
				if input[key] == nil || input[key] == "" {
					t.Error("control lost audit", key)
				}
			}
			if _, present := input["wait"]; present {
				t.Error("client wait leaked into mutation request")
			}
			_, _ = io.WriteString(w, `{"result":{"mode":"paused","revision":1}}`)
		case internalmail.OperationsPath + "/recipients/" + recipientID + "/cancel":
			_, _ = io.WriteString(w, `{"result":{"complete":false,"protected":[{"status":"submitted"}]}}`)
		default:
			t.Error("unexpected private route", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	commands := []struct {
		args []string
		code int
	}{
		{[]string{"delivery", "status", "--domain-id", domainID}, 0},
		{append([]string{"delivery", "pause", "--domain-id", domainID, "--expected-revision", "0"}, controlAuditFlags()...), 0},
		{append([]string{"recipients", "cancel", "--id", recipientID}, controlAuditFlags()...), 1},
	}
	for _, cmd := range commands {
		var out, diag bytes.Buffer
		if code := run(t.Context(), getenv, cmd.args, &out, &diag); code != cmd.code || !json.Valid(out.Bytes()) {
			t.Fatal(cmd.args, code, diag.String())
		}
	}
	if requests.Load() != 3 {
		t.Fatal("control mutation was automatically retried")
	}
}

func TestCLIDrainWaitCompletesOrPreservesHold(t *testing.T) {
	for _, outcome := range []string{"complete", "uncertain", "changed", "timeout", "cancelled", "unavailable"} {
		t.Run(outcome, func(t *testing.T) {
			var posts, polls atomic.Int32
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			initial := internalmail.DeliveryControlStatus{DeliveryScope: internalmail.DeliveryScope{Kind: "namespace"}, Mode: "draining", Revision: 1, Held: true, ActiveClaims: 1, NamespaceMode: "draining"}
			getenv := controlCLIEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					if r.URL.Path != internalmail.DeliveryControlsPath+"/drain" {
						t.Error("drain unexpectedly resumed or changed a scope")
					}
					raw, _ := json.Marshal(initial)
					_ = json.NewEncoder(w).Encode(internalmail.OperationRecord{ID: uuid.NewString(), Result: raw})
					return
				}
				n := polls.Add(1)
				status := initial
				switch outcome {
				case "complete":
					if n > 1 {
						status.ActiveClaims, status.Drained = 0, true
					}
				case "uncertain":
					status.ActiveClaims, status.Uncertain = 0, 1
				case "changed":
					status.Mode, status.Revision, status.Held = "running", 2, false
				case "cancelled":
					cancel()
				case "unavailable":
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_ = json.NewEncoder(w).Encode(status)
			}))
			timeout := "3s"
			if outcome == "timeout" {
				timeout = "30ms"
			}
			args := append([]string{"delivery", "drain", "--namespace", "--expected-revision", "0", "--timeout", timeout}, controlAuditFlags()...)
			var out, diag bytes.Buffer
			code := run(ctx, getenv, args, &out, &diag)
			var result drainOutput
			if json.Unmarshal(out.Bytes(), &result) != nil || posts.Load() != 1 || polls.Load() == 0 {
				t.Fatal("drain lost committed operation or changed hold", code, diag.String())
			}
			if outcome == "complete" {
				if code != 0 || !result.Complete || !result.Status.Drained || polls.Load() < 2 {
					t.Fatal("drain did not await active work", code, diag.String())
				}
			} else if code != 1 || result.Complete || diag.Len() == 0 {
				t.Fatal("incomplete drain reported success", code, diag.String())
			}
			if outcome != "changed" && !result.Status.Held {
				t.Fatal("incomplete drain dropped its saved hold")
			}
		})
	}
}
