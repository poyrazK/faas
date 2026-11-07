// Package legal renders Gregale's public legal documents: the Terms of
// Service, the Privacy Policy and the Acceptable Use Policy.
//
// The texts are embedded in the binary, so every host serves the same
// reviewed version and a document changes only through a pull request.
// Facts that identify the operator (legal name, address, governing law,
// effective date) live in legal.json. Until every one of them is set the
// documents are unpublished: Published reports false and apid answers 404,
// so a half-filled template can never reach a customer (the live DPA showed
// raw {{CUSTOMER_NAME}} placeholders for weeks, issue #2644).
package legal

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"
)

//go:embed legal.json templates/*.html
var files embed.FS

// Document names, which are also the public paths without the leading slash.
const (
	Terms         = "terms"
	Privacy       = "privacy"
	AcceptableUse = "acceptable-use"
)

// Documents lists every published document in display order.
var Documents = []string{Terms, Privacy, AcceptableUse}

var titles = map[string]string{
	Terms:         "Terms of Service",
	Privacy:       "Privacy Policy",
	AcceptableUse: "Acceptable Use Policy",
}

// Config holds the operator facts the documents quote.
type Config struct {
	// IndividualOperator is true while a person, not a company, runs the
	// Service. The documents then say so and announce the move to the
	// company being formed. operator_address may be just the city.
	// While it is true the operator name, address and country are optional;
	// without a name the documents speak as "Gregale" with the contact email.
	IndividualOperator bool   `json:"individual_operator"`
	OperatorName       string `json:"operator_name"`
	OperatorAddress    string `json:"operator_address"`
	OperatorCountry    string `json:"operator_country"`
	GoverningLaw       string `json:"governing_law"`
	Courts             string `json:"courts"`
	EffectiveDate      string `json:"effective_date"`
	MinimumAge         int    `json:"minimum_age"`
	ContactEmail       string `json:"contact_email"`
	PrivacyEmail       string `json:"privacy_email"`
	AbuseEmail         string `json:"abuse_email"`
	SecurityEmail      string `json:"security_email"`
	SupportEmail       string `json:"support_email"`
}

// Missing names every required field that is still empty or invalid.
func (c Config) Missing() []string {
	var out []string
	required := []struct{ name, value string }{
		{"governing_law", c.GoverningLaw},
		{"courts", c.Courts},
		{"effective_date", c.EffectiveDate},
		{"contact_email", c.ContactEmail},
		{"privacy_email", c.PrivacyEmail},
		{"abuse_email", c.AbuseEmail},
		{"security_email", c.SecurityEmail},
		{"support_email", c.SupportEmail},
	}
	// A company operator must be named with its registered address. An
	// individual operator may publish under the Gregale name alone.
	if !c.IndividualOperator {
		required = append(required,
			struct{ name, value string }{"operator_name", c.OperatorName},
			struct{ name, value string }{"operator_address", c.OperatorAddress},
			struct{ name, value string }{"operator_country", c.OperatorCountry})
	}
	for _, f := range required {
		if strings.TrimSpace(f.value) == "" {
			out = append(out, f.name)
		}
	}
	if c.EffectiveDate != "" {
		if _, err := time.Parse("2006-01-02", c.EffectiveDate); err != nil {
			out = append(out, "effective_date (want YYYY-MM-DD)")
		}
	}
	if c.MinimumAge < 13 {
		out = append(out, "minimum_age (at least 13)")
	}
	return out
}

// Published reports whether the documents may be served.
func (c Config) Published() bool { return len(c.Missing()) == 0 }

// Version is the effective date; a new date means customers accept again.
func (c Config) Version() string { return c.EffectiveDate }

// Load parses the embedded legal.json.
func Load() (Config, error) {
	raw, err := files.ReadFile("legal.json")
	if err != nil {
		return Config{}, err
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("legal: legal.json: %w", err)
	}
	return c, nil
}

// Title returns a document's display title, or "" for an unknown name.
func Title(doc string) string { return titles[doc] }

type page struct {
	Doc       string
	Title     string
	Nonce     string
	C         Config
	Documents []link
}

type link struct{ Path, Title string }

var tmpl = template.Must(template.New("").Option("missingkey=error").ParseFS(files, "templates/*.html"))

// Render writes one document as a complete HTML page. nonce is the
// request's CSP nonce ("" when the route runs without CSP).
func Render(c Config, doc, nonce string) ([]byte, error) {
	title, ok := titles[doc]
	if !ok {
		return nil, fmt.Errorf("legal: unknown document %q", doc)
	}
	if missing := c.Missing(); len(missing) > 0 {
		return nil, fmt.Errorf("legal: unpublished, legal.json is missing %s", strings.Join(missing, ", "))
	}
	p := page{Doc: doc, Title: title, Nonce: nonce, C: c}
	for _, d := range Documents {
		p.Documents = append(p.Documents, link{Path: "/" + d, Title: titles[d]})
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout.html", p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
