package templates

import (
	"encoding/json"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/sharedruntime"
)

// platformInternals are component names a customer never runs or sees.
var platformInternals = regexp.MustCompile(`\b(apid|guest-init|vmmd|schedd|gatewayd|jailer)\b`)

// TestHelloTemplatesSpeakToCustomers pins the first-run copy of the hello
// starters. Their README is read inside a directory that start or init has
// already created, so it leads with `gregale start` there. No file names a
// platform component. cmd/gregale's template_readme_test.go pins the rest of
// the README contract.
func TestHelloTemplatesSpeakToCustomers(t *testing.T) {
	for _, name := range []string{"hello-node", "hello-python", "hello-go"} {
		t.Run(name, func(t *testing.T) {
			readme, err := fs.ReadFile(FS, name+"/README.md")
			if err != nil {
				t.Fatal(err)
			}
			text := string(readme)
			if !strings.Contains(text, "From this directory:\n\n```\ngregale start\n```") {
				t.Errorf("README must lead with gregale start from this directory:\n%s", text)
			}
			err = fs.WalkDir(FS, name, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				data, err := fs.ReadFile(FS, path)
				if err != nil {
					return err
				}
				if match := platformInternals.Find(data); match != nil {
					t.Errorf("%s names platform internal %q", path, match)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The hello starters' READMEs match the platform's defaults: the Node
// starter named Node.js 22 while engines.node ">=22" runs on Node.js 24
// (#6629), and "Try it" opened a URL that answers 401 on paid plans and
// read edge health as proof the handler ran (#6628, #6630).
func TestHelloTemplatesMatchRuntimeAndAccessDefaults(t *testing.T) {
	readme, err := fs.ReadFile(FS, "hello-node/README.md")
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := fs.ReadFile(FS, "hello-node/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if err := json.Unmarshal(pkg, &manifest); err != nil {
		t.Fatal(err)
	}
	runtime := sharedruntime.NodeRuntimeForSpec(manifest.Engines.Node)
	major := strings.TrimPrefix(runtime, "node")
	if runtime == "" || !strings.Contains(string(readme), "runs the app on Node.js "+major) {
		t.Errorf("README must name Node.js %s, the runtime engines.node %q selects:\n%s", major, manifest.Engines.Node, readme)
	}
	for _, name := range []string{"hello-node", "hello-python", "hello-go"} {
		readme, err := fs.ReadFile(FS, name+"/README.md")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"gregale invoke <slug> --method GET --path /", "Authorization: Bearer <key>", "401 unauthorized", `"source":"edge"`, "--path /healthz"} {
			if !strings.Contains(string(readme), want) {
				t.Errorf("%s README lacks %q", name, want)
			}
		}
	}
}
