package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const junitExample = `<testsuites tests="999" failures="999"><testsuite name="private-suite" tests="999"><properties><property><testcase/></property></properties><testcase name="pass"/><testcase name="private-name" classname="private-class"><failure type="assertion" message="private-message">private-detail &amp; evidence</failure></testcase><testsuite name="nested"><testcase name="skip"><skipped/></testcase><testcase name="error"><failure/><error message="secondary"/></testcase></testsuite><system-out>ignored output</system-out></testsuite></testsuites>`

func junitReport(t *testing.T, raw string) TestReport {
	t.Helper()
	ref := ArtifactRef{Path: "reports/unit.xml", Digest: jobsDigest([]byte(raw)), Bytes: int64(len(raw))}
	report, err := parseJUnit(t.Context(), []byte(raw), ref)
	if err != nil || report.ArtifactRef != ref || validateReportData(report) != nil {
		t.Fatalf("invalid report projection: %+v %v", report, err)
	}
	return report
}

func TestCIJUnitParsesActualCases(t *testing.T) {
	r := junitReport(t, junitExample)
	if r.Status != ReportParsed || r.TestCounts != (TestCounts{Tests: 4, Passed: 1, Failed: 1, Errors: 1, Skipped: 1}) || len(r.Failures) != 2 {
		t.Fatalf("counts trusted aggregate attributes or ignored content: %+v", r)
	}
	if f := r.Failures[0]; f.Index != 2 || f.Name != "private-name" || f.Suite != "private-suite" || f.Detail != "private-detail & evidence" || f.Kind != "failure" {
		t.Fatalf("failure identity lost: %+v", f)
	}
	if f := r.Failures[1]; f.Index != 4 || f.Suite != "nested" || f.Kind != "error" {
		t.Fatalf("nested error identity lost: %+v", f)
	}
	for _, raw := range []string{
		"\xef\xbb\xbf" + `<?xml version="1.0" encoding="UTF-8"?><testsuite><testcase/></testsuite>`,
		`<j:testsuite xmlns:j="urn:junit"><j:testcase/><j:testcase><j:failure>before <b>nested</b> after</j:failure></j:testcase></j:testsuite>`,
		`<testsuite><system-out><testcase/></system-out><testcase/><other:testcase xmlns:other="urn:other"/></testsuite>`,
	} {
		r := junitReport(t, raw)
		if r.Status != ReportParsed || r.Passed != 1 {
			t.Fatalf("common JUnit dialect rejected: %+v", r)
		}
	}
}

func TestCIJUnitRejectsAmbiguousXMLAndBoundsWork(t *testing.T) {
	attributes := strings.Builder{}
	for i := 0; i <= api.CIJUnitMaxAttributes; i++ {
		fmt.Fprintf(&attributes, ` a%d="x"`, i)
	}
	cases := []struct{ name, raw, status string }{
		{"empty", "", ReportInvalid},
		{"malformed", `<testsuite><testcase/></testsuites>`, ReportInvalid},
		{"partial_counts", `<testsuite><testcase/><testcase>`, ReportInvalid},
		{"two_roots", `<testsuite/><testsuite/>`, ReportInvalid},
		{"wrong_root", `<testcase/>`, ReportInvalid},
		{"trailing", `<testsuite/>text`, ReportInvalid},
		{"duplicate_attribute", `<testsuite name="one" name="two"/>`, ReportInvalid},
		{"conflicting_result", `<testsuite><testcase><skipped/><failure/></testcase></testsuite>`, ReportInvalid},
		{"directive", `<!DOCTYPE testsuite SYSTEM "https://example.invalid/entity"><testsuite/>`, ReportInvalid},
		{"external_entity", `<!DOCTYPE testsuite [<!ENTITY x SYSTEM "file:///etc/passwd">]><testsuite>&x;</testsuite>`, ReportInvalid},
		{"entity_expansion", `<!DOCTYPE testsuite [<!ENTITY a "aa"><!ENTITY b "&a;&a;">]><testsuite>&b;</testsuite>`, ReportInvalid},
		{"undefined_entity", `<testsuite>&unknown;</testsuite>`, ReportInvalid},
		{"instruction", `<?fetch https://example.invalid?><testsuite/>`, ReportInvalid},
		{"encoding", `<?xml version="1.0" encoding="ISO-8859-1"?><testsuite/>`, ReportInvalid},
		{"invalid_utf8", "<testsuite>\xff</testsuite>", ReportInvalid},
		{"depth", strings.Repeat("<testsuite>", api.CIJUnitMaxDepth+1) + strings.Repeat("</testsuite>", api.CIJUnitMaxDepth+1), ReportLimit},
		{"attributes", "<testsuite" + attributes.String() + "/>", ReportLimit},
		{"cases", "<testsuite>" + strings.Repeat("<testcase/>", api.CIJUnitMaxCases+1) + "</testsuite>", ReportLimit},
		{"tokens", "<testsuite>" + strings.Repeat("<!--x-->", api.CIJUnitMaxTokens) + "</testsuite>", ReportLimit},
		{"bytes", strings.Repeat(" ", api.CIJUnitMaxBytes+1), ReportLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := junitReport(t, tc.raw)
			if r.Status != tc.status || r.TestCounts != (TestCounts{}) || len(r.Failures) != 0 {
				t.Fatalf("untrusted or partial counts escaped: %+v", r)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := parseJUnit(ctx, []byte(junitExample), ArtifactRef{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled parsing continued")
	}
}

func TestCIJUnitBoundsFailureDetailsAndJSON(t *testing.T) {
	// HTML escaping grows JSON substantially; Unicode clipping must preserve UTF-8.
	text := strings.Repeat("界&lt;&amp;", api.CIJUnitDetailMaxBytes)
	entry := `<testcase name="` + text + `" classname="` + text + `"><failure message="` + text + `">` + text + `</failure></testcase>`
	r := junitReport(t, "<testsuite>"+strings.Repeat(entry, api.CIJUnitMaxFailures+1)+"</testsuite>")
	if r.Status != ReportParsed || r.Failed != api.CIJUnitMaxFailures+1 || !r.Truncated || len(r.Failures) >= r.Failed {
		t.Fatalf("failure retention was unbounded: %+v", r.TestCounts)
	}
	raw, _ := json.Marshal(r)
	if len(raw) > api.CIJUnitSnapshotMaxBytes || !utf8.Valid(raw) {
		t.Fatal("report snapshot exceeded byte or UTF-8 bounds")
	}
	cleaned, truncated := clipReportText("a\x00\x1b\u202eb\n\t界", 8)
	if cleaned != "ab\n\t界" || !truncated {
		t.Fatalf("unsafe terminal/bidi characters retained: %q", cleaned)
	}
	clipped, _ := clipReportText("界界", 5)
	if clipped != "界" {
		t.Fatal("clipping split a UTF-8 rune")
	}
}

func FuzzCIJUnitBoundedProjection(f *testing.F) {
	for _, raw := range []string{junitExample, `<testsuite/>`, `<!DOCTYPE bad><testsuite/>`, `<testsuite><testcase><error/></testcase></testsuite>`} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > api.CIJUnitMaxBytes+1 {
			t.Skip()
		}
		r, err := parseJUnit(t.Context(), raw, ArtifactRef{Path: "reports/unit.xml", Digest: jobsDigest(raw), Bytes: int64(len(raw))})
		if err != nil || validateReportData(r) != nil {
			t.Fatalf("parser produced an invalid projection: %v", err)
		}
	})
}
