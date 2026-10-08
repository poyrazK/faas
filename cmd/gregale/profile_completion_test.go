package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestConnectionProfileShellCompletion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			executable, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s unavailable: %v", shell, err)
			}
			dir := t.TempDir()
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
			for _, name := range []string{"default", "staging"} {
				path := filepath.Join(dir, name+".json")
				data := fmt.Sprintf(`{"apps":[{"slug":%q}]}`, name+"-app")
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PROFILE_"+strings.ToUpper(name)+"_CACHE", path)
			}
			mock := `#!/bin/sh
if [ "$1" = --profile ]; then
  [ "$2" = staging ] || exit 1
  shift 2
  cache="$PROFILE_STAGING_CACHE"
else
  cache="$PROFILE_DEFAULT_CACHE"
fi
case "$*" in
  'completion profile-names') printf 'default\nstaging\n' ;;
  'completion completion-cache-path') printf '%s\n' "$cache" ;;
  *) exit 1 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "gregale"), []byte(mock), 0o700); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			render := map[string]func() int{"bash": cmdCompletionBash, "zsh": cmdCompletionZsh, "fish": cmdCompletionFish, "pwsh": cmdCompletionPowershell}[shell]
			captureStdoutSwap(t, &buf, render)
			scriptPath := filepath.Join(dir, "completion.ps1")
			if err := os.WriteFile(scriptPath, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			cases := []struct {
				line, want string
				forbidden  bool
			}{
				{"gregale --profile sta", "staging", false},
				{"gregale --profile=sta", "--profile=staging", false},
				{"gregale profile use sta", "staging", false},
				{"gregale --profile staging profile remove sta", "staging", false},
				{"gregale --profile staging app staging", "staging-app", false},
				{"gregale app demo scale --profile sta", "staging", true},
			}
			for _, tc := range cases {
				t.Run(tc.line, func(t *testing.T) {
					words := strings.Fields(tc.line)
					var script string
					switch shell {
					case "bash":
						script = "source '" + scriptPath + "'\nCOMP_WORDS=(" + tc.line + ")\nCOMP_CWORD=" + strconv.Itoa(len(words)-1) + "\n__gregale\nprintf '%s\\n' \"${COMPREPLY[@]}\"\n"
					case "zsh":
						// Exercise routing and cache lookup with the compsys output hooks replaced;
						// compadd itself requires an interactive ZLE completion context.
						prefix := words[len(words)-1]
						script = "compdef() { :; }; compadd() { shift; for candidate in \"$@\"; do [[ \"$candidate\" == \"$PREFIX\"* ]] && print -r -- \"${IPREFIX}${candidate}\"; done; return 0; }; compset() { IPREFIX=--profile=; PREFIX=${PREFIX#--profile=}; }; _values() { shift; compadd -- \"$@\"; }; _arguments() { :; }; _describe() { :; }; _files() { :; }\nsource '" + scriptPath + "'\nwords=(" + tc.line + ")\nCURRENT=" + strconv.Itoa(len(words)) + "\nPREFIX='" + prefix + "'; IPREFIX=''\n_gregale\n"
					case "fish":
						script = "source '" + scriptPath + "'\ncomplete -C '" + tc.line + "' | string replace -r '\\t.*$' ''\ntrue\n"
					case "pwsh":
						script = "$ErrorActionPreference='Stop'\n. '" + scriptPath + "'\n(TabExpansion2 '" + tc.line + "' " + strconv.Itoa(len(tc.line)) + ").CompletionMatches | ForEach-Object { $_.CompletionText }\n"
					}
					var cmd *exec.Cmd
					if shell == "pwsh" {
						path := filepath.Join(dir, "invoke.ps1")
						if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
							t.Fatal(err)
						}
						cmd = exec.Command(executable, "-NoLogo", "-NoProfile", "-File", path)
					} else {
						cmd = exec.Command(executable)
						cmd.Stdin = strings.NewReader(script)
					}
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("shell failed: %v\n%s", err, out)
					}
					found := false
					for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
						if line == tc.want {
							found = true
						}
					}
					if found == tc.forbidden {
						t.Fatalf("want completion %q present=%t; output:\n%s", tc.want, !tc.forbidden, out)
					}
				})
			}
		})
	}
}
