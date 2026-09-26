package api

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestMultipartDeployPreservesRolloutAndEnvironment(t *testing.T) {
	zero := 0
	ramMB, cpuMillicores := 256, 500
	maxInstances := 3
	profile := "small"
	cpuTarget := 70.0
	zeroCPU := 0.0
	for _, tc := range []struct {
		name      string
		ann       DeployAnnotations
		wantField string
		wantValue string
	}{
		{name: "explicit zero", ann: DeployAnnotations{TrafficPercent: &zero}, wantField: "traffic_percent", wantValue: "0"},
		{name: "canary", ann: DeployAnnotations{Canary: &CanaryPresetSpec{Preset: "balanced"}}, wantField: "canary", wantValue: `{"preset":"balanced"}`},
		{name: "scope", ann: DeployAnnotations{Scope: "production"}, wantField: "scope", wantValue: "production"},
		{name: "environment", ann: DeployAnnotations{Environment: "staging"}, wantField: "environment", wantValue: "staging"},
		{name: "deployment instance ceiling", ann: DeployAnnotations{MaxInstances: &maxInstances}, wantField: "max_instances", wantValue: "3"},
		{name: "revision resources", ann: DeployAnnotations{Resources: &DeploymentResourcesRequest{RAMMB: &ramMB, CPUMillicores: &cpuMillicores, ResourceProfile: &profile}}, wantField: "resources", wantValue: `{"ram_mb":256,"cpu_millicores":500,"resource_profile":"small"}`},
		{name: "revision CPU target", ann: DeployAnnotations{Scaling: &DeploymentScalingRequest{CPUUtilizationTargetPct: &cpuTarget}}, wantField: "scaling", wantValue: `{"cpu_utilization_target_pct":70}`},
		{name: "revision CPU target disabled", ann: DeployAnnotations{Scaling: &DeploymentScalingRequest{CPUUtilizationTargetPct: &zeroCPU}}, wantField: "scaling", wantValue: `{"cpu_utilization_target_pct":0}`},
		{name: "rollback enabled", ann: DeployAnnotations{RollbackOn5xx: boolPtr(true)}, wantField: "rollback_on_5xx", wantValue: "true"},
		{name: "rollback explicitly disabled", ann: DeployAnnotations{RollbackOn5xx: boolPtr(false)}, wantField: "rollback_on_5xx", wantValue: "false"},
		{name: "startup CPU boost disabled", ann: DeployAnnotations{DisableStartupCPUBoost: boolPtr(true)}, wantField: "disable_startup_cpu_boost", wantValue: "true"},
		{name: "sidecar", ann: DeployAnnotations{Sidecars: Sidecars{{Name: "otel", Image: "registry.example.com/otel@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Type: SidecarTypeSidecar}}}, wantField: "sidecars", wantValue: `[{"name":"otel","image":"registry.example.com/otel@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","type":"sidecar"}]`},
		{name: "companion", ann: DeployAnnotations{Companions: Companions{{Name: "otel", Preset: "opentelemetry", Type: SidecarTypeSidecar}}}, wantField: "companions", wantValue: `[{"name":"otel","preset":"opentelemetry","type":"sidecar"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := newMultipartWriterWithSourceRoot(&body, "app", false, "", "", "", tc.ann)
			contentType := writer.FormDataContentType()
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			_, params, err := mime.ParseMediaType(contentType)
			if err != nil {
				t.Fatal(err)
			}
			reader := multipart.NewReader(&body, params["boundary"])
			fields := map[string]string{}
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				value, _ := io.ReadAll(part)
				fields[part.FormName()] = string(value)
			}
			if got := fields[tc.wantField]; got != tc.wantValue {
				t.Fatalf("%s = %q, want %q", tc.wantField, got, tc.wantValue)
			}
		})
	}
}
