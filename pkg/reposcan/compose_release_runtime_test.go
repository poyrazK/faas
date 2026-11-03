package reposcan

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestComposeReleasePreservesEntrypoint(t *testing.T) {
	for _, tc := range []struct {
		name, source, entrypoint, command string
		want                              []string
		shell                             bool
	}{
		{"image exec", "image: ghcr.io/example/api:v1", `["python", "manage.py"]`, `["migrate", "--noinput"]`, []string{"python", "manage.py", "migrate", "--noinput"}, false},
		{"image empty entrypoint", "image: ghcr.io/example/api:v1", `[]`, `["./migrate"]`, []string{"./migrate"}, false},
		{"image shell", "image: ghcr.io/example/api:v1", `["/app/my tool", "--mode"]`, `'migrate && echo done'`, []string{"'/app/my tool' --mode migrate && echo done"}, true},
		{"built exec once", "build: ./api", `["python", "manage.py"]`, `["migrate"]`, []string{"python", "manage.py", "migrate"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "services:\n  migrate:\n    " + tc.source + "\n    entrypoint: " + tc.entrypoint + "\n    command: " + tc.command + "\n  api:\n    " + tc.source + "\n    ports: [\"8000:8000\"]\n    depends_on:\n      migrate:\n        condition: service_completed_successfully\n"
			result, err := Scan(composeFixture(body))
			if err != nil {
				t.Fatal(err)
			}
			w := scanWorkload(t, result, "api")
			if !reflect.DeepEqual(w.ReleaseCommand, tc.want) || w.ReleaseCommandShell != tc.shell {
				t.Fatalf("command=%q shell=%v; want %q shell=%v", w.ReleaseCommand, w.ReleaseCommandShell, tc.want, tc.shell)
			}
			if w.ImageProcess != nil && w.ImageProcess.Entrypoint != nil {
				t.Fatal("migration entrypoint changed the serving process")
			}
		})
	}
}

func TestComposeReleasePreservesSecretFiles(t *testing.T) {
	for _, tc := range []struct {
		name, owner string
		wantErr     bool
	}{
		{"migration only", "", false},
		{"shared identical mount", "    secrets: [db_password]\n", false},
		{"equivalent default mode", "    secrets:\n      - source: db_password\n        mode: 0444\n", false},
		{"conflicting source", "    secrets:\n      - source: other_password\n        target: db_password\n", true},
		{"conflicting permissions", "    secrets:\n      - source: db_password\n        mode: 0400\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "services:\n  migrate:\n    image: ghcr.io/example/api:v1\n    command: [\"./migrate\"]\n    secrets: [db_password]\n  api:\n    image: ghcr.io/example/api:v1\n    ports: [\"8000:8000\"]\n" + tc.owner + "    depends_on:\n      migrate:\n        condition: service_completed_successfully\nsecrets:\n  db_password:\n    environment: DB_PASSWORD\n  other_password:\n    environment: OTHER_PASSWORD\n"
			result, err := Scan(fstest.MapFS{"compose.yaml": {Data: []byte(body)}})
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "conflicting declarations") {
					t.Fatalf("conflict was not rejected: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			files := scanWorkload(t, result, "api").SecretFiles
			if len(files) != 1 || files[0].Secret != "DB_PASSWORD" || files[0].Name != "db_password" || files[0].EffectiveMode() != api.SecretFileDefaultMode {
				t.Fatalf("migration secret mount lost or duplicated: %+v", files)
			}
		})
	}
}

func TestComposeReleaseRequiresMatchingUserAndWorkingDir(t *testing.T) {
	for _, tc := range []struct{ name, step, owner, wantError string }{
		{"working directory differs", "    working_dir: /app/migrations\n", "    working_dir: /app/web\n", "working_dir"},
		{"user differs", "    user: '0'\n", "    user: '1000'\n", "user"},
		{"explicit versus inherited user", "    user: '0'\n", "", "user"},
		{"same runtime", "    user: '1000'\n    working_dir: /app\n", "    user: '1000'\n    working_dir: /app\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "services:\n  migrate:\n    image: ghcr.io/example/api:v1\n    command: [\"./migrate\"]\n" + tc.step + "  api:\n    image: ghcr.io/example/api:v1\n    ports: [\"8000:8000\"]\n" + tc.owner + "    depends_on:\n      migrate:\n        condition: service_completed_successfully\n"
			result, err := Scan(fstest.MapFS{"compose.yaml": {Data: []byte(body)}})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || !strings.Contains(err.Error(), "make those settings match") {
					t.Fatalf("runtime mismatch accepted: %v", err)
				}
				return
			}
			if err != nil || len(result.Workloads) != 1 || len(result.Workloads[0].ReleaseCommand) != 1 {
				t.Fatalf("matching runtime rejected: %+v %v", result, err)
			}
		})
	}
}

func TestComposeReleaseSelectsOwnerWithCompatibleRuntime(t *testing.T) {
	body := "services:\n  migrate:\n    image: ghcr.io/example/api:v1\n    command: [\"./migrate\"]\n    user: '1000'\n  api:\n    image: ghcr.io/example/api:v1\n    ports: [\"8000:8000\"]\n    user: '0'\n    depends_on:\n      migrate:\n        condition: service_completed_successfully\n  web:\n    image: ghcr.io/example/api:v1\n    ports: [\"8001:8000\"]\n    user: '1000'\n    depends_on:\n      migrate:\n        condition: service_completed_successfully\n"
	result, err := Scan(fstest.MapFS{"compose.yaml": {Data: []byte(body)}})
	if err != nil {
		t.Fatal(err)
	}
	if scanWorkload(t, result, "web").ReleaseService != "migrate" || len(scanWorkload(t, result, "api").ReleaseCommand) != 0 {
		t.Fatal("migration selected an incompatible owner")
	}
}
