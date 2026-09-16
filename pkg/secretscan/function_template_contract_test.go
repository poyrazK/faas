package secretscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFunctionTemplatesDoNotLogOrEchoRequestEvent(t *testing.T) {
	for _, template := range []string{"function-node", "function-node24", "function-python", "function-python313"} {
		t.Run(template, func(t *testing.T) {
			root := filepath.Join("..", "..", "cmd", "gregale", "templates", template)
			var body []byte
			err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() || filepath.Ext(path) == ".md" || filepath.Base(path) == "package.json" || filepath.Base(path) == "gregale.yaml" {
					return nil
				}
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				body = append(body, b...)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			s := string(body)
			for _, forbidden := range []string{"{ event", "received: event", "\"received\": event"} {
				if strings.Contains(s, forbidden) {
					t.Errorf("template contains unbounded request event reference %q", forbidden)
				}
			}
			if strings.Contains(s, "ctx.log.info(\"function invoked\", { event") {
				t.Error("template logs the complete request event")
			}
		})
	}
}
