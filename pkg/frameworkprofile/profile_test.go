package frameworkprofile

import (
	"testing"

	"testing/fstest"
)

func TestAnalyzeProfilesCommonAPIs(t *testing.T) {
	tests := []struct {
		name      string
		files     fstest.MapFS
		framework string
		command   string
		port      int
	}{
		{
			name: "express",
			files: fstest.MapFS{
				"package.json": &fstest.MapFile{Data: []byte(`{"dependencies":{"express":"^5"},"scripts":{"start":"node server.js"}}`)},
				"server.js":    &fstest.MapFile{Data: []byte("app.listen(process.env.PORT);\n")},
			},
			framework: "express", command: "npm run start", port: 3000,
		},
		{
			name: "fastapi",
			files: fstest.MapFS{
				"requirements.txt": &fstest.MapFile{Data: []byte("fastapi==0.116.0\nuvicorn\n")},
				"app.py":           &fstest.MapFile{Data: []byte("from fastapi import FastAPI\napp=FastAPI()\n")},
			},
			framework: "fastapi", command: "uvicorn app:app --host 0.0.0.0 --port $PORT", port: 8000,
		},
		{
			name: "gin",
			files: fstest.MapFS{
				"go.mod":  &fstest.MapFile{Data: []byte("module example.com/api\ngo 1.24\nrequire github.com/gin-gonic/gin v1.10.0\n")},
				"main.go": &fstest.MapFile{Data: []byte(`package main\nimport _ "github.com/gin-gonic/gin"\n`)},
			},
			framework: "gin", command: "go run .", port: 8080,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Analyze(tt.files)
			if err != nil {
				t.Fatal(err)
			}
			if got.Framework != tt.framework || got.StartCommand != tt.command || got.Port != tt.port {
				t.Fatalf("profile = %+v, want framework=%q command=%q port=%d", got, tt.framework, tt.command, tt.port)
			}
			if !got.Inferred {
				t.Fatal("profile should be marked inferred")
			}
		})
	}
}

func TestAnalyzeDockerfileUsesExposedPortAndDefersCommand(t *testing.T) {
	got, err := Analyze(fstest.MapFS{
		"Dockerfile": &fstest.MapFile{Data: []byte("FROM node:22\nEXPOSE 9000\nCMD [\"node\", \"server.js\"]\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Framework != "oci" || got.Port != 9000 || got.StartCommand != "" {
		t.Fatalf("profile = %+v, want oci/9000/no command", got)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Code != "container_command_deferred" {
		t.Fatalf("warnings = %+v, want container_command_deferred", got.Warnings)
	}
}

func TestAnalyzeUnknownAndLoopbackWarning(t *testing.T) {
	got, err := Analyze(fstest.MapFS{
		"server.js": &fstest.MapFile{Data: []byte("app.listen(3000, '127.0.0.1')\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Framework != "unknown" || got.Inferred {
		t.Fatalf("profile = %+v, want unknown and not inferred", got)
	}
	seen := map[string]bool{}
	for _, warning := range got.Warnings {
		seen[warning.Code] = true
	}
	if !seen["framework_not_detected"] || !seen["loopback_bind_possible"] {
		t.Fatalf("warnings = %+v, want framework_not_detected and loopback_bind_possible", got.Warnings)
	}
}

func TestAnalyzeNodeUsesDeclaredPackageManagerAndHealthRoute(t *testing.T) {
	got, err := Analyze(fstest.MapFS{
		"package.json":   &fstest.MapFile{Data: []byte(`{"packageManager":"pnpm@9.12.0","dependencies":{"express":"^5"},"scripts":{"start":"node src/server.js"}}`)},
		"pnpm-lock.yaml": &fstest.MapFile{Data: []byte("lockfileVersion: 9\n")},
		"src/server.js":  &fstest.MapFile{Data: []byte(`app.get("/readyz", (_req, res) => res.send("ok")); app.listen(process.env.PORT);`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.PackageManager != "pnpm" || got.StartCommand != "pnpm run start" {
		t.Fatalf("profile = %+v, want pnpm start command", got)
	}
	if got.HealthPath != "/readyz" {
		t.Fatalf("health path = %q, want /readyz", got.HealthPath)
	}
}

func TestAnalyzePythonUsesNestedApplicationEntrypoint(t *testing.T) {
	got, err := Analyze(fstest.MapFS{
		"pyproject.toml": &fstest.MapFile{Data: []byte("[project]\ndependencies = ['fastapi', 'uvicorn']\n")},
		"uv.lock":        &fstest.MapFile{Data: []byte("version = 1\n")},
		"src/api.py":     &fstest.MapFile{Data: []byte("from fastapi import FastAPI\napi = FastAPI()\n@api.get('/health')\ndef health(): return {'ok': True}\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.PackageManager != "uv" || got.StartCommand != "uvicorn src.api:api --host 0.0.0.0 --port $PORT" {
		t.Fatalf("profile = %+v, want uv + nested FastAPI entrypoint", got)
	}
	if got.HealthPath != "/health" {
		t.Fatalf("health path = %q, want /health", got.HealthPath)
	}
}

func TestAnalyzeWarnsForDevelopmentNodeCommand(t *testing.T) {
	got, err := Analyze(fstest.MapFS{
		"package.json": &fstest.MapFile{Data: []byte(`{"dependencies":{"express":"^5"},"scripts":{"start":"next dev"}}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, warning := range got.Warnings {
		if warning.Code == "development_start_command" {
			return
		}
	}
	t.Fatalf("warnings = %+v, want development_start_command", got.Warnings)
}
