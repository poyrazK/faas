package main_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func jsonLocationStdin(t *testing.T, data []byte) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = previous
		_ = f.Close()
	})
}

func TestJSONInputLocations(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		line, col   int
	}{
		{"invalid literal", "private-body-value", 1, 1},
		{"missing value", "{\n  \"private\": \"hidden-value\",\n  \"n\":\n}", 4, 1},
		{"UTF8 columns", "{\"msg\":\"雪\",\"x\":}", 1, 16},
		{"CRLF", "{\r\n\t\"x\":\r\n}", 3, 1},
		{"truncated object", "{", 1, 2},
		{"truncated CRLF", "{\r\n \"x\": 1,\r\n", 3, 1},
		{"trailing document", "{} {}", 1, 4},
		{"tab counts one character", "{\n\t\"x\": }", 2, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, kind := range []string{"inline", "file", "stdin"} {
				t.Run(kind, func(t *testing.T) {
					argument, source := tc.input, "inline"
					switch kind {
					case "file":
						source = filepath.Join(t.TempDir(), "request.json")
						if err := os.WriteFile(source, []byte(tc.input), 0o600); err != nil {
							t.Fatal(err)
						}
						argument = "@" + source
					case "stdin":
						jsonLocationStdin(t, []byte(tc.input))
						argument, source = "-", "stdin"
					}
					_, err := main.ResolveJSONFlag("--data", argument)
					want := fmt.Sprintf("%s:%d:%d: --data must be valid JSON", source, tc.line, tc.col)
					if err == nil || !strings.Contains(err.Error(), want) {
						t.Fatalf("error=%v, want %q", err, want)
					}
					for _, private := range []string{"private-body-value", "hidden-value", "invalid character", "looking for"} {
						if strings.Contains(err.Error(), private) {
							t.Fatal("diagnostic exposed payload content or parser excerpts")
						}
					}
				})
			}
		})
	}
}

func TestJSONInputValidBytesPreserved(t *testing.T) {
	for _, input := range []string{"", " \t\r\n", " \r\n{\"n\":9007199254740993,\"text\":\"雪🙂\",\"huge\":1e10000}\r\n", "[true,null,1]", `"text"`, "42"} {
		for _, kind := range []string{"inline", "file", "stdin"} {
			t.Run(kind+fmt.Sprint(len(input)), func(t *testing.T) {
				argument := input
				switch kind {
				case "file":
					path := filepath.Join(t.TempDir(), "valid.json")
					if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
						t.Fatal(err)
					}
					argument = "@" + path
				case "stdin":
					jsonLocationStdin(t, []byte(input))
					argument = "-"
				}
				got, err := main.ResolveJSONFlag("--config", argument)
				if err != nil || !bytes.Equal(got, []byte(input)) {
					t.Fatalf("valid input changed: err=%v", err)
				}
			})
		}
	}
}

func TestJSONInputFilenameControlsEscaped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request\n\x1b.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := main.ResolveJSONFlag("--payload", "@"+path)
	if err == nil || strings.ContainsAny(err.Error(), "\n\x1b") || !strings.Contains(err.Error(), `\n\x1b.json`) {
		t.Fatalf("filename controls were not escaped: %v", err)
	}
}
