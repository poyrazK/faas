package api

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestMultipartDeployPreservesExplicitZeroAndCanary(t *testing.T) {
	zero := 0
	for _, tc := range []struct {
		name      string
		ann       DeployAnnotations
		wantField string
		wantValue string
	}{
		{name: "explicit zero", ann: DeployAnnotations{TrafficPercent: &zero}, wantField: "traffic_percent", wantValue: "0"},
		{name: "canary", ann: DeployAnnotations{Canary: &CanaryPresetSpec{Preset: "balanced"}}, wantField: "canary", wantValue: `{"preset":"balanced"}`},
		{name: "rollback enabled", ann: DeployAnnotations{RollbackOn5xx: boolPtr(true)}, wantField: "rollback_on_5xx", wantValue: "true"},
		{name: "rollback explicitly disabled", ann: DeployAnnotations{RollbackOn5xx: boolPtr(false)}, wantField: "rollback_on_5xx", wantValue: "false"},
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
