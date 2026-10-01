package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppsUDPCommands(t *testing.T) {
	var requests int
	var method, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer fp_test" {
			t.Error("missing bearer credential")
		}
		if r.Method != method || r.URL.Path != path {
			t.Errorf("request %s %s, want %s %s", r.Method, r.URL.Path, method, path)
		}
		if body != "" {
			var got, want map[string]any
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			if err := json.Unmarshal([]byte(body), &want); err != nil {
				t.Fatal(err)
			}
			actual, _ := json.Marshal(got)
			expected, _ := json.Marshal(want)
			if string(actual) != string(expected) {
				t.Errorf("body %s, want %s", actual, expected)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		out := api.UDPListenerResponse{Name: "dns", GuestPort: 5353, PublicPort: 40100, Protocol: "udp", Enabled: false}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]api.UDPListenerResponse{out})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_ = json.NewEncoder(w).Encode(out)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")
	stdout, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()
	cases := []struct {
		args                         []string
		method, suffix, body, output string
	}{
		{[]string{"apps", "udp", "test-app", "add", "--name", "dns", "--guest-port", "5353", "--public-port", "40100"}, "POST", "", `{"name":"dns","guest_port":5353,"public_port":40100}`, "Created disabled UDP listener"},
		{[]string{"apps", "udp", "test-app"}, "GET", "", "", "dns"},
		{[]string{"app", "test-app", "udp", "enable", "dns"}, "PATCH", "/dns", `{"enabled":true}`, "Enabled UDP listener"},
		{[]string{"apps", "udp", "test-app", "disable", "dns"}, "PATCH", "/dns", `{"enabled":false}`, "Disabled UDP listener"},
		{[]string{"apps", "udp", "test-app", "rm", "dns"}, "DELETE", "/dns", "", "Deleted UDP listener"},
	}
	for _, tc := range cases {
		method = tc.method
		path = "/v1/apps/test-app/udp-listeners" + tc.suffix
		body = tc.body
		stdout.Reset()
		if code := run(tc.args); code != 0 {
			t.Fatalf("%v exit=%d", tc.args, code)
		}
		if !strings.Contains(stdout.String(), tc.output) {
			t.Errorf("output %q missing %q", stdout.String(), tc.output)
		}
	}
	if requests != len(cases) {
		t.Fatalf("requests=%d", requests)
	}
	jsonOutput = true
	method = "GET"
	path = "/v1/apps/test-app/udp-listeners"
	body = ""
	stdout.Reset()
	if code := cmdAppsUDP("test-app", nil); code != 0 {
		t.Fatalf("JSON list exit=%d", code)
	}
	var out api.UDPListenerResponse
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Protocol != "udp" || out.Enabled {
		t.Fatalf("JSON listener=%+v", out)
	}
	before := requests
	for _, args := range [][]string{{"apps", "udp"}, {"apps", "udp", "test-app", "add"}, {"apps", "udp", "test-app", "enable"}, {"apps", "udp", "test-app", "list", "extra"}} {
		if code := run(args); code == 0 {
			t.Errorf("%v accepted", args)
		}
	}
	for _, flags := range [][]string{
		{"--name", "dns", "--guest-port", "-1"},
		{"--name", "dns", "--guest-port", "65536"},
		{"--name", "dns", "--guest-port", "5353", "--public-port", "39999"},
		{"--name", "dns", "--guest-port", "5353", "--public-port", "50000"},
	} {
		args := append([]string{"apps", "udp", "test-app", "add"}, flags...)
		if code := run(args); code == 0 {
			t.Errorf("invalid ports accepted: %v", args)
		}
	}
	if requests != before {
		t.Error("invalid input made HTTP request")
	}
}
