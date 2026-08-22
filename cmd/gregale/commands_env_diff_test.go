// commands_env_diff_test.go — unit tests for
// renderEnvDiffTable + renderEnvDiffCell (ADR-117 PR-C).
//
// The CLI handler (envDiff) is exercised in the e2e suite
// where a real server is up. The pure renderer is testable
// in isolation here — no client, no server, no auth.

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderEnvDiffCell_Missing(t *testing.T) {
	cell := api.EnvDiffCell{Present: false}
	got := renderEnvDiffCell(api.EnvDiffKindSecret, cell, map[string]api.EnvDiffCell{"prod": cell}, []string{"prod"})
	if got != "-" {
		t.Errorf("missing cell = %q, want \"-\"", got)
	}
}

func TestRenderEnvDiffCell_SecretSingleScope(t *testing.T) {
	// A secret stamped in only ONE scope — no peers to
	// compare against. Render as "==" (vacuously; the
	// row has only one stamped scope).
	cell := api.EnvDiffCell{Present: true, ValueHash: "abcdef0123456789"}
	got := renderEnvDiffCell(api.EnvDiffKindSecret, cell,
		map[string]api.EnvDiffCell{"prod": cell},
		[]string{"prod"})
	if got != "==" {
		t.Errorf("single-scope secret = %q, want \"==\"", got)
	}
}

func TestRenderEnvDiffCell_SecretAllSame(t *testing.T) {
	// Three scopes, all with the same value_hash. Every
	// cell renders as "==".
	hash := "abcdef0123456789"
	cells := map[string]api.EnvDiffCell{
		"prod":    {Present: true, ValueHash: hash},
		"staging": {Present: true, ValueHash: hash},
		"dev":     {Present: true, ValueHash: hash},
	}
	for sc, cell := range cells {
		got := renderEnvDiffCell(api.EnvDiffKindSecret, cell, cells, []string{"dev", "prod", "staging"})
		if got != "==" {
			t.Errorf("scope %s all-same = %q, want \"==\"", sc, got)
		}
	}
}

func TestRenderEnvDiffCell_SecretDiffer(t *testing.T) {
	// Two scopes, different value_hashes. Both render as
	// "≠" (the "different" indicator). Security: NO
	// plaintext leaks — both are sealed envelopes, the
	// renderer only knows equality.
	cells := map[string]api.EnvDiffCell{
		"prod":    {Present: true, ValueHash: "aaaa1111aaaa1111"},
		"staging": {Present: true, ValueHash: "bbbb2222bbbb2222"},
	}
	prod := renderEnvDiffCell(api.EnvDiffKindSecret, cells["prod"], cells, []string{"prod", "staging"})
	staging := renderEnvDiffCell(api.EnvDiffKindSecret, cells["staging"], cells, []string{"prod", "staging"})
	if prod != "≠" {
		t.Errorf("prod cell = %q, want \"≠\"", prod)
	}
	if staging != "≠" {
		t.Errorf("staging cell = %q, want \"≠\"", staging)
	}
	// Security: NEVER a value field.
	if strings.Contains(prod, "aaaa") || strings.Contains(staging, "bbbb") {
		t.Errorf("secret cell leaked value_hash fragment: prod=%q staging=%q", prod, staging)
	}
}

func TestRenderEnvDiffCell_SecretPrePRCRow(t *testing.T) {
	// Pre-PR-C row: value_hash = '' (NULL in PG, COALESCE
	// surfaces ''). Renderer treats absent value_hash as
	// "unknown" → "-" (not "==" — better to render unknown
	// than to assert a possibly-false equality).
	cell := api.EnvDiffCell{Present: true, ValueHash: ""}
	got := renderEnvDiffCell(api.EnvDiffKindSecret, cell,
		map[string]api.EnvDiffCell{"prod": cell},
		[]string{"prod"})
	if got != "-" {
		t.Errorf("pre-PR-C secret cell = %q, want \"-\" (unknown)", got)
	}
}

func TestRenderEnvDiffCell_EnvAllSame(t *testing.T) {
	// Env cells expose the value (env is public). When
	// all peers match, render as "==" (consistent with
	// the secret-cell rendering for the same equality).
	cells := map[string]api.EnvDiffCell{
		"prod":    {Present: true, Value: "info"},
		"staging": {Present: true, Value: "info"},
	}
	for sc, cell := range cells {
		got := renderEnvDiffCell(api.EnvDiffKindEnv, cell, cells, []string{"prod", "staging"})
		if got != "==" {
			t.Errorf("env all-same %s = %q, want \"==\"", sc, got)
		}
	}
}

