package reposcan

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func composeWorkloads(t *testing.T, result Result) map[string]Workload {
	t.Helper()
	out := make(map[string]Workload, len(result.Workloads))
	for _, workload := range result.Workloads {
		out[workload.Name] = workload
	}
	return out
}

func TestComposeExtendsInheritsFromTheSameFile(t *testing.T) {
	body := `services:
  base:
    image: nginx:1.27
    user: nginx
    working_dir: /srv
    environment: [SHARED=1, BASE_ONLY]
    ports: [80]
  web:
    extends: base
    environment: {WEB_ONLY: x}
    ports: [8080]
  admin:
    extends: {service: web}
    working_dir: /admin
`
	result, err := scanCompose(t, body)
	if err != nil {
		t.Fatal(err)
	}
	workloads := composeWorkloads(t, result)
	web, admin := workloads["web"], workloads["admin"]
	if web.Image != "docker.io/library/nginx:1.27" || !reflect.DeepEqual(web.EnvKeys, []string{"BASE_ONLY", "SHARED", "WEB_ONLY"}) {
		t.Fatalf("web = image %q env %q", web.Image, web.EnvKeys)
	}
	if web.ImageProcess == nil || web.ImageProcess.User != "nginx" || web.ImageProcess.WorkingDir != "/srv" {
		t.Fatalf("web process = %+v", web.ImageProcess)
	}
	if admin.ImageProcess == nil || admin.ImageProcess.User != "nginx" || admin.ImageProcess.WorkingDir != "/admin" {
		t.Fatalf("chained extends: admin process = %+v", admin.ImageProcess)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "extends") {
			t.Fatalf("unexpected warning %q", warning)
		}
	}
}

func TestComposeExtendsFromAnotherFileRebasesItsPaths(t *testing.T) {
	body := `services:
  api:
    extends: {file: common/services.yaml, service: node}
    build:
      target: runtime
    ports: [3000]
`
	result, err := scanCompose(t, body,
		"common/services.yaml", "services:\n  node:\n    build:\n      context: ../app\n      args: {NODE_ENV: production}\n    env_file: node.env\n",
		"common/node.env", "TOKEN=x\n",
		"app/Dockerfile", "FROM node:22 AS build\nFROM node:22-slim AS runtime\n")
	if err != nil {
		t.Fatal(err)
	}
	service := composeWorkloads(t, result)["api"]
	want := &api.DockerfileBuildOptions{Target: "runtime", Args: map[string]string{"NODE_ENV": "production"}}
	if service.RootDir != "app" || !reflect.DeepEqual(service.DockerfileBuild, want) {
		t.Fatalf("api root %q build %+v", service.RootDir, service.DockerfileBuild)
	}
	if !slices.Contains(service.EnvKeys, "TOKEN") {
		t.Fatalf("env_file from the base file was not read relative to it: %q (warnings %q)", service.EnvKeys, result.Warnings)
	}
}

func TestComposeIncludeAddsServicesWithTheirOwnPaths(t *testing.T) {
	body := `include:
  - services/api/compose.yaml
  - path: [workers/compose.yaml, workers/compose.prod.yaml]
services:
  web:
    image: nginx:1.27
    ports: [80]
    depends_on: [api]
`
	result, err := scanCompose(t, body,
		"services/api/compose.yaml", "services:\n  api:\n    build: .\n    ports: [3000]\n    env_file: [api.env]\n",
		"services/api/Dockerfile", "FROM node:22\n",
		"services/api/api.env", "DATABASE_URL=x\n",
		"workers/compose.yaml", "services:\n  worker:\n    image: busybox:1.36\n    command: [sleep, '1']\n",
		"workers/compose.prod.yaml", "services:\n  worker:\n    command: [sleep, '2']\n")
	if err != nil {
		t.Fatal(err)
	}
	workloads := composeWorkloads(t, result)
	api, worker := workloads["api"], workloads["worker"]
	if api.RootDir != "services/api" || !slices.Contains(api.EnvKeys, "DATABASE_URL") {
		t.Fatalf("included api: root %q env %q", api.RootDir, api.EnvKeys)
	}
	if !reflect.DeepEqual(worker.Command, []string{"sleep", "2"}) {
		t.Fatalf("include path list did not merge in order: %q", worker.Command)
	}
	if !slices.Contains(workloads["web"].DependsOn, "api") {
		t.Fatalf("web depends_on = %q", workloads["web"].DependsOn)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "include") {
			t.Fatalf("unexpected warning %q", warning)
		}
	}
}

func TestComposeIncludeWarnsWhatItCannotApply(t *testing.T) {
	body := `include:
  - https://example.com/compose.yaml
  - path: db/compose.yaml
    project_directory: db
services:
  web:
    image: nginx:1.27
`
	result, err := scanCompose(t, body,
		"db/compose.yaml", "services:\n  cache:\n    image: redis:7\n",
		"db/.env", "PASSWORD=x\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"WARNING include https://example.com/compose.yaml not loaded: remote Compose files are not supported",
		"include db/compose.yaml: project_directory not applied; paths and the default .env resolve from the included file's directory",
	} {
		if !containsWarning(result.Warnings, want) {
			t.Errorf("warnings %q lack %q", result.Warnings, want)
		}
	}
	if containsWarning(result.Warnings, "is not read") || containsWarning(result.Warnings, "env_file not applied") {
		t.Errorf("warnings %q still report the included .env as unread", result.Warnings)
	}
}

