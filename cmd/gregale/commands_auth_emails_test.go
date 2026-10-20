package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestAuthEmailTypesContract keeps the CLI's email types in step with the
// template's emailTypes.
func TestAuthEmailTypesContract(t *testing.T) {
	source, err := os.ReadFile("templates/auth/email.mjs")
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, match := range regexp.MustCompile(`(?m)^  ([a-z_]+): \{ placeholders:`).FindAllStringSubmatch(string(source), -1) {
		types = append(types, match[1])
	}
	if !reflect.DeepEqual(types, (*main.AuthEmailTypes)) {
		t.Fatalf("template email types %v, CLI %v", types, (*main.AuthEmailTypes))
	}
}

func TestAuthEmailsSetPreviewAndReset(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	key := "gaak_" + strings.Repeat("k", 43)
	var seen []string
	old := (*main.AuthAdminHTTPClient)
	(*main.AuthAdminHTTPClient) = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		if r.URL.Host != "shop-auth.gregale.app" || r.Header.Get("Authorization") != "Bearer "+key {
			recorder.WriteHeader(http.StatusUnauthorized)
			return recorder.Result(), nil
		}
		line := r.Method + " " + r.URL.Path
		if r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			if len(raw) > 0 {
				line += " " + string(raw)
			}
		}
		seen = append(seen, line)
		template := map[string]any{"type": "signup", "subject": "Welcome to {{app}}", "body": "Hi {{email}}", "custom": true, "placeholders": []string{"app", "email", "link", "code"}}
		switch {
		case strings.HasSuffix(r.URL.Path, "/preview"):
			main.WriteJSONTest(recorder, map[string]any{"subject": "Welcome to Shop", "text": "Hi person@example.com", "html": "<p>Hi person@example.com</p>", "sent": true})
		case r.URL.Path == "/admin/v1/emails":
			main.WriteJSONTest(recorder, map[string]any{"emails": []any{template}})
		default:
			main.WriteJSONTest(recorder, template)
		}
		return recorder.Result(), nil
	})}
	t.Cleanup(func() { (*main.AuthAdminHTTPClient) = old })
	run := func(args ...string) int {
		oldIn := (*main.OsStdin)
		(*main.OsStdin) = strings.NewReader(key + "\n")
		t.Cleanup(func() { (*main.OsStdin) = oldIn })
		return main.CmdAuth(append(args, "--admin-key-stdin"))
	}
	out := captureAuthStdout(t)
	dir := t.TempDir()
	bodyFile := filepath.Join(dir, "signup.txt")
	if err := os.WriteFile(bodyFile, []byte("Hi {{email}}\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	htmlFile := filepath.Join(dir, "preview.html")
	for _, step := range [][]string{
		{"emails", "set", "shop-auth", "signup", "--subject", "Welcome to {{app}}", "--body-file", bodyFile},
		{"emails", "preview", "shop-auth", "signup", "--send-to", "me@example.com", "--html", htmlFile},
		{"emails", "reset", "shop-auth", "signup"},
		{"emails", "list", "shop-auth"},
	} {
		if code := run(step...); code != 0 {
			t.Fatalf("%v = %d\n%s", step, code, out)
		}
	}
	var patch map[string]string
	if err := json.Unmarshal([]byte(strings.TrimPrefix(seen[0], "PATCH /admin/v1/emails/signup ")), &patch); err != nil || !reflect.DeepEqual(patch, map[string]string{"subject": "Welcome to {{app}}", "body": "Hi {{email}}\r\n"}) {
		t.Fatalf("set sent %q (%v)", seen[0], err)
	}
	want := []string{`POST /admin/v1/emails/signup/preview {"send_to":"me@example.com"}`, "DELETE /admin/v1/emails/signup", "GET /admin/v1/emails"}
	if !reflect.DeepEqual(seen[1:], want) {
		t.Fatalf("requests %q", seen[1:])
	}
	if html, err := os.ReadFile(htmlFile); err != nil || string(html) != "<p>Hi person@example.com</p>" {
		t.Fatalf("html file %q, %v", html, err)
	}
	for _, line := range []string{"Saved the signup email", "Placeholders: {{app}} {{email}} {{link}} {{code}}", "Subject: Welcome to Shop", "Sent the preview to me@example.com", "default wording again", "signup                   custom   Welcome to {{app}}"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("output lacks %q:\n%s", line, out)
		}
	}

	seen = nil
	for name, args := range map[string][]string{
		"an unknown type":              {"emails", "show", "shop-auth", "newsletter"},
		"set without a change":         {"emails", "set", "shop-auth", "signup"},
		"a preview flag on set":        {"emails", "set", "shop-auth", "signup", "--subject", "s", "--send-to", "me@example.com"},
		"two bodies":                   {"emails", "set", "shop-auth", "signup", "--body", "a", "--body-file", bodyFile},
		"a body on stdin with the key": {"emails", "set", "shop-auth", "signup", "--body-file", "-"},
		"a subject on two lines":       {"emails", "set", "shop-auth", "signup", "--subject", "a\nb"},
		"a body over the limit":        {"emails", "set", "shop-auth", "signup", "--body", strings.Repeat("x", 4001)},
		"a type on list":               {"emails", "list", "shop-auth", "signup"},
	} {
		if code := run(args...); code == 0 {
			t.Errorf("%s accepted", name)
		}
	}
	if len(seen) != 0 {
		t.Fatalf("requests sent: %v", seen)
	}
}
