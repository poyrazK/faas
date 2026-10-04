package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExportAppEnvRequiresAcknowledgement(t *testing.T) {
	for _, body := range []string{`{}`, `{"acknowledge_sensitive_values":false}`} {
		var request ExportAppEnvRequest
		if err := json.Unmarshal([]byte(body), &request); err != nil {
			t.Fatal(err)
		}
		if request.Validate() == nil {
			t.Fatalf("accepted unacknowledged export %s", body)
		}
	}
	if problem := (ExportAppEnvRequest{AcknowledgeSensitiveValues: true}).Validate(); problem != nil {
		t.Fatal(problem)
	}
}

func TestAppEnvExportPreservesValues(t *testing.T) {
	original := AppEnvExportResponse{AppSlug: "app", Scope: "staging", Values: map[string]string{"EMPTY": "", "MULTILINE": "first\nsecond", "QUOTES": "\"quoted\"\\path"}}
	body, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AppEnvExportResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	for key, value := range original.Values {
		if decoded.Values[key] != value {
			t.Fatalf("changed value for %s", key)
		}
	}
}

func TestExportAppEnvClientRequiresAcknowledgementAndCarriesScope(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/apps/api/env-export" || r.URL.Query().Get("scope") != "staging" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		var body ExportAppEnvRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.AcknowledgeSensitiveValues {
			t.Errorf("missing explicit acknowledgement: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(AppEnvExportResponse{AppSlug: "api", Scope: "staging", Values: map[string]string{"TOKEN": "private"}})
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	if _, err := client.ExportAppEnv(context.Background(), "api", "staging", ExportAppEnvRequest{}); err == nil || calls != 0 {
		t.Fatal("unacknowledged request reached server")
	}
	result, err := client.ExportAppEnv(context.Background(), "api", "staging", ExportAppEnvRequest{AcknowledgeSensitiveValues: true})
	if err != nil || calls != 1 || result.Values["TOKEN"] != "private" {
		t.Fatalf("export response mismatch: %+v %v", result, err)
	}
}
