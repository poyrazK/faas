package main_test

import (
	"os"
	"path/filepath"
	"testing"
)

// ADR-20261026145832: deploy check is the one pre-deploy check; without
// --project it checks local source as `gregale doctor` does.
func TestDeployCheckWithoutProjectIsDoctor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"demo","scripts":{"start":"node server.js"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2][]string{
		{{"doctor", "--path", dir}, {"deploy", "check", "--path", dir}},
		{{"doctor", dir, "--strict"}, {"deploy", "check", dir, "--strict"}},
	} {
		wantCode, wantOut, wantErr := runCLI(t, pair[0]...)
		gotCode, gotOut, gotErr := runCLI(t, pair[1]...)
		if gotCode != wantCode || gotOut != wantOut || gotErr != wantErr {
			t.Errorf("%v = %d\n%s%s\nwant %v = %d\n%s%s", pair[1], gotCode, gotOut, gotErr, pair[0], wantCode, wantOut, wantErr)
		}
		if wantOut == "" && wantErr == "" {
			t.Errorf("%v printed nothing; the comparison proves nothing", pair[0])
		}
	}
}
