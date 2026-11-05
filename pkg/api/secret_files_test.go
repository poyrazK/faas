package api

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateSecretFiles(t *testing.T) {
	many := make([]SecretFile, SecretFilesMax+1)
	for i := range many {
		many[i] = SecretFile{Secret: "TOKEN", Name: fmt.Sprintf("f%d", i)}
	}
	for _, tc := range []struct {
		name  string
		files []SecretFile
		want  string
	}{
		{"none", nil, ""},
		{"valid", []SecretFile{{Secret: "DB_PASSWORD", Name: "db.password", Mode: 0o400}, {Secret: "API_KEY", Name: "api-key_1"}}, ""},
		{"too many", many, fmt.Sprintf("the limit is %d", SecretFilesMax)},
		{"path separator", []SecretFile{{Secret: "TOKEN", Name: "a/b"}}, "secret file name"},
		{"dot dot", []SecretFile{{Secret: "TOKEN", Name: ".."}}, "secret file name"},
		{"empty name", []SecretFile{{Secret: "TOKEN"}}, "secret file name"},
		{"duplicate name", []SecretFile{{Secret: "A", Name: "x"}, {Secret: "B", Name: "x"}}, "declared twice"},
		{"invalid secret name", []SecretFile{{Secret: "lower-case", Name: "x"}}, `names app secret "lower-case"`},
		{"mode beyond permission bits", []SecretFile{{Secret: "TOKEN", Name: "x", Mode: 0o4755}}, "at most 0777"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSecretFiles(tc.files)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSecretFileEffectiveMode(t *testing.T) {
	if got := (SecretFile{}).EffectiveMode(); got != 0o444 {
		t.Fatalf("default mode = %#o, want 0444", got)
	}
	if got := (SecretFile{Mode: 0o400}).EffectiveMode(); got != 0o400 {
		t.Fatalf("mode = %#o, want 0400", got)
	}
}

func TestAppManifestValidatesSecretFiles(t *testing.T) {
	m := AppManifest{Entrypoint: []string{"/app"}, SecretFiles: []SecretFile{{Secret: "TOKEN", Name: "../etc/passwd"}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "secret file name") {
		t.Fatalf("err = %v, want a secret file name error", err)
	}
}
