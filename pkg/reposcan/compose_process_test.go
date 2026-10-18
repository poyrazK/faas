// adr: 1337
package reposcan

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
)

func scanCompose(t *testing.T, body string, extra ...string) (Result, error) {
	t.Helper()
	files := fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte(body)}}
	for i := 0; i+1 < len(extra); i += 2 {
		files[extra[i]] = &fstest.MapFile{Data: []byte(extra[i+1])}
	}
	return Scan(files)
}

// strs keeps an empty entrypoint non-nil: it must encode as [] (cleared), not
// null (inherited), in the frozen deployment contract.
func strs(values ...string) *[]string {
	if values == nil {
		values = []string{}
	}
	return &values
}
func secs(value int) *int { return &value }

func TestComposeImageProcessDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, declaration string
		want              *api.ComposeProcess
	}{
		{"absent", "", nil},
		{"entrypoint list", `entrypoint: [tini, --]`, &api.ComposeProcess{Entrypoint: strs("tini", "--")}},
		{"entrypoint words", `entrypoint: /usr/bin/env node`, &api.ComposeProcess{Entrypoint: strs("/usr/bin/env", "node")}},
		{"entrypoint cleared with command", "entrypoint: []\n    command: [node, server.js]", &api.ComposeProcess{Entrypoint: strs()}},
		{"entrypoint empty string with command", "entrypoint: \"\"\n    command: [node, server.js]", &api.ComposeProcess{Entrypoint: strs()}},
		{"user name", `user: node`, &api.ComposeProcess{User: "node"}},
		{"user id", `user: 1000`, &api.ComposeProcess{User: "1000"}},
		{"user and group", `user: "1000:1000"`, &api.ComposeProcess{User: "1000:1000"}},
		{"working_dir", `working_dir: /srv/app`, &api.ComposeProcess{WorkingDir: "/srv/app"}},
		{"stop_signal short name", `stop_signal: INT`, &api.ComposeProcess{StopSignal: "SIGINT"}},
		{"stop_signal number", `stop_signal: "15"`, &api.ComposeProcess{StopSignal: "SIGTERM"}},
		{"stop_grace_period", `stop_grace_period: 1m30s`, &api.ComposeProcess{StopGracePeriodS: secs(90)}},
		{"stop_grace_period rounds up", `stop_grace_period: 1500ms`, &api.ComposeProcess{StopGracePeriodS: secs(2)}},
		{"interpolated", "user: ${APP_USER}\n    working_dir: ${APP_DIR:-/app}", &api.ComposeProcess{User: "app", WorkingDir: "/app"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "services:\n  web:\n    image: nginx:1.27\n    ports: [80]\n    " + tc.declaration + "\n"
			result, err := scanCompose(t, body, ".env", "APP_USER=app\n")
			if err != nil || len(result.Workloads) != 1 {
				t.Fatalf("scan = %+v, %v", result, err)
			}
			if got := result.Workloads[0].ImageProcess; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("process = %#v; want %#v", got, tc.want)
			}
		})
	}
}

