package deploydiff

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// RenderText writes the [Diff] to w as a human-readable table.
// Style borrowed from cmd/gregale/commands_decompose.go::printPlanText
// — sectioned Fprintf with stable column widths so a customer
// scanning the output can parse "memory 512MB → 256MB" without a
// ruler. Per [pr-829-paddle-renumber-198-200] in memory, the
// diff's text shape MUST stay byte-stable across deploys so
// `gregale deploy --diff | grep` works in CI.
//
// Output shape:
//
//	Deployment diff for "api"
//
//	Memory       512 MB  →  256 MB
//	Concurrency  10      →  30
//	Environment  +STRIPE_KEY
//	Routes       +POST /payments
//
//	⚠ POST /users response schema changed (handler change)
//	⚠ This would create a new deployment row, not patch the existing one
//
//	Fix before deploy.
//
// Sections are emitted in this order:
//  1. App-level scalars (memory, concurrency, idle, …)
//  2. Per-scope env vars (one row per added/removed key)
//  3. Crons (one row per add/remove/modify)
//  4. Edge rules (one row per add/remove/modify)
//  5. Breaks (errors first, then warnings)
//
// All `fmt.Fprintf` / `fmt.Fprintln` return values are discarded
// with `_, _ = ...` per the repo-wide convention (see
// cmd/gregale/commands_registry.go, commands_sign_keys.go). When
// the writer is `os.Stdout` or a `bytes.Buffer`, a short-write is
// not actionable for the diff UX, so the errcheck noise would
// obscure the renderer's actual logic.
func RenderText(w io.Writer, d Diff) {
	if d.Slug != "" {
		_, _ = fmt.Fprintf(w, "Deployment diff for %q\n\n", d.Slug)
	}

	// 1. App-level scalars. Pick the ones that render nicely:
	// memory, concurrency, min_instances, idle_timeout_s,
	// streaming_enabled, websocket_enabled, require_authn,
	// warm_snapshot_enabled, require_signed, eviction_priority,
	// autoscale_target_rps, autoscale_target_cpu_pct,
	// egress_allowlist, app_protocol.
	//
	// Other fields (e.g. require_signed, scaling_policy) land in
	// the "Other" group below — the renderer is intentionally
	// narrow so the customer's first scan reads as the headline
	// UX.
	hasScalars := false
	for _, c := range d.Changes {
		if isHeadlineScalar(c.Field) {
			hasScalars = true
			break
		}
	}
	if hasScalars {
		_, _ = fmt.Fprintln(w, "App config:")
		for _, c := range d.Changes {
			if !isHeadlineScalar(c.Field) {
				continue
			}
			printScalar(w, c)
		}
		_, _ = fmt.Fprintln(w)
	}

	// 2. Per-scope env vars.
	envRows := filterChanges(d.Changes, "environment.")
	if len(envRows) > 0 {
		_, _ = fmt.Fprintln(w, "Environment:")
		for _, c := range envRows {
			switch c.Kind {
			case ChangeAdd:
				_, _ = fmt.Fprintf(w, "  + %s\n", c.Field)
			case ChangeRemove:
				_, _ = fmt.Fprintf(w, "  - %s\n", c.Field)
			default:
				_, _ = fmt.Fprintf(w, "  ~ %s\n", c.Field)
			}
		}
		_, _ = fmt.Fprintln(w)
	}

	// 3. Crons.
	cronRows := filterChanges(d.Changes, "cron[")
	if len(cronRows) > 0 {
		_, _ = fmt.Fprintln(w, "Crons:")
		for _, c := range cronRows {
			switch c.Kind {
			case ChangeAdd:
				_, _ = fmt.Fprintf(w, "  + %s\n", c.Field)
			case ChangeRemove:
				_, _ = fmt.Fprintf(w, "  - %s\n", c.Field)
			default:
				_, _ = fmt.Fprintf(w, "  ~ %s\n", c.Field)
			}
		}
		_, _ = fmt.Fprintln(w)
	}

	// 4. Edge rules.
	erRows := filterChanges(d.Changes, "edge_rule[")
	if len(erRows) > 0 {
		_, _ = fmt.Fprintln(w, "Routes / edge rules:")
		for _, c := range erRows {
			switch c.Kind {
			case ChangeAdd:
				_, _ = fmt.Fprintf(w, "  + %s\n", c.Field)
			case ChangeRemove:
				_, _ = fmt.Fprintf(w, "  - %s\n", c.Field)
			default:
				_, _ = fmt.Fprintf(w, "  ~ %s\n", c.Field)
			}
		}
		_, _ = fmt.Fprintln(w)
	}

	// 5. Other app-config scalars (the long tail that didn't make
	// the headline).
	otherRows := otherScalars(d.Changes)
	if len(otherRows) > 0 {
		_, _ = fmt.Fprintln(w, "Other settings:")
		for _, c := range otherRows {
			printScalar(w, c)
		}
		_, _ = fmt.Fprintln(w)
	}

	// 6. Breaks: errors first (gate-firing), then warnings.
	errors, warns := splitBreaksBySeverity(d.Breaks)
	if len(errors) > 0 {
		_, _ = fmt.Fprintln(w, "Plan-quota breaks (gate fires):")
		for _, b := range errors {
			printBreak(w, b)
		}
		_, _ = fmt.Fprintln(w)
	}
	if len(warns) > 0 {
		_, _ = fmt.Fprintln(w, "Warnings:")
		for _, b := range warns {
			printBreak(w, b)
		}
		_, _ = fmt.Fprintln(w)
	}

	// Final gate line. Mirrors the "Fix before deploy" UX from the
	// user's example: when there are blocking breaks, render the
	// warning so the customer's eye lands on it.
	if d.HasBlockingBreaks() {
		_, _ = fmt.Fprintln(w, "Fix before deploy.")
	} else if len(d.Changes) > 0 {
		_, _ = fmt.Fprintln(w, "Ready to deploy.")
	} else {
		_, _ = fmt.Fprintln(w, "No changes.")
	}
}

