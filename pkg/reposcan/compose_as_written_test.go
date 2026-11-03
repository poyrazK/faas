// adr: 20261102192943
package reposcan

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
)

// composeFixture is a Compose file plus the build contexts it names, each a
// small Express service listening on 3000.
func composeFixture(compose string) fstest.MapFS {
	fsys := fstest.MapFS{"compose.yaml": {Data: []byte(compose)}}
	for _, dir := range []string{"api", "web", "admin", "tools"} {
		fsys[dir+"/Dockerfile"] = &fstest.MapFile{Data: []byte("FROM node:22\nWORKDIR /app\nCOPY . .\nEXPOSE 3000\nCMD [\"node\",\"server.js\"]\n")}
		fsys[dir+"/package.json"] = &fstest.MapFile{Data: []byte(`{"name":"svc","dependencies":{"express":"4"}}`)}
		fsys[dir+"/server.js"] = &fstest.MapFile{Data: []byte("require('express')().listen(3000)\n")}
	}
	return fsys
}

func scanWorkload(t *testing.T, result Result, name string) Workload {
	t.Helper()
	for _, w := range result.Workloads {
		if w.Name == name {
			return w
		}
	}
	t.Fatalf("workload %q missing from %+v", name, result.Workloads)
	return Workload{}
}

func hasWarning(result Result, parts ...string) bool {
	return slices.ContainsFunc(result.Warnings, func(warning string) bool {
		for _, part := range parts {
			if !strings.Contains(warning, part) {
				return false
			}
		}
		return true
	})
}

