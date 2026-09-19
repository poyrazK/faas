package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestProjectEnvironmentConfigResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newProjectEnvironmentConfigResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("project environment config resource schema diagnostics: %v", diags)
	}
}

func TestCanonicalProjectEnvironmentConfig(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "canonicalizes object", input: ` { "replicas": 2, "region": "eu" } `, want: `{"region":"eu","replicas":2}`},
		{name: "empty input", input: "", want: `{}`},
		{name: "rejects non-object", input: `[]`, wantErr: true},
		{name: "rejects multiple values", input: `{} {}`, wantErr: true},
		{name: "rejects secret key", input: `{"api_token":"do-not-store"}`, wantErr: true},
		{name: "rejects invalid key", input: `{"bad/key":true}`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := canonicalProjectEnvironmentConfig(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatal("canonicalProjectEnvironmentConfig succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("canonicalProjectEnvironmentConfig: %v", err)
			}
			if string(got) != test.want {
				t.Fatalf("canonicalProjectEnvironmentConfig = %s, want %s", got, test.want)
			}
		})
	}
}

func TestProjectEnvironmentConfigModelCanonicalizesResponse(t *testing.T) {
	fallback := projectEnvironmentConfigModel{
		ProjectSlug: types.StringValue("orders"),
		Environment: types.StringValue("production"),
	}
	model, diags := projectEnvironmentConfigModelFromResponse(projectEnvironmentConfigResponse{
		ProjectSlug: "orders",
		Environment: "production",
		Version:     4,
		ConfigHash:  "hash-4",
		Values:      []byte(` {"replicas":2,"region":"eu"} `),
		UpdatedAt:   "2026-09-19T10:04:00Z",
	}, fallback)
	if diags.HasError() {
		t.Fatalf("project environment config state diagnostics: %v", diags)
	}
	if model.ID.ValueString() != "orders/production" || model.Version.ValueInt64() != 4 || model.Values.ValueString() != `{"region":"eu","replicas":2}` {
		t.Fatalf("project environment config model = %+v", model)
	}
	if !reflect.DeepEqual(model.ProjectSlug, fallback.ProjectSlug) || !reflect.DeepEqual(model.Environment, fallback.Environment) {
		t.Fatalf("project environment identity changed: %+v", model)
	}
}
