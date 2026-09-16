// Command subprocessor-md generates docs/compliance/subprocessors.md
// from docs/compliance/subprocessors.json. Mirrors the
// cmd/denylist-md shape: a single main.go with a pure render()
// function, no maps iterated without sort, no timestamps in the
// rendered output.
//
// Wired into the Makefile's `subprocessor-md` target and run as
// part of `spec-check`, so a stale catalog edit is caught at
// `git diff --exit-code docs/compliance/subprocessors.md` time.
//
// The renderer also enforces the 30-day notice window (DPA §7):
// any sub-processor entry whose `notice_published_at` is younger
// than 30 days before `effective_date` causes subprocessor-check
// to exit 1 with a clear error message. The notice window is a
// load-bearing legal invariant — silently passing the gate would
// let the operator deploy a sub-processor addition before the
// Controller's 30-day objection window has elapsed.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

const (
	subprocessorJSONPath = "docs/compliance/subprocessors.json"
	subprocessorMDPath   = "docs/compliance/subprocessors.md"
)

// subProcessor is the on-disk shape of one entry in
// docs/compliance/subprocessors.json. Fields are kept in
// alphabetical order (id is the only required field at the type
// level — JSON has no required-but-not-required distinction;
// subprocessor-check validates the rest).
type subProcessor struct {
	ID                string   `json:"id"`
	Category          string   `json:"category"`
	Vendor            string   `json:"vendor"`
	Service           string   `json:"service"`
	DataCategories    []string `json:"data_categories"`
	DataRegion        string   `json:"data_region"`
	Encryption        string   `json:"encryption"`
	RetentionDays     *int     `json:"retention_days"` // nullable
	DPASigned         bool     `json:"dpa_signed"`
	DPAReference      string   `json:"dpa_reference"`
	OperatorSwitchEnv *string  `json:"operator_switch_env"` // nullable
	Rationale         string   `json:"rationale"`
	NoticePublishedAt *string  `json:"notice_published_at"` // date when the 30-day notice was first published at docs.gregale.dev/dpa/subprocessors
	EffectiveDate     *string  `json:"effective_date"`      // date when the sub-processor starts processing customer data; must be ≥ 30 days after notice_published_at
}