// A port Compose binds to a loopback address is published only on the
// machine running Compose, so the service must not get a public URL.
func TestComposeLoopbackPortsStayInternal(t *testing.T) {
	for _, tc := range []struct {
		name, ports string
		internal    bool
	}{
		{"ipv4 loopback", `["127.0.0.1:9000:3000"]`, true},
		{"other loopback address", `["127.0.0.2:9000:3000/tcp"]`, true},
		{"ipv6 loopback", `["[::1]:9000:3000"]`, true},
		{"localhost", `["localhost:9000:3000"]`, true},
		{"long form", "\n      - target: 3000\n        published: \"9000\"\n        host_ip: 127.0.0.1", true},
		{"all interfaces", `["0.0.0.0:9000:3000"]`, false},
		{"no address", `["9000:3000"]`, false},
		{"one loopback one public", `["127.0.0.1:9000:3000", "9001:3001"]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Scan(composeFixture("services:\n  web:\n    build: ./web\n    ports: [\"3000:3000\"]\n  admin:\n    build: ./admin\n    ports: " + tc.ports + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			admin := scanWorkload(t, result, "admin")
			if admin.Class != ClassHTTP {
				t.Fatalf("class = %q, want http: the service still listens", admin.Class)
			}
			if got := NewWorkloadVisibility(admin) == api.AppVisibilityInternal; got != tc.internal {
				t.Fatalf("internal = %v, want %v (workload %+v)", got, tc.internal, admin)
			}
			if warned := hasWarning(result, "admin", "loopback"); warned != tc.internal {
				t.Fatalf("loopback warning = %v, want %v: %v", warned, tc.internal, result.Warnings)
			}
			if web := scanWorkload(t, result, "web"); NewWorkloadVisibility(web) != "" {
				t.Fatalf("published web became internal: %+v", web)
			}
		})
	}
}

// Prebuilt tools such as Adminer bound to localhost stay internal too, and
// keep their container port as the serving port.
func TestComposeLoopbackImagePortStaysInternal(t *testing.T) {
	result, err := Scan(composeFixture("services:\n  adminer:\n    image: adminer:4\n    ports: [\"127.0.0.1:8081:8080\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	adminer := scanWorkload(t, result, "adminer")
	if NewWorkloadVisibility(adminer) != api.AppVisibilityInternal || !slices.Equal(adminer.Ports, []int{8080}) {
		t.Fatalf("adminer = %+v, want internal on container port 8080", adminer)
	}
}

// depends_on: {db: {condition: service_healthy}} on a Compose datastore is the
// most common Compose shape; it deploys with the gate relaxed to a plain
// dependency, so the service keeps the datastore's variable.
func TestComposeDatastoreHealthGateRelaxed(t *testing.T) {
	result, err := Scan(composeFixture(`services:
  web:
    build: ./web
    ports: ["3000:3000"]
    environment:
      DATABASE_URL: postgres://app:secret@db:5432/app
    depends_on:
      db:
        condition: service_healthy
      cache:
        condition: service_healthy
      api:
        condition: service_healthy
  api:
    build: ./api
    expose: ["3000"]
  db:
    image: postgres:16
    healthcheck:
      test: ["CMD-SHELL", "pg_isready"]
  cache:
    image: redis:7
`))
	if err != nil {
		t.Fatal(err)
	}
	web := scanWorkload(t, result, "web")
	want := map[string]string{"db": api.ComposeDependencyStarted, "cache": api.ComposeDependencyStarted, "api": api.ComposeDependencyHealthy}
	if len(web.DependsOnConditions) != len(want) {
		t.Fatalf("conditions = %v, want %v", web.DependsOnConditions, want)
	}
	for name, condition := range want {
		if web.DependsOnConditions[name] != condition {
			t.Fatalf("conditions = %v, want %v: only datastore gates relax", web.DependsOnConditions, want)
		}
	}
	if !slices.Contains(web.DependsOn, "db") {
		t.Fatalf("db dependency dropped: %v", web.DependsOn)
	}
	if reasons := DependencyValidationReasons(result.Workloads, result.Managed); len(reasons) != 0 {
		t.Fatalf("plan blocked: %v", reasons)
	}
	if !hasWarning(result, "web", "db", "service_healthy") || !hasWarning(result, "web", "cache", "service_healthy") {
		t.Fatalf("relaxed gates not reported: %v", result.Warnings)
	}
}

// A migration service the others wait for with service_completed_successfully
// runs as the release command of the waiting service that shares its build.
func TestComposeCompletionDependencyBecomesReleaseCommand(t *testing.T) {
	result, err := Scan(composeFixture(`services:
  migrate:
    build: ./api
    command: npm run migrate
    environment:
      MIGRATIONS_DIR: db/migrations
    depends_on:
      db:
        condition: service_healthy
  api:
    build: ./api
    ports: ["3000:3000"]
    depends_on:
      migrate:
        condition: service_completed_successfully
  web:
    build: ./web
    ports: ["8080:3000"]
    depends_on:
      migrate:
        condition: service_completed_successfully
      api:
        condition: service_started
  db:
    image: postgres:16
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range result.Workloads {
		if w.Name == "migrate" {
			t.Fatalf("migrate deployed as an app: %+v", w)
		}
		if slices.Contains(w.DependsOn, "migrate") || w.DependsOnConditions["migrate"] != "" {
			t.Fatalf("%s still depends on migrate: %v %v", w.Name, w.DependsOn, w.DependsOnConditions)
		}
	}
	apiWorkload := scanWorkload(t, result, "api")
	if !slices.Equal(apiWorkload.ReleaseCommand, []string{"npm run migrate"}) || !apiWorkload.ReleaseCommandShell || apiWorkload.ReleaseService != "migrate" {
		t.Fatalf("api release = %v shell=%v service=%q", apiWorkload.ReleaseCommand, apiWorkload.ReleaseCommandShell, apiWorkload.ReleaseService)
	}
	if !slices.Contains(apiWorkload.EnvKeys, "MIGRATIONS_DIR") || !slices.Contains(apiWorkload.DependsOn, "db") {
		t.Fatalf("api lacks the step's environment or dependencies: env=%v deps=%v", apiWorkload.EnvKeys, apiWorkload.DependsOn)
	}
	if web := scanWorkload(t, result, "web"); len(web.ReleaseCommand) != 0 {
		t.Fatalf("web also runs the migration: %v", web.ReleaseCommand)
	}
	if !hasWarning(result, "migrate runs as the release command of api") || !hasWarning(result, "web depends on migrate", "does not wait") {
		t.Fatalf("fold not reported: %v", result.Warnings)
	}
	if reasons := DependencyValidationReasons(result.Workloads, result.Managed); len(reasons) != 0 {
		t.Fatalf("plan blocked: %v", reasons)
	}
}

// Exec-form commands and prebuilt images fold the same way.
func TestComposeCompletionDependencyExecFormImage(t *testing.T) {
	result, err := Scan(composeFixture(`services:
  seed:
    image: ghcr.io/acme/shop:1.4
    command: ["./shop", "seed"]
  shop:
    image: ghcr.io/acme/shop:1.4
    ports: ["8080:8080"]
    depends_on:
      seed:
        condition: service_completed_successfully
`))
	if err != nil {
		t.Fatal(err)
	}
	shop := scanWorkload(t, result, "shop")
	if !slices.Equal(shop.ReleaseCommand, []string{"./shop", "seed"}) || shop.ReleaseCommandShell {
		t.Fatalf("shop release = %v shell=%v", shop.ReleaseCommand, shop.ReleaseCommandShell)
	}
}

func TestComposeCompletionDependencyRefusals(t *testing.T) {
	for _, tc := range []struct{ name, compose, want string }{
		{"other build", `services:
  migrate:
    build: ./tools
    command: npm run migrate
  api:
    build: ./api
    ports: ["3000:3000"]
    depends_on:
      migrate:
        condition: service_completed_successfully
`, "build migrate from the same build: or image:"},
		{"other build args", `services:
  migrate:
    build:
      context: ./api
      args:
        MODE: migrate
    command: npm run migrate
  api:
    build: ./api
    ports: ["3000:3000"]
    depends_on:
      migrate:
        condition: service_completed_successfully
`, "same build: or image:"},
		{"publishes ports", `services:
  migrate:
    build: ./api
    command: npm run migrate
    ports: ["9000:3000"]
  api:
    build: ./api
    ports: ["3000:3000"]
    depends_on:
      migrate:
        condition: service_completed_successfully
`, "publishes ports"},
		{"datastore", `services:
  api:
    build: ./api
    ports: ["3000:3000"]
    depends_on:
      db:
        condition: service_completed_successfully
  db:
    image: postgres:16
`, "not a service Gregale builds"},
		{"chained", `services:
  migrate:
    build: ./api
    command: npm run migrate
  seed:
    build: ./api
    command: npm run seed
    depends_on:
      migrate:
        condition: service_completed_successfully
  api:
    build: ./api
    ports: ["3000:3000"]
    depends_on:
      seed:
        condition: service_completed_successfully
`, "chained completion dependencies"},
		{"only a worker waits", `services:
  migrate:
    build: ./tools
    command: npm run migrate
  worker:
    build: ./tools
    command: npm run work
    depends_on:
      migrate:
        condition: service_completed_successfully
`, "waiting service that serves requests"},
		{"two steps for one service", `services:
  migrate:
    build: ./api
    command: npm run migrate
  seed:
    build: ./api
    command: npm run seed
  api:
    build: ./api
    ports: ["3000:3000"]
    depends_on:
      migrate:
        condition: service_completed_successfully
      seed:
        condition: service_completed_successfully
`, "one release command per service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Scan(composeFixture(tc.compose))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// A Procfile release: and a Compose completion dependency would both claim the
// same service's release command.
func TestComposeCompletionDependencyConflictsWithProcfileRelease(t *testing.T) {
	fsys := composeFixture(`services:
  migrate:
    build: .
    command: npm run migrate
  web:
    build: .
    ports: ["3000:3000"]
    depends_on:
      migrate:
        condition: service_completed_successfully
`)
	fsys["Dockerfile"] = fsys["api/Dockerfile"]
	fsys["package.json"] = fsys["api/package.json"]
	fsys["Procfile"] = &fstest.MapFile{Data: []byte("web: node server.js\nrelease: npm run db:migrate\n")}
	_, err := Scan(fsys)
	if err == nil || !strings.Contains(err.Error(), "already runs Compose service migrate") {
		t.Fatalf("error = %v, want the Procfile conflict", err)
	}
}
