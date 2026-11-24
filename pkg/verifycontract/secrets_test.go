package verifycontract

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSecretRefsAreCollectedFromHTTPHeaders(t *testing.T) {
	c, err := ParseYAML([]byte(`version: 1
checks:
  - name: me
    http:
      path: /api/me
      headers:
        Authorization: "Bearer ${secret:API_TOKEN}"
        X-Tenant: "${secret:TENANT_ID}-${secret:API_TOKEN}"
  - name: health
    http:
      path: /healthz
`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.SecretRefs(), []string{"API_TOKEN", "TENANT_ID"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SecretRefs = %v, want %v", got, want)
	}
}

func TestValidateRefusesBadSecretRefs(t *testing.T) {
	tests := []struct{ name, check, want string }{
		{"invalid name", "http:\n      path: /\n      headers:\n        Authorization: \"${secret:bad-name}\"", "valid secret key"},
		{"unclosed", "http:\n      path: /\n      headers:\n        Authorization: \"Bearer ${secret:TOKEN\"", "not closed"},
		{"in path", "http:\n      path: /x?t=${secret:TOKEN}", "only in http header values"},
		{"in body", "http:\n      method: POST\n      path: /x\n      body: \"${secret:TOKEN}\"", "only in http header values"},
		{"in mcp args", "mcp:\n      tool: echo\n      arguments: {t: \"${secret:TOKEN}\"}\n      expected_result: ok", "only in http header values"},
		{"in sql", "sql:\n      database: main\n      query: \"select '${secret:TOKEN}'\"\n      expect: {rows: 1}", "only in http header values"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseYAML([]byte("version: 1\nchecks:\n  - name: c\n    " + tt.check + "\n"))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateBoundsDistinctSecretRefs(t *testing.T) {
	var b strings.Builder
	b.WriteString("version: 1\nchecks:\n")
	for i := 0; i <= MaxSecretRefs; i++ {
		fmt.Fprintf(&b, "  - name: c%d\n    http:\n      path: /\n      headers:\n        Authorization: \"${secret:K%d}\"\n", i, i)
	}
	if _, err := ParseYAML([]byte(b.String())); err == nil || !strings.Contains(err.Error(), "reference") {
		t.Fatalf("err = %v, want the secret reference bound", err)
	}
}

func TestExpandSecrets(t *testing.T) {
	got, err := ExpandSecrets("Bearer ${secret:A}.${secret:B}", map[string]string{"A": "x", "B": "y"})
	if err != nil || got != "Bearer x.y" {
		t.Fatalf("ExpandSecrets = %q, %v", got, err)
	}
	if got, err := ExpandSecrets("plain", nil); err != nil || got != "plain" {
		t.Fatalf("plain value = %q, %v", got, err)
	}
	_, err = ExpandSecrets("Bearer ${secret:A}", map[string]string{})
	var missing *MissingSecretError
	if !errors.As(err, &missing) || missing.Name != "A" {
		t.Fatalf("err = %v, want missing A", err)
	}
}