func TestRenderEnvDiffCell_EnvDiffer(t *testing.T) {
	// Env cells with different values: render the literal
	// value (the customer wants to see WHICH value is
	// different, not just "≠"). Mirror of the secret-cell
	// "≠" — env is public, so the literal is fine.
	cells := map[string]api.EnvDiffCell{
		"prod":    {Present: true, Value: "info"},
		"staging": {Present: true, Value: "debug"},
	}
	prod := renderEnvDiffCell(api.EnvDiffKindEnv, cells["prod"], cells, []string{"prod", "staging"})
	staging := renderEnvDiffCell(api.EnvDiffKindEnv, cells["staging"], cells, []string{"prod", "staging"})
	if prod != "info" {
		t.Errorf("prod env cell = %q, want \"info\"", prod)
	}
	if staging != "debug" {
		t.Errorf("staging env cell = %q, want \"debug\"", staging)
	}
}

func TestRenderEnvDiffTable_Empty(t *testing.T) {
	resp := &api.EnvDiffResponse{
		AppSlug:     "empty-app",
		Scopes:      []string{},
		Rows:        []api.EnvDiffRow{},
		GeneratedAt: time.Now(),
	}
	var buf bytes.Buffer
	renderEnvDiffTable(&buf, "empty-app", resp)
	if !strings.Contains(buf.String(), "no env vars or secrets") {
		t.Errorf("empty matrix output: %q, want header containing 'no env vars or secrets'", buf.String())
	}
}

func TestRenderEnvDiffTable_FullMatrix(t *testing.T) {
	// Two scopes (prod, staging), three rows: STRIPE_KEY
	// (secret, same hash), DATABASE_URL (secret, different
	// hashes), LOG_LEVEL (env, different values).
	resp := &api.EnvDiffResponse{
		AppSlug: "demo",
		Scopes:  []string{"prod", "staging"},
		Rows: []api.EnvDiffRow{
			{
				Key:  "DATABASE_URL",
				Kind: api.EnvDiffKindSecret,
				Cells: map[string]api.EnvDiffCell{
					"prod":    {Present: true, ValueHash: "aaaa1111aaaa1111"},
					"staging": {Present: true, ValueHash: "bbbb2222bbbb2222"},
				},
			},
			{
				Key:  "LOG_LEVEL",
				Kind: api.EnvDiffKindEnv,
				Cells: map[string]api.EnvDiffCell{
					"prod":    {Present: true, Value: "info"},
					"staging": {Present: true, Value: "debug"},
				},
			},
			{
				Key:  "STRIPE_KEY",
				Kind: api.EnvDiffKindSecret,
				Cells: map[string]api.EnvDiffCell{
					"prod":    {Present: true, ValueHash: "abcdef0123456789"},
					"staging": {Present: true, ValueHash: "abcdef0123456789"},
				},
			},
		},
		GeneratedAt: time.Now(),
	}
	var buf bytes.Buffer
	renderEnvDiffTable(&buf, "demo", resp)
	out := buf.String()
	// Sanity: header is present, all three keys are listed.
	for _, want := range []string{"KEY", "KIND", "prod", "staging", "DATABASE_URL", "LOG_LEVEL", "STRIPE_KEY"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q in:\n%s", want, out)
		}
	}
	// Secret cells: DATABASE_URL rows show "≠", STRIPE_KEY
	// rows show "==".
	databaseRow := extractRow(out, "DATABASE_URL")
	if !strings.Contains(databaseRow, "≠") {
		t.Errorf("DATABASE_URL row missing \"≠\" in:\n%s", databaseRow)
	}
	stripeRow := extractRow(out, "STRIPE_KEY")
	if !strings.Contains(stripeRow, "==") {
		t.Errorf("STRIPE_KEY row missing \"==\" in:\n%s", stripeRow)
	}
	// Env cells: LOG_LEVEL row shows the literal values.
	logRow := extractRow(out, "LOG_LEVEL")
	if !strings.Contains(logRow, "info") || !strings.Contains(logRow, "debug") {
		t.Errorf("LOG_LEVEL row missing literal env values in:\n%s", logRow)
	}
	// Security: STRIPE_KEY row must NOT carry any value_hash
	// fragment (the renderer only shows "==" or "≠", never
	// the actual hash).
	if strings.Contains(stripeRow, "abcdef") {
		t.Errorf("STRIPE_KEY row leaked value_hash fragment: %q", stripeRow)
	}
}

// extractRow pulls one row's text from the table by matching
// the leading KEY column. Helps assertions read the row
// without depending on tabwriter's whitespace.
func extractRow(table, key string) string {
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, key) {
			return line
		}
	}
	return ""
}