type catalog struct {
	Version          string         `json:"version"`
	EffectiveDate    string         `json:"effective_date"`
	NoticeWindowDays int            `json:"notice_window_days"`
	ControllerPath   string         `json:"controller_artifact_path"`
	RenderedPath     string         `json:"rendered_artifact_path"`
	Generator        string         `json:"generator"`
	DPAReference     string         `json:"dpa_reference"`
	SubProcessors    []subProcessor `json:"sub_processors"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "subprocessor-md:", err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := os.ReadFile(subprocessorJSONPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", subprocessorJSONPath, err)
	}
	var cat catalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return fmt.Errorf("unmarshal %s: %w", subprocessorJSONPath, err)
	}
	if err := validate(cat); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	if _, err := os.Stdout.WriteString(render(cat)); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

// validate enforces the load-bearing invariants: every sub-processor
// has a unique id, every entry with an effective_date carries a
// notice_published_at that is at least NoticeWindowDays older, and
// every entry with a DPA claim has dpa_signed=true.
func validate(cat catalog) error {
	if cat.NoticeWindowDays != 30 {
		return fmt.Errorf("notice_window_days must be 30 (DPA §7); got %d", cat.NoticeWindowDays)
	}
	seen := make(map[string]bool, len(cat.SubProcessors))
	for i, sp := range cat.SubProcessors {
		if sp.ID == "" {
			return fmt.Errorf("sub_processors[%d].id is empty", i)
		}
		if seen[sp.ID] {
			return fmt.Errorf("duplicate sub-processor id: %q", sp.ID)
		}
		seen[sp.ID] = true
		if sp.DPASigned && sp.DPAReference == "" {
			return fmt.Errorf("sub-processor %q has dpa_signed=true but empty dpa_reference", sp.ID)
		}
		// 30-day notice window check (DPA §7).
		if sp.EffectiveDate != nil && sp.NoticePublishedAt == nil {
			return fmt.Errorf("sub-processor %q has effective_date but no notice_published_at", sp.ID)
		}
		if sp.EffectiveDate != nil {
			notice, err := time.Parse("2006-01-02", *sp.NoticePublishedAt)
			if err != nil {
				return fmt.Errorf("sub-processor %q notice_published_at parse: %w", sp.ID, err)
			}
			effective, err := time.Parse("2006-01-02", *sp.EffectiveDate)
			if err != nil {
				return fmt.Errorf("sub-processor %q effective_date parse: %w", sp.ID, err)
			}
			elapsed := effective.Sub(notice)
			required := time.Duration(cat.NoticeWindowDays) * 24 * time.Hour
			if elapsed < required {
				return fmt.Errorf("sub-processor %q: notice window not satisfied — notice_published_at=%s, effective_date=%s, elapsed=%s, required=%s",
					sp.ID, notice.Format("2006-01-02"), effective.Format("2006-01-02"), elapsed.Round(24*time.Hour), required)
			}
		}
	}
	return nil
}

// render emits the full markdown document. Pure function: no
// timestamps, no maps iterated without sort, no template strings.
// The renderer's output is what `git diff --exit-code` checks for
// drift against the hand-curated file.
func render(cat catalog) string {
	var b []byte
	b = append(b, []byte("# Sub-processors\n\n")...)
	b = append(b, []byte("<!-- GENERATED — do not edit by hand; regenerate with `make subprocessor-md`. -->\n\n")...)
	b = append(b, []byte("Single source of truth: [`docs/compliance/subprocessors.json`](subprocessors.json).\n\n")...)
	b = append(b, []byte(fmt.Sprintf("> **Notice window:** Processor shall notify Controller at least\n> **%d days** before adding a new sub-processor. Controller may\n> object on reasonable data-protection grounds; the parties shall\n> work in good faith to resolve the objection before the change\n> takes effect (%s). The %d-day window is enforced by the\n> `subprocessor-check` CI gate (PR-3): every new sub-processor\n> entry must carry a `notice_published_at` timestamp that is at\n> least %d days older than `effective_date` before the operator\n> can deploy the change.\n\n",
		cat.NoticeWindowDays, cat.DPAReference, cat.NoticeWindowDays, cat.NoticeWindowDays))...)

	// Header table.
	b = append(b, []byte("## Current sub-processors\n\n")...)
	b = append(b, []byte("| Category | Vendor | Service | Data categories | Data region | DPA reference | Operator switch |\n")...)
	b = append(b, []byte("|---|---|---|---|---|---|---|\n")...)
	rows := append([]subProcessor{}, cat.SubProcessors...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	for _, sp := range rows {
		switchStr := "—"
		if sp.OperatorSwitchEnv != nil {
			switchStr = *sp.OperatorSwitchEnv
		}
		dataCats := "["
		for i, c := range sp.DataCategories {
			if i > 0 {
				dataCats += ", "
			}
			dataCats += c
		}
		dataCats += "]"
		b = append(b, []byte(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | `%s` |\n",
			sp.Category, sp.Vendor, sp.Service, dataCats, sp.DataRegion, sp.DPAReference, switchStr))...)
	}
	b = append(b, '\n')

	// On-boarding checklist (static — mirrors docs/compliance/subprocessors.md top section).
	b = append(b, []byte("## Sub-processor on-boarding checklist\n\n")...)
	b = append(b, []byte("When adding a new sub-processor:\n\n")...)
	b = append(b, []byte("1. **Update `subprocessors.json`** — append a new object with all required fields (`id`, `category`, `vendor`, `service`, `data_categories`, `data_region`, `encryption`, `retention_days`, `dpa_signed`, `dpa_reference`, `operator_switch_env`, `rationale`, `notice_published_at`, `effective_date`). The `id` must be a stable kebab-case slug; never reuse a removed entry's id (use `subprocessor-archive.json`).\n")...)
	b = append(b, []byte("2. **Set `notice_published_at`** — the date the operator first publishes the 30-day notice at `https://gregale.dev/docs/dpa/subprocessors`. This timestamp must be **≥ 30 days older** than the planned `effective_date`. The `subprocessor-check` CI gate fails the build if this invariant is violated.\n")...)
	b = append(b, []byte("3. **Regenerate `subprocessors.md`** — run `make subprocessor-md`. Hand-edits to the markdown file are caught at `git diff --exit-code docs/compliance/subprocessors.md` time (same pattern as `spec-check`).\n")...)
	b = append(b, []byte("4. **Update DPA §7** — add a bullet to `docs/DPA.md` §7 listing the new sub-processor. The DPA is the executed contract; the JSON is the rendering source for the public notice.\n")...)
	b = append(b, []byte("5. **Update vendor assessment** — if the new sub-processor is critical-tier (database, billing, identity-provider), write a one-file assessment under `docs/compliance/vendor-assessments/` (PR-10).\n")...)
	b = append(b, []byte("6. **Update sub-processor archive** — never delete an entry from `subprocessors.json`. Mark it for removal (set `effective_until` + `removal_reason`), keep the entry visible until `effective_until` elapses, then move it to `subprocessor-archive.json`.\n\n")...)

	b = append(b, []byte("## Removed sub-processors\n\n")...)
	b = append(b, []byte("See [`subprocessor-archive.json`](subprocessor-archive.json).\n\n")...)

	// Cross-references (static).
	b = append(b, []byte("## Cross-references\n\n")...)
	b = append(b, []byte("- `docs/DPA.md` §7 (executed DPA — the contract).\n")...)
	b = append(b, []byte("- `docs/faas_implementation_spec.md` §11 (security hardening checklist).\n")...)
	b = append(b, []byte("- `docs/compliance/soc2-control-mapping.md` CC6.6 / CC9.1 / CC9.2.\n")...)
	b = append(b, []byte("- `docs/compliance/iso27001-statement-of-applicability.md` A.5.19 / A.5.20 / A.5.21 / A.5.22.\n")...)
	b = append(b, []byte("- `cmd/subprocessor-md` (CI gate generator).\n")...)
	b = append(b, []byte("- `pkg/netns` and `pkg/oci` (network egress enforcement — see `cmd/denylist-md` for the network-side mirror of this PR).\n")...)

	return string(b)
}
