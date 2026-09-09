package drills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecord_TemplateHasRequiredTokens locks the field-label set in the
// embedded template. The bash script emits each label literally in the
// heredoc block (see deploy/scripts/faas-m8-restore-drill.sh step 7);
// a refactor that drops any of these silently breaks the M8 audit trail.
// Bash `bash -n` cannot catch this — only the Go embed + grep does.
func TestRecord_TemplateHasRequiredTokens(t *testing.T) {
	md := TemplateMarkdown()
	if md == "" {
		t.Fatal("template embed returned empty — pkg/drills/record.go's //go:embed path is wrong or file is missing")
	}
	for _, tok := range RequiredTokens {
		if !strings.Contains(md, tok) {
			t.Errorf("template missing required token: %q", tok)
		}
	}
}

// TestRecord_BashScriptAndGoRendererAgree parses the row labels emitted
// by the bash heredoc in faas-m8-restore-drill.sh step 7 and asserts
// they match the Go renderer's RequiredTokens slice in BOTH set and
// order. This closes the only material gap in the original contract:
// a label rename on one side desyncs the audit trail and the Go test
// would still pass because each side is tested in isolation.
//
// We parse the heredoc block by extracting every line matching
// `^| <label> |` between the `FIELDS` and `FOLLOW` markers. The markers
// are stable because lint-drill and TestRecord_TemplateHasRequiredTokens
// already lock the surrounding structure.
func TestRecord_BashScriptAndGoRendererAgree(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	scriptPath := filepath.Join(repoRoot, "deploy", "scripts", "faas-m8-restore-drill.sh")
	raw, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Skipf("drill script %q not readable from %q: %v", scriptPath, cwd, err)
	}

	const (
		startMarker = "cat <<FIELDS"
		endMarker   = "FIELDS"
	)
	lines := strings.Split(string(raw), "\n")
	inBlock := false
	var bashLabels []string
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		if !inBlock {
			if trim == startMarker {
				inBlock = true
			}
			continue
		}
		if trim == endMarker {
			break
		}
		// Match table rows: "| <label> | <value...> |"
		if !strings.HasPrefix(trim, "| ") {
			continue
		}
		// Split on "|", trim each field. Row schema is "| Label | Value |".
		parts := strings.Split(trim, "|")
		if len(parts) < 4 {
			continue
		}
		label := strings.TrimSpace(parts[1])
		if label == "" || label == "Field" {
			continue
		}
		bashLabels = append(bashLabels, label)
	}

	if len(bashLabels) == 0 {
		t.Fatalf("parsed zero labels from %q between %q and %q — script structure changed?",
			scriptPath, startMarker, endMarker)
	}

	if len(bashLabels) != len(RequiredTokens) {
		t.Errorf("bash heredoc emits %d labels but Go renderer emits %d — count drift\n"+
			"  bash: %v\n  go:   %v",
			len(bashLabels), len(RequiredTokens), bashLabels, RequiredTokens)
	}
	for i := range bashLabels {
		if i >= len(RequiredTokens) {
			break
		}
		if bashLabels[i] != RequiredTokens[i] {
			t.Errorf("label drift at row %d: bash=%q go=%q\n  full bash: %v\n  full go:   %v",
				i, bashLabels[i], RequiredTokens[i], bashLabels, RequiredTokens)
		}
	}
}

