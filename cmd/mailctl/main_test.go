package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestDefaultOffCLIReadsNoCredentialOrSocket(t *testing.T) {
	for _, tc := range []struct{ master, operations string }{{"", "1"}, {"0", "1"}, {"true", "1"}, {"1", ""}, {"1", "0"}, {"1", "true"}} {
		var out, diag bytes.Buffer
		code := run(t.Context(), func(key string) string {
			switch key {
			case internalmail.EnableEnv:
				return tc.master
			case internalmail.OperationsEnableEnv:
				return tc.operations
			}
			t.Fatal("disabled CLI read configuration", key)
			return ""
		}, []string{"uncertain", "resolve"}, &out, &diag)
		if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "disabled") {
			t.Fatal(code, diag.String())
		}
	}
}

func TestCLICommandsUsePrivateOperatorRoutesAndExplicitAudit(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "maild.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var received []string
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-operator-token-at-least-32-bytes" {
			t.Error("incorrect credential")
		}
		mu.Lock()
		received = append(received, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		if r.Method == http.MethodPost {
			var v map[string]any
			if json.NewDecoder(r.Body).Decode(&v) != nil {
				t.Error("missing JSON")
			}
			if strings.HasSuffix(r.URL.Path, "/apply") || strings.HasSuffix(r.URL.Path, "/reconcile") {
				for _, key := range []string{"request_id", "actor", "reason", "evidence"} {
					if v[key] == nil || v[key] == "" {
						t.Error("missing audit field", key)
					}
				}
			}
			if strings.HasSuffix(r.URL.Path, "/preview") && len(v) != 0 {
				t.Error("preview carried mutation options")
			}
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	getenv := func(key string) string {
		switch key {
		case internalmail.EnableEnv, internalmail.OperationsEnableEnv:
			return "1"
		case "FAAS_INTERNAL_MAIL_SOCKET":
			return socket
		case "FAAS_INTERNAL_MAIL_OPERATIONS_TOKEN":
			return "private-operator-token-at-least-32-bytes"
		}
		t.Fatal("CLI read unrelated credential", key)
		return ""
	}
	id := uuid.NewString()
	audit := []string{"--request-id", uuid.NewString(), "--actor", "operator.test", "--reason", "queue_reviewed", "--evidence", "ticket:123"}
	for _, args := range [][]string{
		{"messages", "list", "--status", "uncertain", "--limit", "1"},
		{"messages", "show", "--id", id},
		{"messages", "events", "--id", id, "--after", "operator:abc"},
		{"domains", "list"}, {"domains", "show", "--id", id}, {"domains", "check", "--id", id},
		{"suppressions", "list", "--after", "a@example.test"}, {"audit", "list"}, {"retention", "preview"},
		append([]string{"retention", "apply"}, audit...),
		append([]string{"uncertain", "resolve", "--id", id, "--outcome", "retry", "--confirmed-not-accepted"}, audit...),
		append([]string{"uncertain", "resolve", "--id", id, "--outcome", "submitted", "--confirmed-relay-accepted"}, audit...),
	} {
		var out, diag bytes.Buffer
		if code := run(t.Context(), getenv, args, &out, &diag); code != 0 || !json.Valid(out.Bytes()) {
			t.Fatal(args, code, diag.String())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 12 || !strings.Contains(received[0], "status=uncertain") || !strings.HasSuffix(received[8], "/retention/preview") {
		t.Fatal("incorrect command dispatch", received)
	}
	for _, path := range received {
		if !strings.Contains(path, internalmail.OperationsPath+"/") {
			t.Fatal("operator CLI crossed service boundary", path)
		}
	}
}

func TestCLIRejectsIncompleteOrAmbiguousMutationsBeforeSecretRead(t *testing.T) {
	id := uuid.NewString()
	audit := []string{"--request-id", uuid.NewString(), "--actor", "operator.test", "--reason", "reviewed", "--evidence", "ticket:123"}
	for _, args := range [][]string{
		{"messages", "list", "--limit", "101"}, {"messages", "list", "extra"}, {"messages", "show", "--id", "bad"},
		{"retention", "apply"}, {"retention", "preview", "--apply"}, {"suppressions", "remove"},
		{"uncertain", "resolve", "--id", id, "--outcome", "retry", "--confirmed-not-accepted"},
		append([]string{"uncertain", "resolve", "--id", id, "--outcome", "retry"}, audit...),
		append([]string{"uncertain", "resolve", "--id", id, "--outcome", "retry", "--confirmed-relay-accepted"}, audit...),
		append([]string{"uncertain", "resolve", "--id", id, "--outcome", "retry", "--confirmed-relay-accepted", "--confirmed-not-accepted"}, audit...),
	} {
		var out, diag bytes.Buffer
		code := run(t.Context(), func(key string) string {
			if key == internalmail.EnableEnv || key == internalmail.OperationsEnableEnv {
				return "1"
			}
			t.Fatal("invalid request read private configuration", key)
			return ""
		}, args, &out, &diag)
		if code != 2 || out.Len() != 0 {
			t.Fatal(args, code, diag.String())
		}
	}
}
