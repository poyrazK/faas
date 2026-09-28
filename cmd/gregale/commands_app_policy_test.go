package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdAppPolicyControlsPatchAndJSONReceipt(t *testing.T) {
	for _, tc := range []struct {
		name             string
		flags            []string
		wantMaintenance  bool
		wantStreaming    bool
		wantWebsocket    bool
		wantRouteMetrics bool
		wantConsumerAuth string
	}{
		{
			name:            "enable controls",
			flags:           []string{"--maintenance", "--streaming-enabled", "--websocket-enabled", "--route-metrics", "--consumer-auth-mode", api.ConsumerAuthModeRequired},
			wantMaintenance: true, wantStreaming: true, wantWebsocket: true, wantRouteMetrics: true,
			wantConsumerAuth: api.ConsumerAuthModeRequired,
		},
		{
			name:             "disable controls",
			flags:            []string{"--no-maintenance", "--no-streaming-enabled", "--no-websocket", "--no-route-metrics", "--consumer-auth-mode", api.ConsumerAuthModeOptional},
			wantConsumerAuth: api.ConsumerAuthModeOptional,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetJSONOut(t)
			var got api.UpdateAppRequest
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPatch || r.URL.Path != "/v1/apps/hello" {
					t.Errorf("request = %s %s, want PATCH /v1/apps/hello", r.Method, r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode PATCH body: %v", err)
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				writeJSONTest(w, api.AppResponse{
					Slug: constSlug, MaintenanceMode: tc.wantMaintenance,
					StreamingEnabled: tc.wantStreaming, WebSocketEnabled: tc.wantWebsocket,
					RouteMetricsEnabled: tc.wantRouteMetrics, ConsumerAuthMode: tc.wantConsumerAuth,
				})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")

			args := append([]string{"app", constSlug}, tc.flags...)
			args = append(args, "--json")
			var stdout bytes.Buffer
			oldOut := osStdout
			osStdout = &stdout
			t.Cleanup(func() { osStdout = oldOut })
			if code := run(args); code != 0 {
				t.Fatalf("run(%v) = %d, want 0; output: %s", args, code, stdout.String())
			}
			if calls != 1 {
				t.Fatalf("API calls = %d, want one PATCH", calls)
			}
			for name, field := range map[string]*bool{
				"maintenance_mode":      got.MaintenanceMode,
				"streaming_enabled":     got.StreamingEnabled,
				"websocket_enabled":     got.WebSocketEnabled,
				"route_metrics_enabled": got.RouteMetricsEnabled,
			} {
				if field == nil {
					t.Errorf("PATCH omitted %s", name)
				}
			}
			if got.MaintenanceMode == nil || *got.MaintenanceMode != tc.wantMaintenance {
				t.Errorf("maintenance_mode = %v, want %t", got.MaintenanceMode, tc.wantMaintenance)
			}
			if got.StreamingEnabled == nil || *got.StreamingEnabled != tc.wantStreaming {
				t.Errorf("streaming_enabled = %v, want %t", got.StreamingEnabled, tc.wantStreaming)
			}
			if got.WebSocketEnabled == nil || *got.WebSocketEnabled != tc.wantWebsocket {
				t.Errorf("websocket_enabled = %v, want %t", got.WebSocketEnabled, tc.wantWebsocket)
			}
			if got.RouteMetricsEnabled == nil || *got.RouteMetricsEnabled != tc.wantRouteMetrics {
				t.Errorf("route_metrics_enabled = %v, want %t", got.RouteMetricsEnabled, tc.wantRouteMetrics)
			}
			if got.ConsumerAuthMode == nil || *got.ConsumerAuthMode != tc.wantConsumerAuth {
				t.Errorf("consumer_auth_mode = %v, want %q", got.ConsumerAuthMode, tc.wantConsumerAuth)
			}
			if got.Visibility != nil || got.RAMMB != nil || got.RequireAuthn != nil {
				t.Errorf("PATCH unexpectedly changed unrelated fields: %+v", got)
			}

			var receipt api.AppResponse
			if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
				t.Fatalf("decode --json mutation receipt: %v; output: %s", err, stdout.String())
			}
			if receipt.Slug != constSlug || receipt.MaintenanceMode != tc.wantMaintenance ||
				receipt.StreamingEnabled != tc.wantStreaming || receipt.WebSocketEnabled != tc.wantWebsocket ||
				receipt.RouteMetricsEnabled != tc.wantRouteMetrics || receipt.ConsumerAuthMode != tc.wantConsumerAuth {
				t.Fatalf("JSON receipt = %+v, does not reflect updated controls", receipt)
			}
		})
	}
}

