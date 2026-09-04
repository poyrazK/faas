// man_markdown.go — `gregale man --markdown`.
//
// Renders the whole command manifest (cli_meta.go) as one Markdown
// document. The public web app vendors the committed output verbatim
// (faas-web content/docs/cli-reference.md), so the binary stays the
// single source of truth for the reference the way it already is for
// man pages and shell completion. TestMarkdownReferenceFresh keeps
// docs/cli-reference.md in lock-step with the manifest.

package main

import (
	"fmt"
	"html"
	"io"
	"strings"
)

// renderMarkdownReference emits the manifest as Markdown. Headings are
// stable anchors: `## <command>` and `### <command> <sub>`.
func renderMarkdownReference(w io.Writer, cmds []cliCommand) {
	_, _ = fmt.Fprintln(w, "# gregale CLI reference")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Generated from the CLI's command manifest by `gregale man --markdown`. Do not edit by hand.")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "| Command | What it does |")
	_, _ = fmt.Fprintln(w, "|---|---|")
	for _, c := range cmds {
		_, _ = fmt.Fprintf(w, "| [`%s`](#%s) | %s |\n", c.Name, c.Name, mdCell(c.Short))
	}
	for _, c := range cmds {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintf(w, "## %s\n\n", c.Name)
		_, _ = fmt.Fprintf(w, "%s\n\n", mdText(c.Short))
		_, _ = fmt.Fprintf(w, "`%s`\n\n", mdSynopsis(c))
		if len(c.ClosedSet) > 0 && len(c.Positionals) > 0 {
			_, _ = fmt.Fprintf(w, "%s is one of %s.\n\n", mdText(c.Positionals[0]), mdCodeList(c.ClosedSet))
		}
		if len(c.Flags) > 0 {
			mdFlagTable(w, c.Flags)
		}
		for _, s := range c.Subcommands {
			_, _ = fmt.Fprintf(w, "### %s %s\n\n%s\n\n", c.Name, s.Name, mdText(s.Short))
			if len(s.Flags) > 0 {
				mdFlagTable(w, s.Flags)
			}
		}
	}
}

func mdSynopsis(c cliCommand) string {
	parts := []string{"gregale", c.Name}
	if len(c.Subcommands) > 0 {
		parts = append(parts, "[<subcommand>]")
	}
	parts = append(parts, c.Positionals...)
	for _, f := range c.Flags {
		parts = append(parts, mdFlagSyntax(f))
	}
	return strings.Join(parts, " ")
}

func mdFlagTable(w io.Writer, flags []cliFlag) {
	_, _ = fmt.Fprintln(w, "| Flag | Meaning | |")
	_, _ = fmt.Fprintln(w, "|---|---|---|")
	for _, f := range flags {
		extra := ""
		if f.Req {
			extra = "required"
		}
		if len(f.ClosedSet) > 0 {
			if extra != "" {
				extra += "; "
			}
			extra += "one of " + mdCodeList(f.ClosedSet)
		}
		_, _ = fmt.Fprintf(w, "| `%s` | %s | %s |\n", mdFlagLabel(f), mdCell(f.Short), extra)
	}
	_, _ = fmt.Fprintln(w)
}

func mdCodeList(vals []string) string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = "`" + v + "`"
	}
	return strings.Join(out, " · ")
}

func mdFlagSyntax(f cliFlag) string {
	label := "--" + f.Name
	if value := mdFlagValue(f); value != "" {
		label += " <" + value + ">"
	}
	if !f.Req {
		return "[" + label + "]"
	}
	return label
}

func mdFlagLabel(f cliFlag) string {
	label := "--" + f.Name
	if value := mdFlagValue(f); value != "" {
		label += " <" + value + ">"
	}
	return label
}

func mdFlagValue(f cliFlag) string {
	if f.Value != "" {
		return f.Value
	}
	if f.Req || len(f.ClosedSet) > 0 {
		return "value"
	}
	return ""
}

// mdCell keeps a table row on one line, escapes Markdown/HTML text, and
// protects the column separator.
func mdCell(s string) string {
	return strings.ReplaceAll(mdText(s), "|", "\\|")
}

func mdText(s string) string {
	return html.EscapeString(strings.ReplaceAll(s, "\n", " "))
}
