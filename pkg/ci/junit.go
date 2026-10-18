package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var errJUnitInvalid = errors.New("invalid JUnit report")
var errJUnitLimit = errors.New("JUnit report limit exceeded")

type junitFrame struct{ kind, suite string }
type junitCase struct {
	name, class, suite, message, problemType string
	detail                                   strings.Builder
	failed, errored, skipped, truncated      bool
}
type junitParser struct {
	report            TestReport
	frames            []junitFrame
	current           *junitCase
	namespace         string
	root, declaration bool
}

// Parse only already verified, bounded artifact bytes. encoding/xml has no
// resolver, custom entities or CharsetReader. Directives and non-declaration
// processing instructions are rejected instead of being interpreted.
func parseJUnit(ctx context.Context, raw []byte, ref ArtifactRef) (TestReport, error) {
	if err := ctx.Err(); err != nil {
		return TestReport{}, err
	}
	base := TestReport{ArtifactRef: ref, Status: ReportInvalid}
	if len(raw) > api.CIJUnitMaxBytes {
		base.Status = ReportLimit
		return base, nil
	}
	p := junitParser{report: TestReport{ArtifactRef: ref, Status: ReportParsed}}
	decoder := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})))
	for tokens := 0; ; tokens++ {
		if err := ctx.Err(); err != nil {
			return TestReport{}, err
		}
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if !p.root || len(p.frames) != 0 || p.current != nil {
				return base, nil
			}
			return boundTestReport(p.report), nil
		}
		if err != nil {
			return base, nil //nolint:nilerr // Malformed XML is a diagnostic; native receipts determine job outcome.
		}
		if tokens >= api.CIJUnitMaxTokens {
			base.Status = ReportLimit
			return base, nil
		}
		if err := p.token(token); err != nil {
			if errors.Is(err, errJUnitLimit) {
				base.Status = ReportLimit
			}
			return base, nil
		}
	}
}

func (p *junitParser) token(token xml.Token) error {
	switch value := token.(type) {
	case xml.StartElement:
		return p.start(value)
	case xml.EndElement:
		if len(p.frames) == 0 {
			return errJUnitInvalid
		}
		frame := p.frames[len(p.frames)-1]
		p.frames = p.frames[:len(p.frames)-1]
		if frame.kind == "case" {
			p.finishCase()
		}
	case xml.CharData:
		if len(p.frames) == 0 {
			if strings.TrimSpace(string(value)) != "" {
				return errJUnitInvalid
			}
		} else if frame := p.frames[len(p.frames)-1]; (frame.kind == "failure" || frame.kind == "failure_text") && p.current != nil {
			text, clipped := clipReportText(string(value), api.CIJUnitDetailMaxBytes-p.current.detail.Len())
			p.current.detail.WriteString(text)
			p.current.truncated = p.current.truncated || clipped
		}
	case xml.Directive:
		return errJUnitInvalid
	case xml.ProcInst:
		if value.Target != "xml" || p.root || p.declaration {
			return errJUnitInvalid
		}
		p.declaration = true
	}
	return nil
}

