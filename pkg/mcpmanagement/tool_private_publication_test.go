package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func privateUploadedToolArgs(t *testing.T, runtime string) planUploadedToolArgs {
	t.Helper()
	a := uploadedRuntimeToolArgs(t, runtime)
	a.AccessMode = "external-oauth"
	for i := range a.Files {
		if a.Files[i].Path != "gregale-mcp.json" {
			continue
		}
		var config mcphosting.Config
		if err := json.Unmarshal([]byte(a.Files[i].Content), &config); err != nil {
			t.Fatal(err)
		}
		config.Auth = mcphosting.AuthConfig{Mode: "external-oauth", Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks", Resource: "https://alpha.gregale.dev/mcp", Scopes: []string{"tools:read"}, ToolScopes: map[string][]string{"add": {"tools:read"}}}
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		a.Files[i].Content = string(data)
	}
	return a
}

func privatePublicationCLI(t *testing.T, f *publicationFixture) LocalCLI {
	t.Helper()
	return func(ctx context.Context, args []string, in []byte) (LocalCLIResult, error) {
		if !slices.Contains(args, "init") {
			i := slices.Index(args, "--publication-id")
			if i < 0 || i+1 >= len(args) || !toolpublication.ValidID(args[i+1]) || slices.Contains(args, "--token-env") || slices.Contains(args, "--secrets-file") {
				return LocalCLIResult{}, errors.New("private publication used local credentials or omitted native identity")
			}
		}
		out, err := f.cli(ctx, args, in)
		if err != nil || slices.Contains(args, "init") {
			return out, err
		}
		var receipt map[string]any
		if err = json.Unmarshal(out.Stdout, &receipt); err != nil {
			return out, err
		}
		receipt["auth_mode"] = "external-oauth"
		out.Stdout, err = json.Marshal(receipt)
		return out, err
	}
}

func TestHostedPrivateToolPublicationJourney(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, runtime := range []string{"node", "go", "python"} {
			for _, archive := range []bool{false, true} {
				t.Run(protocol+"/"+runtime+map[bool]string{false: "/inline", true: "/archive"}[archive], func(t *testing.T) { hostedToolPublicationJourney(t, protocol, runtime, archive, true) })
			}
		}
	}
}