// isHeadlineScalar gates which [Change.Field] values land in the
// top "App config" section. The list is intentionally narrow —
// memory, concurrency, idle_timeout_s, and the boolean plan-gated
// flags. Everything else goes to "Other settings" below.
func isHeadlineScalar(field string) bool {
	switch field {
	case "memory", "concurrency", "idle_timeout_s",
		"streaming_enabled", "websocket_enabled",
		"require_authn", "warm_snapshot_enabled",
		"app_protocol":
		return true
	}
	return false
}

// otherScalars returns the Changes whose Field is app-config but
// isn't a headline scalar — used to populate the "Other settings"
// section. Excludes environment / cron / edge_rule prefixed rows.
func otherScalars(changes []Change) []Change {
	out := []Change{}
	for _, c := range changes {
		if isHeadlineScalar(c.Field) {
			continue
		}
		if strings.HasPrefix(c.Field, "environment.") ||
			strings.HasPrefix(c.Field, "cron[") ||
			strings.HasPrefix(c.Field, "edge_rule[") {
			continue
		}
		out = append(out, c)
	}
	return out
}

// printScalar emits one "FieldName   before → after" line, with the
// field column padded to 14 chars so the → arrow lines up across
// rows. Matches the printPlanText Fprintf style.
func printScalar(w io.Writer, c Change) {
	label := c.Field
	for len(label) < 14 {
		label += " "
	}
	switch c.Kind {
	case ChangeAdd:
		_, _ = fmt.Fprintf(w, "  %s+ %v\n", label, c.After.Value)
	case ChangeRemove:
		_, _ = fmt.Fprintf(w, "  %s- %v\n", label, c.Before.Value)
	default:
		_, _ = fmt.Fprintf(w, "  %s%v → %v\n", label, c.Before.Value, c.After.Value)
	}
}

// printBreak emits one "⚠ code — reason" line. Stable wire shape
// so `gregale deploy --diff | grep code` works in CI.
func printBreak(w io.Writer, b Break) {
	marker := "⚠"
	if b.Severity == SeverityError {
		marker = "✗"
	}
	field := ""
	if b.Field != "" {
		field = " (" + b.Field + ")"
	}
	_, _ = fmt.Fprintf(w, "  %s %s — %s%s\n", marker, b.Code, b.Reason, field)
}

// filterChanges returns the Changes whose Field starts with the
// given prefix. Used to split environment / cron / edge_rule rows.
func filterChanges(changes []Change, prefix string) []Change {
	out := []Change{}
	for _, c := range changes {
		if strings.HasPrefix(c.Field, prefix) {
			out = append(out, c)
		}
	}
	// Stable order: sort by Field ASC, then by Kind (add, modify,
	// remove) so a CI grep sees the same sequence.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return kindOrder(out[i].Kind) < kindOrder(out[j].Kind)
	})
	return out
}

// kindOrder gives add < modify < remove so the renderer always
// prints adds first, then mods, then removes within a section.
func kindOrder(k ChangeKind) int {
	switch k {
	case ChangeAdd:
		return 0
	case ChangeModify:
		return 1
	case ChangeRemove:
		return 2
	}
	return 9
}

// splitBreaksBySeverity returns errors and warns as two slices,
// errors first (stable order by Code ASC).
func splitBreaksBySeverity(breaks []Break) ([]Break, []Break) {
	var errors, warns []Break
	for _, b := range breaks {
		if b.Severity == SeverityError {
			errors = append(errors, b)
		} else {
			warns = append(warns, b)
		}
	}
	sort.SliceStable(errors, func(i, j int) bool { return errors[i].Code < errors[j].Code })
	sort.SliceStable(warns, func(i, j int) bool { return warns[i].Code < warns[j].Code })
	return errors, warns
}
