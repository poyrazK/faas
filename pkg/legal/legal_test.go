package legal

import (
	"regexp"
	"strings"
	"testing"
)

func fullConfig(t *testing.T) Config {
	t.Helper()
	c, err := Load()
	if err != nil {
		t.Fatalf("embedded legal.json: %v", err)
	}
	c.OperatorName = "Test Operator"
	c.OperatorAddress = "1 Example Street, Exampletown"
	c.OperatorCountry = "Exampleland"
	c.GoverningLaw = "Exampleland"
	c.Courts = "Exampletown"
	c.EffectiveDate = "2026-12-01"
	return c
}

// leftover matches anything a reader should never see: an unexpanded
// template action, html/template's <no value>, or a DPA-style placeholder.
var leftover = regexp.MustCompile(`\{\{|\}\}|<no value>|\[[A-Z][A-Z _]+\]|ZgotmplZ`)

func TestEmbeddedConfigParses(t *testing.T) {
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}

func TestEveryDocumentRendersCompletely(t *testing.T) {
	c := fullConfig(t)
	if m := c.Missing(); len(m) > 0 {
		t.Fatalf("fixture incomplete: %v", m)
	}
	for _, doc := range Documents {
		body, err := Render(c, doc, "n0nce")
		if err != nil {
			t.Fatalf("%s: %v", doc, err)
		}
		s := string(body)
		if m := leftover.FindString(s); m != "" {
			t.Errorf("%s: leftover placeholder %q", doc, m)
		}
		for _, want := range []string{"<h1>" + Title(doc) + "</h1>", "Test Operator", "Effective 2026-12-01", `nonce="n0nce"`} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q", doc, want)
			}
		}
		if len(s) < 3000 {
			t.Errorf("%s: suspiciously short (%d bytes): body template not included?", doc, len(s))
		}
	}
	terms, _ := Render(c, Terms, "")
	for _, want := range []string{"laws of Exampleland", "courts of Exampletown", "merchant of record"} {
		if !strings.Contains(string(terms), want) {
			t.Errorf("terms: missing %q", want)
		}
	}
}

func TestUnpublishedConfigRefusesToRender(t *testing.T) {
	c := fullConfig(t)
	c.GoverningLaw = " "
	if c.Published() {
		t.Fatal("blank governing_law must leave the documents unpublished")
	}
	if _, err := Render(c, Terms, ""); err == nil {
		t.Fatal("Render must refuse an unpublished config")
	}
	c = fullConfig(t)
	c.EffectiveDate = "1 December 2026"
	if c.Published() {
		t.Fatal("a non-ISO effective_date must leave the documents unpublished")
	}
}

// While a person runs the Service the documents say so and announce the
// move to the company; a company operator gets neither sentence.
func TestIndividualOperatorWording(t *testing.T) {
	for _, individual := range []bool{true, false} {
		c := fullConfig(t)
		c.IndividualOperator = individual
		terms, err := Render(c, Terms, "")
		if err != nil {
			t.Fatal(err)
		}
		privacy, err := Render(c, Privacy, "")
		if err != nil {
			t.Fatal(err)
		}
		for name, got := range map[string]bool{
			"terms names an individual":    strings.Contains(string(terms), "Test Operator, an individual"),
			"terms announces the company":  strings.Contains(string(terms), "Company formation"),
			"privacy announces controller": strings.Contains(string(privacy), "will become the controller"),
		} {
			if got != individual {
				t.Errorf("individual_operator=%v: %s = %v", individual, name, got)
			}
		}
	}
}

// An individual operator may publish under the Gregale name alone; a
// company operator must be named with its address.
func TestBrandOnlyIndividualOperator(t *testing.T) {
	c := fullConfig(t)
	c.IndividualOperator = true
	c.OperatorName, c.OperatorAddress, c.OperatorCountry = "", "", ""
	if m := c.Missing(); len(m) > 0 {
		t.Fatalf("brand-only individual operator should publish, missing %v", m)
	}
	for _, doc := range Documents {
		body, err := Render(c, doc, "")
		if err != nil {
			t.Fatalf("%s: %v", doc, err)
		}
		s := string(body)
		for _, bad := range []string{"operated by", ", ,", " , ", "()", "an individual"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s: brand-only text contains %q", doc, bad)
			}
		}
	}
	terms, _ := Render(c, Terms, "")
	if !strings.Contains(string(terms), `agreement between you and Gregale ("we", "us"), the operator of the Gregale platform`) {
		t.Error("terms: brand-only party wording missing")
	}
	c.IndividualOperator = false
	if c.Published() {
		t.Fatal("a company operator without name and address must stay unpublished")
	}
}

func TestUnknownDocument(t *testing.T) {
	if _, err := Render(fullConfig(t), "cookies", ""); err == nil {
		t.Fatal("unknown document must error")
	}
}

// The operator values are HTML-escaped like any other template data.
func TestOperatorValuesAreEscaped(t *testing.T) {
	c := fullConfig(t)
	c.OperatorName = `<script>x</script>`
	body, err := Render(c, Terms, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "<script>x") {
		t.Fatal("operator name rendered unescaped")
	}
}