func TestCmdAppPolicyControlsPartialPatchAndValidation(t *testing.T) {
	t.Run("omitted policy fields stay omitted", func(t *testing.T) {
		resetJSONOut(t)
		var got api.UpdateAppRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode PATCH body: %v", err)
			}
			writeJSONTest(w, api.AppResponse{Slug: constSlug, MaintenanceMode: true})
		}))
		defer srv.Close()
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_test_x")
		if code := cmdApp([]string{constSlug, "--maintenance"}); code != 0 {
			t.Fatalf("cmdApp --maintenance = %d, want 0", code)
		}
		if got.MaintenanceMode == nil || !*got.MaintenanceMode {
			t.Fatalf("maintenance_mode = %v, want true", got.MaintenanceMode)
		}
		if got.StreamingEnabled != nil || got.WebSocketEnabled != nil || got.RouteMetricsEnabled != nil || got.ConsumerAuthMode != nil {
			t.Fatalf("partial PATCH included omitted policy fields: %+v", got)
		}
	})

	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{name: "consumer auth enum", flags: []string{constSlug, "--consumer-auth-mode", "sometimes"}},
		{name: "maintenance conflict", flags: []string{constSlug, "--maintenance", "--no-maintenance"}},
		{name: "streaming conflict", flags: []string{constSlug, "--streaming-enabled", "--no-streaming-enabled"}},
		{name: "websocket conflict", flags: []string{constSlug, "--websocket-enabled", "--no-websocket"}},
		{name: "route metrics conflict", flags: []string{constSlug, "--route-metrics", "--no-route-metrics"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			if code := cmdApp(tc.flags); code == 0 {
				t.Fatalf("cmdApp(%v) succeeded; want local validation error", tc.flags)
			}
			if calls != 0 {
				t.Fatalf("invalid flags made %d API request(s)", calls)
			}
		})
	}
}

func TestCmdAppPolicyControlsVisibleInPlainAppRead(t *testing.T) {
	resetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONTest(w, api.AppResponse{
			Slug: constSlug, MaintenanceMode: true, StreamingEnabled: true,
			WebSocketEnabled: true, RouteMetricsEnabled: true,
			ConsumerAuthMode: api.ConsumerAuthModeRequired,
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = reader.Close()
		_ = writer.Close()
	})
	if code := cmdApp([]string{constSlug}); code != 0 {
		t.Fatalf("cmdApp read = %d, want 0", code)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	plainOutput := strings.Join(strings.Fields(string(output)), " ")
	for _, want := range []string{
		"maintenance mode: enabled", "streaming: enabled", "websocket: enabled",
		"route metrics: enabled", "consumer auth mode: required",
	} {
		if !strings.Contains(plainOutput, want) {
			t.Errorf("app read output missing %q:\n%s", want, output)
		}
	}
}

func TestUpdateAppRequestFieldsHaveReviewedCLISurfaces(t *testing.T) {
	requestType := reflect.TypeOf(api.UpdateAppRequest{})
	seen := make(map[string]bool, requestType.NumField())
	for i := 0; i < requestType.NumField(); i++ {
		field := requestType.Field(i)
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if jsonName == "-" {
			continue
		}
		if jsonName == "" {
			jsonName = field.Name
		}
		seen[jsonName] = true
		surface, ok := updateAppRequestCLISurfaces[jsonName]
		if !ok {
			t.Errorf("UpdateAppRequest.%s (%s) has no reviewed CLI surface; add a path or an explicit known-gap disposition", field.Name, jsonName)
			continue
		}
		if surface.Path == "" && surface.KnownGap == "" {
			t.Errorf("UpdateAppRequest.%s has neither a CLI path nor an explicit known-gap disposition", field.Name)
		}
		if surface.Path != "" && surface.KnownGap != "" {
			t.Errorf("UpdateAppRequest.%s has both a CLI path and a known-gap disposition", field.Name)
		}
		if len(surface.AppFlags) > 0 {
			command, ok := lookupCliCommand("app")
			if !ok {
				t.Fatal("app command missing from CLI manifest")
			}
			for _, flagName := range surface.AppFlags {
				if !cliCommandHasFlag(command, flagName) {
					t.Errorf("UpdateAppRequest.%s maps to --%s but app CLI metadata does not document that flag", jsonName, flagName)
				}
			}
		}
	}
	for jsonName := range updateAppRequestCLISurfaces {
		if !seen[jsonName] {
			t.Errorf("CLI surface metadata refers to %q, which is not a JSON field on UpdateAppRequest", jsonName)
		}
	}
}

func TestAppPolicyControlsHelpCompletionManAndReference(t *testing.T) {
	wants := []string{
		"--maintenance", "--no-maintenance", "--streaming-enabled", "--no-streaming-enabled",
		"--websocket-enabled", "--no-websocket", "--route-metrics", "--no-route-metrics", "--consumer-auth-mode",
	}
	command, ok := lookupCliCommand("app")
	if !ok {
		t.Fatal("app command missing from CLI manifest")
	}
	var help, man, reference bytes.Buffer
	printLocalCommandHelp(&help, command)
	renderManCommand(&man, command)
	renderMarkdownReference(&reference, []cliCommand{command})
	for surface, render := range map[string]func(io.Writer, cliCommand){
		"bash completion":       renderBashCommand,
		"zsh completion":        renderZshCommand,
		"fish completion":       renderFishCommand,
		"powershell completion": renderPowershellCommand,
	} {
		var out bytes.Buffer
		render(&out, command)
		for _, want := range wants {
			needle := want
			if surface == "fish completion" {
				needle = "-l " + strings.TrimPrefix(want, "--")
			}
			if !strings.Contains(out.String(), needle) {
				t.Errorf("%s missing %s", surface, want)
			}
		}
	}
	for surface, out := range map[string]string{
		"help": help.String(), "man": man.String(), "reference": reference.String(),
	} {
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Errorf("%s missing %s", surface, want)
			}
		}
	}
}

func cliCommandHasFlag(command cliCommand, name string) bool {
	for _, flag := range command.Flags {
		if flag.Name == name {
			return true
		}
	}
	return false
}
