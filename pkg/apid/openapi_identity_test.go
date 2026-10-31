package apid

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPublicOpenAPIIdentity is issue #2704: the served reference (and every
// SDK generated from it) presents Gregale, not the internal predecessor, and
// carries no loopback endpoint or internal license reference.
func TestPublicOpenAPIIdentity(t *testing.T) {
	for name, raw := range map[string][]byte{
		"embedded": OpenAPIYAML(),
		"public":   PublicOpenAPIYAMLFor(allVisible),
	} {
		t.Run(name, func(t *testing.T) {
			var doc struct {
				Info struct {
					Title   string `yaml:"title"`
					Summary string `yaml:"summary"`
					Contact struct {
						Name string `yaml:"name"`
					} `yaml:"contact"`
					License struct {
						Identifier string `yaml:"identifier"`
					} `yaml:"license"`
				} `yaml:"info"`
				Servers []struct {
					Description string `yaml:"description"`
				} `yaml:"servers"`
			}
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("decode spec: %v", err)
			}
			if doc.Info.Title != "Gregale API" || doc.Info.Contact.Name != "Gregale" {
				t.Errorf("info title %q contact %q, want Gregale", doc.Info.Title, doc.Info.Contact.Name)
			}
			if !strings.HasPrefix(doc.Info.License.Identifier, "LicenseRef-Gregale") {
				t.Errorf("license identifier %q, want a Gregale reference", doc.Info.License.Identifier)
			}
			text := strings.ToLower(string(raw))
			for _, banned := range []string{"one-box faas", "onebox-internal", "127.0.0.1:8081", "/v1/webhooks/stripe"} {
				if strings.Contains(text, banned) {
					t.Errorf("spec contains %q", banned)
				}
			}
		})
	}
}

// customerFacingDescriptionRules are what a description or summary in the
// reference must not carry (issue #2704): internal decision records, issue
// and release-step numbers, repository paths, migration files, loopback
// addresses, and internal daemon names. Daemon names inside a code span are
// data values (an audit row's writer, an event name) and are allowed.
var customerFacingDescriptionRules = []struct {
	name     string
	re       *regexp.Regexp
	codeSpan bool // also match inside `code spans`
}{
	{"decision record", regexp.MustCompile(`\bADR[- ]?\d`), true},
	{"issue number", regexp.MustCompile(`\b(?:[Ii]ssues?|PR) #\d`), true},
	{"release step", regexp.MustCompile(`\bPR-[A-Z0-9]+\b`), true},
	{"repository path", regexp.MustCompile(`\b(?:pkg|cmd|guest)/[a-z0-9_]`), true},
	{"migration file", regexp.MustCompile(`\bmigrations/\d{3,}`), true},
	{"loopback address", regexp.MustCompile(`127\.0\.0\.1`), true},
	{"daemon name", regexp.MustCompile(`(?:^|[^\w/.-])(?:gatewayd(?:-public|-internal)?|apid|schedd|vmmd|imaged|meterd|builderd|githubd|realtimed|outboundd|livequeryd|entityd)(?:$|[^\w/.])`), false},
}

var codeSpan = regexp.MustCompile("`[^`]*`")

// TestPublicOpenAPIDescriptionsAreCustomerFacing walks every description and
// summary in the embedded spec, which the SDKs are generated from and which
// the public reference is filtered from.
func TestPublicOpenAPIDescriptionsAreCustomerFacing(t *testing.T) {
	var doc any
	if err := yaml.Unmarshal(OpenAPIYAML(), &doc); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	var walk func(node any, path string)
	walk = func(node any, path string) {
		switch v := node.(type) {
		case map[string]any:
			for key, child := range v {
				if s, ok := child.(string); ok && (key == "description" || key == "summary") {
					checkCustomerFacingText(t, path+"/"+key, s)
					continue
				}
				walk(child, path+"/"+key)
			}
		case []any:
			for i, child := range v {
				walk(child, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(doc, "")
}

func checkCustomerFacingText(t *testing.T, path, text string) {
	t.Helper()
	for _, finding := range customerFacingViolations(text) {
		t.Errorf("%s carries a %s", path, finding)
	}
}

// customerFacingViolations names each rule text breaks, with context.
func customerFacingViolations(text string) []string {
	plain := codeSpan.ReplaceAllString(text, "")
	var out []string
	for _, rule := range customerFacingDescriptionRules {
		subject := plain
		if rule.codeSpan {
			subject = text
		}
		if loc := rule.re.FindStringIndex(subject); loc != nil {
			start, end := max(0, loc[0]-40), min(len(subject), loc[1]+40)
			out = append(out, fmt.Sprintf("%s: %q", rule.name, subject[start:end]))
		}
	}
	return out
}

func TestCustomerFacingViolations(t *testing.T) {
	for text, want := range map[string]string{
		"Per-app SLO (ADR-747).":                          "decision record",
		"Preview environments (issue #961).":              "issue number",
		"Older rows (pre-PR-A) have no value.":            "release step",
		"Validated by pkg/api/dto.go.":                    "repository path",
		"Stamped by migrations/00135.":                    "migration file",
		"Listens on 127.0.0.1:8081.":                      "loopback address",
		"apid applies the plan default.":                  "daemon name",
		"Restart the gateway after schedd recovers.":      "daemon name",
		"The API applies the plan default.":               "",
		"Writer: `apid` for surface, `schedd` for wakes.": "",
		"Scope `deploy/write`; see RFC 7617 §2.":          "",
	} {
		got := customerFacingViolations(text)
		switch {
		case want == "" && len(got) != 0:
			t.Errorf("%q: violations %v, want none", text, got)
		case want != "" && (len(got) == 0 || !strings.HasPrefix(got[0], want)):
			t.Errorf("%q: violations %v, want %s", text, got, want)
		}
	}
}