func TestComposeImageProcessRejectsMalformedDeclaration(t *testing.T) {
	for _, tc := range []struct{ declaration, want string }{
		{`entrypoint: "sh -c 'echo hi'"`, "list form"},
		{`entrypoint: echo $$HOME`, "list form"},
		{`entrypoint: []`, "also set command"},
		{`entrypoint: [3]`, "entrypoint arguments must be strings"},
		{`entrypoint: {a: b}`, "entrypoint must be"},
		{`entrypoint: ["", run]`, "entrypoint must name an executable"},
		{`user: "a b"`, "user must be"},
		{`user: ":staff"`, "user must be"},
		{`user: ""`, "user must be"},
		{`working_dir: app`, "working_dir must be"},
		{`working_dir: /srv/../app`, "working_dir must be"},
		{`stop_signal: SIGKILL`, "stop_signal"},
		{`stop_signal: SIGWINCH`, "stop_signal"},
		{`stop_grace_period: 30`, "stop_grace_period"},
		{`stop_grace_period: 500ms`, "stop_grace_period"},
		{`stop_grace_period: 10m`, "stop_grace_period"},
		{`stop_grace_period: soon`, "stop_grace_period"},
	} {
		t.Run(tc.declaration, func(t *testing.T) {
			body := "services:\n  web:\n    image: nginx:1.27\n    " + tc.declaration + "\n"
			_, err := scanCompose(t, body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestComposeBuildServiceProcessKeysWarn(t *testing.T) {
	body := "services:\n  api:\n    build: .\n    ports: [\"8080:8080\"]\n    entrypoint: [tini, --]\n    user: node\n    stop_signal: SIGINT\n"
	result, err := scanCompose(t, body, "Dockerfile", "FROM node:22\n")
	if err != nil || len(result.Workloads) != 1 {
		t.Fatalf("scan = %+v, %v", result, err)
	}
	if result.Workloads[0].ImageProcess != nil {
		t.Fatalf("build service carried an image process: %#v", result.Workloads[0].ImageProcess)
	}
	want := "compose.yaml: api: entrypoint, user, stop_signal ignored: Compose process overrides apply only to prebuilt images"
	if !containsWarning(result.Warnings, want) {
		t.Fatalf("warnings %q lack %q", result.Warnings, want)
	}
}

func TestComposeEnvFileKeys(t *testing.T) {
	envFile := "# comment\n\nexport DATABASE_URL=postgres://db\nAPI_TOKEN='quoted value'\nFEATURE_FLAG\nPORT=8080 # trailing\n"
	for _, tc := range []struct {
		name, declaration string
		files             []string
		want              []string
		warning           string
	}{
		{"string path", "env_file: app.env", []string{"app.env", envFile},
			[]string{"API_TOKEN", "DATABASE_URL", "FEATURE_FLAG", "LOG_LEVEL", "PORT"}, ""},
		{"list merged with environment", "env_file: [app.env, ./more.env]", []string{"app.env", envFile, "more.env", "LOG_LEVEL=debug\nEXTRA=1\n"},
			[]string{"API_TOKEN", "DATABASE_URL", "EXTRA", "FEATURE_FLAG", "LOG_LEVEL", "PORT"}, ""},
		{"missing required file", "env_file: .env.production", nil,
			[]string{"LOG_LEVEL"}, "env_file .env.production is not in the source"},
		{"missing optional file", "env_file: [{path: .env.local, required: false}]", nil,
			[]string{"LOG_LEVEL"}, ""},
		{"long form present", "env_file: [{path: app.env, format: raw}]", []string{"app.env", "ONLY_KEY=1\n"},
			[]string{"LOG_LEVEL", "ONLY_KEY"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "services:\n  web:\n    image: nginx:1.27\n    ports: [80]\n    environment: {LOG_LEVEL: info}\n    " + tc.declaration + "\n"
			result, err := scanCompose(t, body, tc.files...)
			if err != nil || len(result.Workloads) != 1 {
				t.Fatalf("scan = %+v, %v", result, err)
			}
			if got := result.Workloads[0].EnvKeys; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("env keys = %q; want %q", got, tc.want)
			}
			if tc.warning != "" && !containsWarning(result.Warnings, tc.warning) {
				t.Fatalf("warnings %q lack %q", result.Warnings, tc.warning)
			}
			for _, warning := range result.Warnings {
				if strings.Contains(warning, "postgres://") || strings.Contains(warning, "quoted value") {
					t.Fatalf("warning leaked an env value: %q", warning)
				}
			}
		})
	}
}

func TestComposeEnvFileRejectsMalformedDeclaration(t *testing.T) {
	for _, tc := range []struct {
		declaration string
		files       []string
		want        string
	}{
		{"env_file: ../secrets.env", nil, "must be a file inside the source"},
		{"env_file: /etc/app.env", nil, "must be a file inside the source"},
		{"env_file: [{path: app.env, required: maybe}]", nil, "required must be a boolean"},
		{"env_file: [{path: app.env, format: json}]", nil, "format"},
		{"env_file: {path: app.env}", nil, "env_file must be"},
		{"env_file: app.env", []string{"app.env", "1BAD=value\n"}, "line 1 has an invalid variable declaration"},
	} {
		t.Run(tc.declaration, func(t *testing.T) {
			body := "services:\n  web:\n    image: nginx:1.27\n    " + tc.declaration + "\n"
			_, err := scanCompose(t, body, tc.files...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestComposeUnsupportedKeysWarn(t *testing.T) {
	body := `services:
  web:
    image: nginx:1.27
    ports: [80]
    container_name: web
    platform: linux/amd64
    init: true
    x-team: storefront
    volumes: [./data:/data]
    deploy: {replicas: 3}
    restart: always
    labels: {tier: web}
    secrets: [api_key]
    networks: [front]
    mystery: 1
  api:
    build:
      context: .
      args: {NODE_ENV: production}
      target: runtime
      cache_from: [api:latest]
    platform: linux/arm64
  db:
    image: postgres:16
    volumes: [pgdata:/var/lib/postgresql/data]
`
	result, err := scanCompose(t, body, "Dockerfile", "FROM node:22\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"web: volumes ignored: persistent volumes and bind mounts are not supported",
		"web: deploy ignored: CPU, memory and replicas come from the plan",
		"web: restart ignored: the platform starts, parks and restarts services itself",
		"web: labels ignored: container labels are not used",
		"web: secrets ignored: Compose secrets are not mounted; set them with `gregale secrets set`",
		"web: networks ignored: services reach each other through depends_on service bindings",
		"web: mystery ignored: not supported by Gregale",
		"api: build.args ignored: build args are not passed to the builder",
		"api: build.target ignored: the builder builds the Dockerfile's final stage",
		"api: build.cache_from ignored: the builder uses only build.context and build.dockerfile",
		"api: platform ignored: services run on linux/amd64",
	} {
		if !containsWarning(result.Warnings, want) {
			t.Errorf("warnings lack %q", want)
		}
	}
	for _, quiet := range []string{"container_name", "web: platform", "init", "x-team", "db: volumes"} {
		for _, warning := range result.Warnings {
			if strings.Contains(warning, quiet+" ignored") {
				t.Errorf("unexpected warning %q", warning)
			}
		}
	}
}

func TestComposeRejectsExtendsAndInclude(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"services:\n  web:\n    image: nginx:1.27\n    extends: {service: base}\n", "extends is not supported"},
		{"include: [other.yaml]\nservices:\n  web:\n    image: nginx:1.27\n", "uses include, which is not supported"},
	} {
		_, err := scanCompose(t, tc.body)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("err = %v; want it to mention %q", err, tc.want)
		}
	}
}

func containsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}
