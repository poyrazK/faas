package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplyProjectPlanWithSecretsMultipart(t *testing.T) {
	const value = "fixture-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects" || r.URL.Query().Get("plan_token") != "plan" {
			t.Errorf("unexpected endpoint %s", r.URL)
		}
		if r.Header.Get("Idempotency-Key") != "retry-1" {
			t.Error("retry key lost")
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(part)
			if part.FormName() == "secrets" {
				var bundle map[string]string
				if err := json.Unmarshal(body, &bundle); err != nil {
					t.Fatal(err)
				}
				found = bundle["TOKEN"] == value
			}
			if part.FormName() == "source" && strings.Contains(string(body), value) {
				t.Error("secret value in source")
			}
		}
		if !found {
			t.Error("missing secrets field")
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, "test").ApplyProjectPlanWithSecrets(ContextWithIdempotencyKey(context.Background(), "retry-1"), "plan", strings.NewReader("source"), "source.tgz", "demo", "", "", 0, nil, nil, false, false, "staging", "", map[string]string{"TOKEN": value})
	if err != nil {
		t.Fatal(err)
	}
}
