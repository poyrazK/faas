package secretscan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShippedFunctionTemplatesAreNotSecrets(t *testing.T) {
	for _, template := range []string{"function-node", "function-node24", "function-python", "function-python313", "function-go"} {
		t.Run(template, func(t *testing.T) {
			root := filepath.Join("..", "..", "cmd", "gregale", "templates", template)
			err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				if IsTextFile(p, b) {
					for _, f := range ScanFile(p, b) {
						t.Errorf("template %s:%d flagged as %s", filepath.Base(p), f.Line, f.Provider)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSourceEntropyScansCredentialTokens(t *testing.T) {
	const secret = "A7qDm9PzR3uK8vN2xL5cW0sY6hF4eB1j"
	for _, line := range []string{
		`const token = "` + secret + `";`,
		`console.log("` + secret + `");`,
		`{"token": "` + secret + `"}`,
		`token: ` + secret,
		secret,
	} {
		got := ScanFile("source.js", []byte(line))
		if len(got) != 1 || got[0].Provider != "high_entropy" {
			t.Errorf("credential literal not detected: findings=%+v", got)
		}
	}
	for _, line := range []string{
		`// Mirrors cmd/gregale/templates/function-node/handler.js (node22).`,
		`ctx.log.info("function invoked", { event, invocation_id: ctx.invocation_id, runtime: process.env.FAAS_RUNTIME });`,
		`const result = JSON.stringify({ invocation_id: ctx.invocation_id });`,
	} {
		if got := ScanFile("handler.js", []byte(line)); len(got) != 0 {
			t.Errorf("code flagged: %+v", got)
		}
	}
}