func (p *junitParser) start(value xml.StartElement) error {
	if len(p.frames) >= api.CIJUnitMaxDepth || len(value.Attr) > api.CIJUnitMaxAttributes {
		return errJUnitLimit
	}
	attrs := make(map[xml.Name]bool, len(value.Attr))
	for _, attr := range value.Attr {
		if attrs[attr.Name] {
			return errJUnitInvalid
		}
		attrs[attr.Name] = true
	}
	frame := junitFrame{kind: "ignored"}
	if len(p.frames) == 0 {
		if p.root || value.Name.Local != "testsuite" && value.Name.Local != "testsuites" {
			return errJUnitInvalid
		}
		p.root, p.namespace = true, value.Name.Space
		frame.kind = "container"
		if value.Name.Local == "testsuite" {
			frame = p.suiteFrame(value)
		}
	} else {
		parent := p.frames[len(p.frames)-1]
		switch {
		case parent.kind == "failure" || parent.kind == "failure_text":
			frame.kind = "failure_text"
		case value.Name.Space != p.namespace:
		case (parent.kind == "suite" || parent.kind == "container") && value.Name.Local == "testsuite":
			frame = p.suiteFrame(value)
		case (parent.kind == "suite" || parent.kind == "container") && value.Name.Local == "testsuites":
			frame.kind = "container"
		case parent.kind == "suite" && value.Name.Local == "testcase":
			if p.current != nil {
				return errJUnitInvalid
			}
			if p.report.Tests >= api.CIJUnitMaxCases {
				return errJUnitLimit
			}
			p.current = &junitCase{name: junitAttribute(value, "name"), class: junitAttribute(value, "classname"), suite: parent.suite}
			frame.kind = "case"
		case parent.kind == "case" && (value.Name.Local == "failure" || value.Name.Local == "error" || value.Name.Local == "skipped"):
			if err := p.problem(value); err != nil {
				return err
			}
			if value.Name.Local != "skipped" {
				frame.kind = "failure"
			}
		}
	}
	p.frames = append(p.frames, frame)
	return nil
}

func (p *junitParser) suiteFrame(value xml.StartElement) junitFrame {
	name, truncated := clipReportText(junitAttribute(value, "name"), api.CIJUnitNameMaxBytes)
	p.report.Truncated = p.report.Truncated || truncated
	return junitFrame{kind: "suite", suite: name}
}

func junitAttribute(value xml.StartElement, name string) string {
	for _, attr := range value.Attr {
		if attr.Name.Local == name && attr.Name.Space == "" {
			return attr.Value
		}
	}
	return ""
}

func (p *junitParser) problem(value xml.StartElement) error {
	c := p.current
	if c == nil {
		return errJUnitInvalid
	}
	if value.Name.Local == "skipped" {
		if c.failed || c.errored {
			return errJUnitInvalid
		}
		c.skipped = true
		return nil
	}
	if c.skipped {
		return errJUnitInvalid
	}
	if !c.failed && !c.errored {
		c.message = junitAttribute(value, "message")
		c.problemType = junitAttribute(value, "type")
	}
	c.failed = c.failed || value.Name.Local == "failure"
	c.errored = c.errored || value.Name.Local == "error"
	return nil
}

func (p *junitParser) finishCase() {
	c := p.current
	p.current = nil
	p.report.Tests++
	switch {
	case c.errored:
		p.report.Errors++
	case c.failed:
		p.report.Failed++
	case c.skipped:
		p.report.Skipped++
	default:
		p.report.Passed++
	}
	if !c.failed && !c.errored {
		return
	}
	if len(p.report.Failures) >= api.CIJUnitMaxFailures {
		p.report.Truncated = true
		return
	}
	f := TestFailure{Index: p.report.Tests, Suite: c.suite, Kind: "failure", Detail: c.detail.String()}
	if c.errored {
		f.Kind = "error"
	}
	if c.name == "" {
		c.name = "(unnamed)"
	}
	for _, field := range []struct {
		dst   *string
		src   string
		bound int
	}{
		{&f.Name, c.name, api.CIJUnitNameMaxBytes}, {&f.Class, c.class, api.CIJUnitNameMaxBytes},
		{&f.Type, c.problemType, api.CIJUnitNameMaxBytes}, {&f.Message, c.message, api.CIJUnitMessageMaxBytes},
	} {
		value, truncated := clipReportText(field.src, field.bound)
		*field.dst = value
		c.truncated = c.truncated || truncated
	}
	p.report.Truncated = p.report.Truncated || c.truncated
	p.report.Failures = append(p.report.Failures, f)
}

func boundTestReport(report TestReport) TestReport {
	for len(report.Failures) > 0 {
		raw, _ := json.Marshal(report)
		if len(raw) <= api.CIJUnitSnapshotMaxBytes {
			break
		}
		report.Failures = report.Failures[:len(report.Failures)-1]
		report.Truncated = true
	}
	return report
}
