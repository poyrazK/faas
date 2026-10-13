package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestCompletionReturnsBoundedPromptAndResourceSuggestions(t *testing.T) {
	for _, test := range []struct {
		name   string
		params string
		want   []string
	}{
		{
			name:   "prompt styles are case insensitive",
			params: `{"ref":{"type":"ref/prompt","name":"summarize"},"argument":{"name":"style","value":"EXEC"}}`,
			want:   []string{"executive"},
		},
		{
			name:   "resource IDs are bounded",
			params: `{"ref":{"type":"ref/resource","uri":"customer://records/{recordId}"},"argument":{"name":"recordId","value":"EXAMPLE-"}}`,
			want:   []string{"example-1", "example-2", "example-3"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			complete(response, json.RawMessage("1"), json.RawMessage(test.params), testScopedAuthenticator(t, `{"mode":"open"}`), nil)
			if response.Code != 200 {
				t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
			}
			var decoded struct {
				Result struct {
					Completion struct {
						Values  []string `json:"values"`
						Total   int      `json:"total"`
						HasMore bool     `json:"hasMore"`
					} `json:"completion"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("decode completion response: %v", err)
			}
			if len(decoded.Result.Completion.Values) != len(test.want) {
				t.Fatalf("values = %#v, want %#v", decoded.Result.Completion.Values, test.want)
			}
			for index, value := range test.want {
				if decoded.Result.Completion.Values[index] != value {
					t.Fatalf("values = %#v, want %#v", decoded.Result.Completion.Values, test.want)
				}
			}
			if decoded.Result.Completion.Total != len(test.want) || decoded.Result.Completion.HasMore {
				t.Fatalf("completion metadata = %#v, want total %d and hasMore false", decoded.Result.Completion, len(test.want))
			}
		})
	}
}

func TestCompletionRejectsUnknownReferencesAndArguments(t *testing.T) {
	response := httptest.NewRecorder()
	complete(response, json.RawMessage("1"), json.RawMessage(`{"ref":{"type":"ref/prompt","name":"private"},"argument":{"name":"style","value":"b"}}`), testScopedAuthenticator(t, `{"mode":"open"}`), nil)
	if response.Code != 400 {
		t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
	}
}
