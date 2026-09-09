// Package drills owns the rendering contract for the M8 restore-drill
// record. The bash script at deploy/scripts/faas-m8-restore-drill.sh
// produces a Markdown file using a heredoc-extended block; this package
// reads the same template via //go:embed so the Go test can catch
// template drift that bash -n alone cannot.
//
// The render helpers mirror the bash heredoc field set one-for-one; we
// intentionally do not embed the actual run output (the script owns the
// live measurement collection). The shape contract is the load-bearing
// thing: a run that drops a required token breaks the spec §14 M8 audit
// trail, and the test is the only place we can assert it without a real
// EX44 + PG + systemd.
package drills

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed testdata/TEMPLATE-restore-drill.md
var templateMarkdown string

// RequiredTokens is the list of row labels the drill record MUST
// contain. The bash script emits each of these via a literal table cell
// in docs/drills/<UTC-date>-<HHMMSS>-restore-drill.md; the Go test
// iterates this list to assert the template (and by extension the script)
// still contains every one. The slice MUST match the row labels emitted
// by RenderRecord exactly — TestRecord_RenderProducesLabels wires the
// two together so a drift in either direction fails the build.
var RequiredTokens = []string{
	"Date (UTC)",
	"Operator",
	"Box",
	"Started",
	"Finished",
	"Wall-clock total",
	"RPO via basebackup",
	"RPO via WAL",
	"Wake latency",
	"Basebackup used",
	"Basebackup SHA-256",
	"Recovery stanza status",
	"host.age SHA-256 (preserved)",
	"Verdict",
	"Operator / commit",
}

// RecoveryProbeScope prevents restore-drill evidence from being confused with
// the SSD platform snapshot-restore SLO. The drill probe crosses the public
// edge and executes the application; the <350 ms gate measures only the
// internal wake.boot_started.at → wake.boot_completed.at interval.
const RecoveryProbeScope = "end-to-end recovery probe; outside the <350 ms platform snapshot-restore SLO"

// Metrics is the seven-field drill summary block the bash script populates
// from live measurements. The bash heredoc and RenderRecord must agree
// field-for-field; the test locks this contract.
type Metrics struct {
	Date           string // YYYY-MM-DD (UTC)
	Operator       string // $USER at run time
	Box            string // `hostname -f` output
	Started        string // ISO-8601 UTC
	Finished       string // ISO-8601 UTC
	TotalMin       int    // wall-clock minutes
	TotalSec       int    // wall-clock seconds (0-59)
	RPOBaseMin     int    // basebackup age at drill start, minutes
	RPOBaseSec     int    // basebackup age at drill start, seconds
	RPOWALMin      int    // newest archived WAL age, minutes (0 if no WAL)
	RPOWALSec      int    // newest archived WAL age, seconds
	WakeLatency    string // seconds (printed as-is)
	BasebackupPath string // /var/lib/pgsql/basebackup/basebackup-<UTC>
	BaseSHA        string // sha256 of base.tar.gz (or "-" if missing)
	RecoveryStatus string // "promoted at <ts>" or "failed-to-promote: ..."
	HostAgeSHA     string // sha256 of host.age at backup time
	Verdict        string // "PASS" or "FAIL"
	Commit         string // git rev-parse HEAD or "no-git"
}

// RenderRecord produces the markdown table row set mirroring the bash
// heredoc. We emit one row per field, with the literal column header
// `Field | Value` matching the template at docs/drills/TEMPLATE-restore-drill.md.
//
// The bash script writes the full document (heading + acceptance bar +
// table + step log + follow-ups); this function writes ONLY the table
// rows. The test asserts both the shape (via RequiredTokens against the
// embedded template) and that RenderRecord includes each label literally.
func RenderRecord(m Metrics) string {
	var b strings.Builder
	row := func(field, value string) {
		fmt.Fprintf(&b, "| %s | %s |\n", field, value)
	}
	row("Date (UTC)", m.Date)
	row("Operator", m.Operator)
	row("Box", m.Box)
	row("Started", m.Started)
	row("Finished", m.Finished)
	row("Wall-clock total", fmt.Sprintf("%d min %d s", m.TotalMin, m.TotalSec))
	row("RPO via basebackup", fmt.Sprintf("%d min %d s", m.RPOBaseMin, m.RPOBaseSec))
	row("RPO via WAL", fmt.Sprintf("%d min %d s", m.RPOWALMin, m.RPOWALSec))
	row("Wake latency", m.WakeLatency+"s ("+RecoveryProbeScope+")")
	row("Basebackup used", m.BasebackupPath)
	row("Basebackup SHA-256", m.BaseSHA)
	row("Recovery stanza status", m.RecoveryStatus)
	row("host.age SHA-256 (preserved)", m.HostAgeSHA)
	row("Verdict", "**"+m.Verdict+"** (bar = 30 min)")
	row("Operator / commit", m.Commit)
	return b.String()
}

// TemplateMarkdown returns the embedded template content. Exposed for the
// test so the assertion and the embed live in the same file.
func TemplateMarkdown() string { return templateMarkdown }