// Each included project interpolates with its own values, as Compose does
// (ADR-20261025090000): the including project's, overlaid by the include's
// env_file list, or else by the .env beside the included file.
func TestComposeIncludeEnvFiles(t *testing.T) {
	body := `include:
  - db/compose.yaml
  - path: api/compose.yaml
    env_file: [api/base.env, api/local.env]
services:
  web:
    image: nginx:1.27
    command: ["serve", "${MODE}", "${SHARED}"]
`
	result, err := scanCompose(t, body,
		".env", "MODE=root\nSHARED=from-root\n",
		"db/compose.yaml", "include: [cache/compose.yaml]\nservices:\n  db-admin:\n    image: nginx:1.27\n    command: [\"serve\", \"${MODE}\", \"${SHARED}\"]\n",
		"db/.env", "MODE=db\n",
		"db/cache/compose.yaml", "services:\n  cache-admin:\n    image: nginx:1.27\n    command: [\"serve\", \"${MODE}\", \"${SHARED}\"]\n",
		"db/cache/.env", "SHARED=from-cache\n",
		"api/compose.yaml", "services:\n  api:\n    image: nginx:1.27\n    command: [\"serve\", \"${MODE}\", \"${SHARED}\"]\n",
		"api/.env", "MODE=ignored-when-env_file-is-set\n",
		"api/base.env", "MODE=api-base\nSHARED=from-api\n",
		"api/local.env", "MODE=api-local\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		service string
		want    []string
	}{
		{"web", []string{"serve", "root", "from-root"}},
		{"db-admin", []string{"serve", "db", "from-root"}},
		{"cache-admin", []string{"serve", "db", "from-cache"}},
		{"api", []string{"serve", "api-local", "from-api"}},
	} {
		if got := composeBuildWorkload(t, result, tc.service).Command; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s command = %q, want %q", tc.service, got, tc.want)
		}
	}

	for _, tc := range []struct {
		name, include, want string
	}{
		{"missing env_file", "  - path: api/compose.yaml\n    env_file: api/missing.env\n", "env file api/missing.env does not exist in the source"},
		{"env_file outside the source", "  - path: api/compose.yaml\n    env_file: ../secret.env\n", `include env_file "../secret.env": must be a path inside the source`},
		{"env_file not a string", "  - path: api/compose.yaml\n    env_file: [1]\n", "include env_file entries must be strings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scanCompose(t, "include:\n"+tc.include+"services:\n  web:\n    image: nginx:1.27\n",
				"api/compose.yaml", "services:\n  api:\n    image: nginx:1.27\n")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestComposeIncludeAndExtendsErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		files      []string
		want       string
	}{
		{"unknown base", "services:\n  web:\n    extends: missing\n    image: nginx:1.27\n", nil,
			`service "web" extends "missing", which compose.yaml does not define`},
		{"extends cycle", "services:\n  a:\n    extends: b\n    image: nginx:1.27\n  b:\n    extends: a\n", nil,
			"Compose extends cycle"},
		{"missing extends file", "services:\n  web:\n    extends: {file: base.yaml, service: app}\n", nil,
			"Compose file base.yaml does not exist in the source"},
		{"extends file outside the source", "services:\n  web:\n    extends: {file: ../base.yaml, service: app}\n", nil,
			`extends file "../base.yaml": must be a path inside the source`},
		{"missing include", "include: [db.yaml]\nservices:\n  web:\n    image: nginx:1.27\n", nil,
			"Compose file db.yaml does not exist in the source"},
		{"duplicate service", "include: [db.yaml]\nservices:\n  db:\n    image: postgres:16\n",
			[]string{"db.yaml", "services:\n  db:\n    image: postgres:16\n"},
			`service "db" is defined in both compose.yaml and db.yaml`},
		{"include cycle", "include: [a/compose.yaml]\nservices:\n  web:\n    image: nginx:1.27\n",
			[]string{"a/compose.yaml", "include: [../b/compose.yaml]\n", "b/compose.yaml", "include: [../a/compose.yaml]\n"},
			"Compose include cycle"},
		{"merge tags in a linked file", "include: [db.yaml]\nservices:\n  web:\n    image: nginx:1.27\n",
			[]string{"db.yaml", "services:\n  db:\n    image: !reset null\n"},
			"unsupported Compose merge directive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scanCompose(t, tc.body, tc.files...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestComposeLinkedFilesAreBounded(t *testing.T) {
	var include []string
	var files []string
	for i := range composeLinkedFilesMax + 1 {
		name := fmt.Sprintf("parts/%02d.yaml", i)
		include = append(include, name)
		files = append(files, name, fmt.Sprintf("services:\n  svc%02d:\n    image: nginx:1.27\n", i))
	}
	body := "include: [" + strings.Join(include, ", ") + "]\nservices:\n  web:\n    image: nginx:1.27\n"
	_, err := scanCompose(t, body, files...)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("more than %d Compose files", composeLinkedFilesMax)) {
		t.Fatalf("err = %v", err)
	}
}

func TestComposeWithoutLinksIsUnchanged(t *testing.T) {
	body := "services:\n  web:\n    image: nginx:1.27\n    environment: [A=1]\n"
	links, err := resolveComposeLinks(nil, []byte(body), []string{"compose.yaml"}, nil)
	if err != nil || string(links.body) != body || !reflect.DeepEqual(links.sources, []string{"compose.yaml"}) || links.warnings != nil || links.serviceEnv != nil {
		t.Fatalf("links=%+v err=%v", links, err)
	}
}
