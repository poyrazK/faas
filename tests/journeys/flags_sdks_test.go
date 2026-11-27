package journeys

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// flagsProbe is one language's checkout probe: its fixture and how the
// journey puts the repository's flags SDK source into the app.
type flagsProbe struct {
	fixture string
	addSDK  func(t *testing.T, web string)
}

// flagsProbes are the checkout probes by runtime. Each serves the same
// contract (testdata/flags-checkout/web/server.js) from that language's SDK
// source in this repository, so a journey tests the SDK it ships with.
var flagsProbes = map[string]flagsProbe{
	// Node 24 strips the SDK's types: no build step.
	"node": {fixture: "flags-checkout", addSDK: func(t *testing.T, web string) {
		copySDKFile(t, filepath.Join("..", "..", "sdk", "node", "src", "flags.ts"), filepath.Join(web, "gregale-flags.mts"))
	}},
	// flags.py needs only httpx (in the probe's requirements.txt).
	"python": {fixture: "flags-checkout-python", addSDK: func(t *testing.T, web string) {
		copySDKFile(t, filepath.Join("..", "..", "sdk", "python", "faas_sdk", "preview", "flags.py"), filepath.Join(web, "gregale_flags.py"))
	}},
	// The Go SDK module uses only the standard library; the probe's go.mod
	// replaces it with this copy.
	"go": {fixture: "flags-checkout-go", addSDK: func(t *testing.T, web string) {
		copySDKTree(t, filepath.Join("..", "..", "sdk", "go"), filepath.Join(web, "gregale-go"))
	}},
}

func copySDKFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("read flags SDK source: %v", err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", to, err)
	}
}

// copySDKTree copies an SDK module without its tests and test data.
func copySDKTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy SDK %s: %v", from, err)
	}
}

// flagsSDKCustomers is how many verified customers each SDK journey uses.
const flagsSDKCustomers = 4

// TestFlagsSDKs runs the Python and Go flags SDKs through the Node journey's
// contract (TestFlagsAndExperiments) on a deployed Gregale: a project app
// built from the repository's SDK source, verified customers calling it with
// consumer keys, and
//
//   - the SDK serves a variant flag the same way `flag inspect` explains it;
//   - track() accepts a verified customer's conversion and drops nothing;
//   - a kill serves the code's fallback, and unkilling restores the variant.
//
// Experiment counting is server-side and stays with the Node journey. It
// skips when Flags are not released to the account.
func TestFlagsSDKs(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	skipFlagsUnreleased(t, tg)
	for _, runtime := range []string{"python", "go"} {
		t.Run(runtime, func(t *testing.T) {
			t.Parallel()
			j := &flagsJourney{tg: tg, runtime: runtime, count: flagsSDKCustomers, project: tg.slug("flags-" + runtime)}
			j.app = j.project + "-web"
			tg.ownApp(t, j.app)
			t.Cleanup(func() {
				if res := tg.run(t, 2*time.Minute, "project", "delete", j.project, "--yes"); res.exit != 0 {
					t.Logf("warning: cleanup could not delete project %s (%s)", j.project, res.problem())
				}
			})
			s := newStory(t)
			if !s.step("deploy_project", j.deploy) {
				return
			}
			if !s.step("verified_customers", j.makeCustomers) {
				return
			}
			if !s.step("create_variant_flag", j.createFlag) {
				return
			}
			s.check("sdk_matches_inspector", j.sdkMatchesInspector)
			s.check("track_accepted", j.trackAccepted)
			s.check("kill_serves_fallback", j.killRestores)
		})
	}
}

// trackAccepted converts every customer: track() takes the verified
// customer's event and the flush leaves nothing dropped.
func (j *flagsJourney) trackAccepted(t *testing.T) {
	for _, c := range j.customers {
		doc := j.served(t, c, "/checkout?convert=1", func(d map[string]any) bool { return str(d, "reason") == "rule_match" })
		if field(doc, "tracked") != true || num(doc, "dropped") != 0 {
			t.Errorf("customer %s: tracked %v, dropped %v; want tracked and none dropped (%s)", c.tenant, field(doc, "tracked"), num(doc, "dropped"), compact(doc))
		}
	}
}

// killRestores serves the fallback while the flag is killed and the
// configured variant again once it is not.
func (j *flagsJourney) killRestores(t *testing.T) {
	c := j.customers[0]
	j.flag(t, "kill", "set", "checkout")
	j.served(t, c, "/checkout", func(d map[string]any) bool { return str(d, "variant") == "fallback" && str(d, "reason") == "killed" })
	j.flag(t, "kill", "unset", "checkout")
	j.served(t, c, "/checkout", func(d map[string]any) bool { return str(d, "reason") == "rule_match" })
}