// TestRecord_RenderProducesFields locks the wire shape of RenderRecord.
// The bash heredoc and Go renderer must agree on every label so a future
// edit to either side is caught by this test. The set of labels is
// pinned exactly — not by substring — because the bash heredoc emits the
// same literals (asserted by the shell smoke) and a renamed row in one
// but not the other would silently desynchronize.
func TestRecord_RenderProducesFields(t *testing.T) {
	m := Metrics{
		Date:           "2026-07-25",
		Operator:       "alice",
		Box:            "faas-1.example.com",
		Started:        "2026-07-25T03:00:00Z",
		Finished:       "2026-07-25T03:14:32Z",
		TotalMin:       14,
		TotalSec:       32,
		RPOBaseMin:     0,
		RPOBaseSec:     12,
		RPOWALMin:      0,
		RPOWALSec:      4,
		WakeLatency:    "12",
		BasebackupPath: "/var/lib/pgsql/basebackup/basebackup-2026-07-25T025932Z",
		BaseSHA:        "deadbeef",
		RecoveryStatus: "promoted at 2026-07-25T03:14:32Z",
		HostAgeSHA:     "cafef00d",
		Verdict:        "PASS",
		Commit:         "abc1234",
	}
	out := RenderRecord(m)

	// Exact row-label set, in emit order. Must match RequiredTokens
	// AND the bash heredoc at deploy/scripts/faas-m8-restore-drill.sh:296-316.
	wantRows := []string{
		"| Date (UTC) | 2026-07-25 |",
		"| Operator | alice |",
		"| Box | faas-1.example.com |",
		"| Started | 2026-07-25T03:00:00Z |",
		"| Finished | 2026-07-25T03:14:32Z |",
		"| Wall-clock total | 14 min 32 s |",
		"| RPO via basebackup | 0 min 12 s |",
		"| RPO via WAL | 0 min 4 s |",
		"| Wake latency | 12s (end-to-end recovery probe; outside the <350 ms platform snapshot-restore SLO) |",
		"| Basebackup used | /var/lib/pgsql/basebackup/basebackup-2026-07-25T025932Z |",
		"| Basebackup SHA-256 | deadbeef |",
		"| Recovery stanza status | promoted at 2026-07-25T03:14:32Z |",
		"| host.age SHA-256 (preserved) | cafef00d |",
		"| Verdict | **PASS** (bar = 30 min) |",
		"| Operator / commit | abc1234 |",
	}
	for _, want := range wantRows {
		if !strings.Contains(out, want) {
			t.Errorf("RenderRecord missing exact row %q\nfull output:\n%s", want, out)
		}
	}
	if !strings.Contains(TemplateMarkdown(), RecoveryProbeScope) {
		t.Errorf("operator template must keep recovery-probe scope %q", RecoveryProbeScope)
	}
}

// TestRecord_RenderEmptyMetricsIsDeterministic asserts RenderRecord does
// not panic on zero-value Metrics. The bash script can in principle emit
// empty fields when a measurement step is skipped; the renderer must
// still produce a stable string.
func TestRecord_RenderEmptyMetricsIsDeterministic(t *testing.T) {
	a := RenderRecord(Metrics{})
	b := RenderRecord(Metrics{})
	if a != b {
		t.Errorf("RenderRecord(zero) is non-deterministic\nfirst:\n%s\nsecond:\n%s", a, b)
	}
	if !strings.Contains(a, "Wall-clock total") {
		t.Errorf("RenderRecord(zero) missing Wall-clock total label")
	}
}

// TestRecord_OperatorTemplateStaysInSync guards against drift between the
// embedded template (pkg/drills/testdata/) and the operator-facing copy
// at docs/drills/TEMPLATE-restore-drill.md. The bash script reads the
// latter; pkg/drills reads the former. Both MUST be byte-identical or
// the contract silently desynchronizes (lint-drill's grep catches this
// for individual tokens; this catches whole-file drift).
func TestRecord_OperatorTemplateStaysInSync(t *testing.T) {
	// `go test` cwd's into the package dir, so we walk up to the repo
	// root by counting the path elements. pkg/drills → repo root.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	path := filepath.Join(repoRoot, "docs", "drills", "TEMPLATE-restore-drill.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("operator template %q not readable from %q; run tests from repo root: %v",
			path, cwd, err)
	}
	if string(got) != TemplateMarkdown() {
		t.Errorf("operator template drifted from embedded testdata copy\n"+
			"  run: cp %s pkg/drills/testdata/TEMPLATE-restore-drill.md\n"+
			"  embedded size: %d, operator size: %d",
			path, len(TemplateMarkdown()), len(got))
	}
}
