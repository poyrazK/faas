package reposcan

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const multiStageDockerfile = "FROM node:22 AS build\nRUN npm ci\nFROM node:22-slim AS runtime\nCOPY --from=build /app /app\n"

func composeBuildWorkload(t *testing.T, result Result, name string) Workload {
	t.Helper()
	for _, workload := range result.Workloads {
		if workload.Name == name {
			return workload
		}
	}
	t.Fatalf("workload %q missing from %+v", name, result.Workloads)
	return Workload{}
}

func TestComposeDockerfileBuildDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, build string
		files       []string
		want        *api.DockerfileBuildOptions
		warnings    []string
	}{
		{"absent", "build: .", nil, nil, nil},
		{"context only", "build: {context: .}", nil, nil, nil},
		{"map form with scalars and target",
			"build:\n      context: .\n      target: runtime\n      args:\n        NODE_ENV: production\n        PORT: 8080\n        DEBUG: false\n        PYTHON: 3.12\n        EMPTY: \"\"",
			nil,
			&api.DockerfileBuildOptions{Target: "runtime", Args: map[string]string{
				"NODE_ENV": "production", "PORT": "8080", "DEBUG": "false", "PYTHON": "3.12", "EMPTY": ""}},
			nil},
		{"list form",
			"build:\n      context: .\n      args: [NODE_ENV=production, \"EQUALS=a=b\", \"BLANK=\"]",
			nil,
			&api.DockerfileBuildOptions{Args: map[string]string{"NODE_ENV": "production", "EQUALS": "a=b", "BLANK": ""}},
			nil},
		{"names without values read .env",
			"build:\n      context: .\n      args:\n        - VERSION\n        - MISSING",
			[]string{".env", "VERSION=1.2.3\n"},
			&api.DockerfileBuildOptions{Args: map[string]string{"VERSION": "1.2.3"}},
			[]string{"build.args.MISSING has no value and .env does not set it; the Dockerfile's ARG default applies"}},
		{"null map value reads .env",
			"build:\n      context: .\n      args:\n        VERSION:\n",
			[]string{".env", "VERSION=2.0\n"},
			&api.DockerfileBuildOptions{Args: map[string]string{"VERSION": "2.0"}},
			nil},
		{"braced interpolation",
			"build:\n      context: .\n      args: {TAG: \"${TAG:-dev}\", HOST: \"${HOST}\"}",
			[]string{".env", "HOST=api.example.com\n"},
			&api.DockerfileBuildOptions{Args: map[string]string{"TAG": "dev", "HOST": "api.example.com"}},
			nil},
		{"unbraced variable passes literally and warns",
			"build:\n      context: .\n      args: [VERSION=$VERSION, \"PRICE=$$5\"]",
			[]string{".env", "VERSION=1\n"},
			&api.DockerfileBuildOptions{Args: map[string]string{"VERSION": "$VERSION", "PRICE": "$5"}},
			[]string{"build.args.VERSION passes $VERSION literally; write ${VERSION} to read it from .env"}},
		{"target matches stage names without regard to case",
			"build: {context: ., target: RUNTIME}",
			[]string{"Dockerfile", "from node:22 as Runtime\n"},
			&api.DockerfileBuildOptions{Target: "RUNTIME"},
			nil},
		{"target on a continued FROM line",
			"build: {context: ., target: deps}",
			[]string{"Dockerfile", "# syntax=docker/dockerfile:1\nFROM --platform=$BUILDPLATFORM \\\n  node:22 \\\n  AS deps\n"},
			&api.DockerfileBuildOptions{Target: "deps"},
			nil},
		{"declared Dockerfile in a sub-context",
			"build: {context: api, dockerfile: docker/prod.Dockerfile, target: runtime}",
			[]string{"api/docker/prod.Dockerfile", multiStageDockerfile, "api/server.js", "x"},
			&api.DockerfileBuildOptions{Target: "runtime"},
			nil},
		{"empty args are no options", "build: {context: ., args: {}}", nil, nil, nil},
		{"context defaults to the project directory", "build: {target: runtime}", nil,
			&api.DockerfileBuildOptions{Target: "runtime"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := tc.files
			if !containsFile(files, "Dockerfile") && !strings.Contains(tc.build, "dockerfile:") {
				files = append(files, "Dockerfile", multiStageDockerfile)
			}
			body := "services:\n  api:\n    " + tc.build + "\n    ports: [8080]\n"
			result, err := scanCompose(t, body, files...)
			if err != nil {
				t.Fatal(err)
			}
			got := composeBuildWorkload(t, result, "api").DockerfileBuild
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DockerfileBuild = %+v, want %+v", got, tc.want)
			}
			for _, want := range tc.warnings {
				if !containsWarning(result.Warnings, want) {
					t.Errorf("warnings %q lack %q", result.Warnings, want)
				}
			}
			for _, warning := range result.Warnings {
				if strings.Contains(warning, "build.args ignored") || strings.Contains(warning, "build.target ignored") {
					t.Errorf("unexpected warning %q", warning)
				}
			}
		})
	}
}

