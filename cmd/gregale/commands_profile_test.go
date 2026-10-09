package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func setupConnectionProfiles(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_API", "")
	t.Setenv("FAAS_JSON", "")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", "")
	old := profileOverride
	profileOverride = ""
	t.Cleanup(func() { profileOverride = old })
	setFakeKeyring(t)
	if err := saveCLIConfig(cliConfig{APIBase: "https://default.example", Profiles: map[string]connectionProfile{"staging": {APIBase: "https://staging.example"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionProfileSwitchingAndLegacyCredentials(t *testing.T) {
	setupConnectionProfiles(t)
	original := "fp_live_" + strings.Repeat("a", 48)
	staging := "fp_live_" + strings.Repeat("b", 48)
	if err := saveToken(original); err != nil {
		t.Fatal(err)
	}
	defaultPath, _ := tokenPath()
	defaultCache := cachePathForScripts()
	profileOverride = "staging"
	if got := loadToken(); got != "" {
		t.Fatalf("staging inherited default token: %q", got)
	}
	if err := saveToken(staging); err != nil {
		t.Fatal(err)
	}
	if got := loadToken(); got != staging {
		t.Fatal("staging credential missing")
	}
	stagingPath, _ := tokenPath()
	if stagingPath == defaultPath || cachePathForScripts() == defaultCache {
		t.Fatal("profile stores are shared")
	}
	profileOverride = ""
	if code := cmdProfile([]string{"use", "staging"}); code != 0 {
		t.Fatalf("use exit=%d", code)
	}
	if got := apiBase(); got != "https://staging.example" {
		t.Fatalf("api=%s", got)
	}
	if got := loadToken(); got != staging {
		t.Fatal("switch did not select staging credential")
	}
	profileOverride = "default"
	if got := loadToken(); got != original {
		t.Fatal("legacy default credential changed")
	}
	if got := apiBase(); got != "https://default.example" {
		t.Fatal(got)
	}
	t.Setenv("FAAS_TOKEN", "override")
	t.Setenv("FAAS_API", "https://override.example")
	if loadToken() != "override" || apiBase() != "https://override.example" {
		t.Fatal("environment precedence changed")
	}
}

func TestConnectionProfileLogoutRevokesOnlySelectedSession(t *testing.T) {
	setupConnectionProfiles(t)
	var revoked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method=%s", r.Method)
		}
		revoked = append(revoked, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	for _, name := range []string{"default", "staging"} {
		profileOverride = name
		token := testAPIKey('a')
		if name == "staging" {
			token = testAPIKey('b')
		}
		if err := saveToken(token); err != nil {
			t.Fatal(err)
		}
		if err := saveManagedSession(token, name+"-key", server.URL); err != nil {
			t.Fatal(err)
		}
	}
	defaultToken := testAPIKey('a')
	if code := cmdLogout(); code != 0 {
		t.Fatalf("logout exit=%d", code)
	}
	if len(revoked) != 1 || !strings.HasSuffix(revoked[0], "/staging-key") {
		t.Fatalf("revocations=%v", revoked)
	}
	if loadToken() != "" {
		t.Fatal("staging token survived logout")
	}
	meta, err := loadManagedSession()
	if err != nil || meta.Managed {
		t.Fatalf("staging metadata survived: %+v %v", meta, err)
	}
	profileOverride = "default"
	meta, err = loadManagedSession()
	if err != nil || !meta.matches(defaultToken) {
		t.Fatalf("default session changed: %+v %v", meta, err)
	}
	if loadToken() != defaultToken {
		t.Fatal("default credential removed")
	}
	path, _ := cliSessionMetadataPath()
	if filepath.Base(filepath.Dir(path)) != "gregale" {
		t.Fatal("legacy metadata path changed")
	}
	if code := cmdLogout(); code != 0 {
		t.Fatalf("default logout exit=%d", code)
	}
	if len(revoked) != 2 || !strings.HasSuffix(revoked[1], "/default-key") {
		t.Fatalf("revocations=%v", revoked)
	}
}

func TestConnectionProfileRemovalClearsOnlySelectedStores(t *testing.T) {
	setupConnectionProfiles(t)
	var paths []string
	for _, name := range []string{"default", "staging"} {
		profileOverride = name
		token := testAPIKey('a')
		if name == "staging" {
			token = testAPIKey('b')
		}
		if err := saveToken(token); err != nil {
			t.Fatal(err)
		}
		if err := saveManagedSession(token, name+"-key", "https://example.com"); err != nil {
			t.Fatal(err)
		}
		path, _ := cliSessionMetadataPath()
		paths = append(paths, path)
	}
	profileOverride = ""
	if code := cmdProfile([]string{"remove", "staging"}); code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(paths[1]); !os.IsNotExist(err) {
		t.Fatalf("removed profile metadata: %v", err)
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("default metadata removed: %v", err)
	}
	if loadToken() == "" {
		t.Fatal("default token removed")
	}
	cfg, err := loadCLIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Profiles["staging"]; ok {
		t.Fatal("profile remains configured")
	}
}

func TestConnectionProfilePrefixDoesNotConsumeCommandFlags(t *testing.T) {
	setupConnectionProfiles(t)
	for _, tc := range []struct {
		args []string
		want string
		rest string
	}{
		{[]string{"--profile", "staging", "apps"}, "staging", "apps"},
		{[]string{"--json", "--profile=staging", "apps"}, "staging", "--json apps"},
		{[]string{"app", "demo", "scale", "--profile", "small"}, "", "app demo scale --profile small"},
		{[]string{"app", "demo", "exec", "--", "--profile", "staging"}, "", "app demo exec -- --profile staging"},
	} {
		profileOverride = ""
		rest, err := extractConnectionProfile(tc.args)
		if err != nil || profileOverride != tc.want || strings.Join(rest, " ") != tc.rest {
			t.Fatalf("args=%v rest=%v profile=%q err=%v", tc.args, rest, profileOverride, err)
		}
	}
}

func TestConnectionProfileBashCompletion(t *testing.T) {
	setupConnectionProfiles(t)
	var buf bytes.Buffer
	captureStdoutSwap(t, &buf, cmdCompletionBash)
	for _, tc := range []struct {
		words string
		index int
		want  string
	}{
		{"gregale --profile sta", 2, "staging"},
		{"gregale --profile=sta", 1, "--profile=staging"},
		{"gregale profile use sta", 3, "staging"},
		{"gregale --profile staging profile remove sta", 5, "staging"},
	} {
		script := buf.String() + "\ngregale() { printf 'default\\nstaging\\n'; }\nCOMP_WORDS=(" + tc.words + ")\nCOMP_CWORD=" + strconv.Itoa(tc.index) + "\n__gregale\nprintf '%s\\n' \"${COMPREPLY[@]}\"\n"
		cmd := exec.Command("bash")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != tc.want {
			t.Fatalf("%s: output=%s err=%v", tc.words, out, err)
		}
	}
}

func TestConnectionProfileFallbackFilesStayIsolated(t *testing.T) {
	setupConnectionProfiles(t)
	setFakeKeyring(t, withSetErr(errors.New("keychain unavailable")))
	defaultToken := testAPIKey('a')
	if err := saveToken(defaultToken); err != nil {
		t.Fatal(err)
	}
	defaultPath, _ := tokenPath()
	profileOverride = "staging"
	if loadToken() != "" {
		t.Fatal("named connection inherited fallback token")
	}
	stagingToken := testAPIKey('b')
	if err := saveToken(stagingToken); err != nil {
		t.Fatal(err)
	}
	path, _ := tokenPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode=%v", info.Mode())
	}
	if loadToken() != stagingToken {
		t.Fatal("named fallback credential missing")
	}
	deleteToken()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("named token not removed: %v", err)
	}
	profileOverride = "default"
	if loadToken() != defaultToken {
		t.Fatal("default fallback credential changed")
	}
	if _, err := os.Stat(defaultPath); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionProfileJSONListDoesNotExposeCredentials(t *testing.T) {
	setupConnectionProfiles(t)
	old := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = old })
	var buf bytes.Buffer
	if code := captureStdoutSwap(t, &buf, func() int { return cmdProfile([]string{"list"}) }); code != 0 {
		t.Fatal(code)
	}
	rows := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(rows) != 2 {
		t.Fatalf("rows=%q", buf.String())
	}
	for i, want := range []string{"default", "staging"} {
		var row struct {
			Name    string `json:"name"`
			APIBase string `json:"api_base"`
			Active  bool   `json:"active"`
		}
		if err := json.Unmarshal([]byte(rows[i]), &row); err != nil {
			t.Fatal(err)
		}
		if row.Name != want || row.APIBase == "" || row.Active != (want == "default") {
			t.Fatalf("row=%+v", row)
		}
	}
	if strings.Contains(buf.String(), "token") {
		t.Fatal("list includes credential fields")
	}
}
