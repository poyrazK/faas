package reposcan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestComposeSecretFiles(t *testing.T) {
	const top = "secrets:\n  db_password:\n    environment: DB_PASSWORD\n  api_key:\n    environment: API_KEY\n  local_file:\n    file: ./secret.txt\n"
	for _, tc := range []struct {
		name, secrets string
		want          []api.SecretFile
		warning       string
	}{
		{"short form", "secrets: [db_password]",
			[]api.SecretFile{{Secret: "DB_PASSWORD", Name: "db_password"}}, ""},
		{"long form with target and octal mode", "secrets:\n      - source: api_key\n        target: key.txt\n        mode: 0440\n      - db_password",
			[]api.SecretFile{{Secret: "API_KEY", Name: "key.txt", Mode: 0o440}, {Secret: "DB_PASSWORD", Name: "db_password"}}, ""},
		{"absolute target in /run/secrets", "secrets:\n      - source: api_key\n        target: /run/secrets/api",
			[]api.SecretFile{{Secret: "API_KEY", Name: "api"}}, ""},
		{"string mode", "secrets:\n      - source: api_key\n        mode: \"0400\"",
			[]api.SecretFile{{Secret: "API_KEY", Name: "api_key", Mode: 0o400}}, ""},
		{"file secrets are not uploaded", "secrets: [db_password, local_file]",
			[]api.SecretFile{{Secret: "DB_PASSWORD", Name: "db_password"}}, `secret "local_file" ignored: only secrets declared with environment: NAME`},
		{"targets outside /run/secrets are not written", "secrets:\n      - source: api_key\n        target: /etc/app/key\n      - db_password",
			[]api.SecretFile{{Secret: "DB_PASSWORD", Name: "db_password"}}, `secret "api_key" ignored: target "/etc/app/key" is not a file in /run/secrets`},
		{"uid and gid are ignored", "secrets:\n      - source: api_key\n        uid: \"1000\"\n        gid: \"1000\"",
			[]api.SecretFile{{Secret: "API_KEY", Name: "api_key"}}, `secret "api_key": uid ignored`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "services:\n  web:\n    image: nginx:1.27\n    ports: [80]\n    " + tc.secrets + "\n" + top
			result, err := scanCompose(t, body)
			if err != nil {
				t.Fatal(err)
			}
			if got := composeBuildWorkload(t, result, "web").SecretFiles; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("secret files = %+v, want %+v", got, tc.want)
			}
			if tc.warning != "" && !containsWarning(result.Warnings, tc.warning) {
				t.Fatalf("warnings %q lack %q", result.Warnings, tc.warning)
			}
			if containsWarning(result.Warnings, "secrets ignored:") || containsWarning(result.Warnings, "Compose secrets are not mounted") {
				t.Fatalf("service secrets still reported as unsupported: %q", result.Warnings)
			}
		})
	}
	for _, tc := range []struct {
		name, secrets, want string
	}{
		{"undeclared secret", "secrets: [missing]", `secret "missing" is not declared under the top-level secrets`},
		{"not a list", "secrets: db_password", "secrets must be a list"},
		{"entry without a source", "secrets:\n      - target: x", "secrets entries must name a secret"},
		{"invalid mode", "secrets:\n      - source: api_key\n        mode: \"rw\"", `mode "rw" must be octal permission bits`},
		{"mode beyond permission bits", "secrets:\n      - source: api_key\n        mode: 04755", "must be permission bits"},
		{"two secrets on one file", "secrets:\n      - source: api_key\n        target: same\n      - source: db_password\n        target: same", `secret file "same" is declared twice`},
		{"hidden file name", "secrets:\n      - source: api_key\n        target: .key", "secret file name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "services:\n  web:\n    image: nginx:1.27\n    " + tc.secrets + "\n" + top
			if _, err := scanCompose(t, body); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	t.Run("built services and plans carry them", func(t *testing.T) {
		body := "services:\n  web:\n    build: .\n    ports: [3000]\n    secrets: [db_password]\n" + top
		result, err := scanCompose(t, body, "Dockerfile", "FROM node:22\n")
		if err != nil {
			t.Fatal(err)
		}
		want := []api.SecretFile{{Secret: "DB_PASSWORD", Name: "db_password"}}
		workload := composeBuildWorkload(t, result, "web")
		if !reflect.DeepEqual(workload.SecretFiles, want) {
			t.Fatalf("secret files = %+v, want %+v", workload.SecretFiles, want)
		}
		if got := PlanWorkload(workload).SecretFiles; !reflect.DeepEqual(got, want) {
			t.Fatalf("plan secret files = %+v, want %+v", got, want)
		}
	})
	t.Run("interpolated from .env", func(t *testing.T) {
		body := "services:\n  web:\n    image: nginx:1.27\n    secrets:\n      - source: api_key\n        target: ${KEY_FILE}\n" + top
		result, err := scanCompose(t, body, ".env", "KEY_FILE=token\n")
		if err != nil {
			t.Fatal(err)
		}
		want := []api.SecretFile{{Secret: "API_KEY", Name: "token"}}
		if got := composeBuildWorkload(t, result, "web").SecretFiles; !reflect.DeepEqual(got, want) {
			t.Fatalf("secret files = %+v, want %+v", got, want)
		}
	})
}