func containsFile(files []string, name string) bool {
	for i := 0; i+1 < len(files); i += 2 {
		if files[i] == name {
			return true
		}
	}
	return false
}

func TestComposeDockerfileBuildRejectsInvalidDeclarations(t *testing.T) {
	manyArgs := make([]string, 0, api.DockerfileBuildArgsMax+1)
	for i := range api.DockerfileBuildArgsMax + 1 {
		manyArgs = append(manyArgs, fmt.Sprintf("A%d=x", i))
	}
	for _, tc := range []struct {
		name, build, want string
	}{
		{"unknown target", "build: {context: ., target: prod}", `build.target "prod" is not a stage in Dockerfile`},
		{"target grammar", "build: {context: ., target: \"-x\"}", `build.target "-x" is not a valid stage name`},
		{"target type", "build: {context: ., target: [runtime]}", "build.target must be a stage name"},
		{"argument name", "build: {context: ., args: [\"1BAD=x\"]}", `build.args name "1BAD"`},
		{"argument value type", "build: {context: ., args: {A: {nested: 1}}}", "build.args.A must be a string, number or boolean"},
		{"list entry type", "build: {context: ., args: [{A: 1}]}", "build.args list entries must be NAME=value or NAME"},
		{"args shape", "build: {context: ., args: production}", "build.args must be a mapping or a list"},
		{"argument count", "build: {context: ., args: [" + strings.Join(manyArgs, ", ") + "]}", "the limit is 64"},
		{"value size", "build: {context: ., args: {A: " + strings.Repeat("x", api.DockerfileBuildArgValueMaxBytes+1) + "}}", "build.args.A is 4097 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "services:\n  api:\n    " + tc.build + "\n    ports: [8080]\n"
			_, err := scanCompose(t, body, "Dockerfile", multiStageDockerfile)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestComposeDockerfileBuildWithoutDockerfileWarns(t *testing.T) {
	body := "services:\n  api:\n    build: {context: api, args: {NODE_ENV: production}, target: runtime}\n    ports: [3000]\n"
	result, err := scanCompose(t, body, "api/package.json", `{"scripts":{"start":"node server.js"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := composeBuildWorkload(t, result, "api").DockerfileBuild; got != nil {
		t.Fatalf("DockerfileBuild = %+v, want nil for a Railpack build", got)
	}
	if !containsWarning(result.Warnings, "api: build.args and build.target ignored: the build context has no Dockerfile, so it builds with Railpack") {
		t.Fatalf("warnings = %q", result.Warnings)
	}
}

func TestComposeDockerfileBuildIgnoredForPrebuiltImages(t *testing.T) {
	body := "services:\n  web:\n    image: nginx:1.27\n    ports: [80]\n"
	result, err := scanCompose(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if got := composeBuildWorkload(t, result, "web").DockerfileBuild; got != nil {
		t.Fatalf("DockerfileBuild = %+v, want nil", got)
	}
}

func TestDockerfileStages(t *testing.T) {
	body := "# FROM commented AS nope\nFROM golang:1.26 AS Build\nRUN go build\nFROM scratch\nFROM build AS test\nfrom --platform=linux/amd64 alpine:3 as final\n"
	want := []string{"build", "test", "final"}
	if got := dockerfileStages(body); !reflect.DeepEqual(got, want) {
		t.Fatalf("dockerfileStages = %q, want %q", got, want)
	}
}
