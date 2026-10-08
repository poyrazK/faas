// man_markdown.go — `gregale man --markdown`.
//
// Renders the supplied command manifest as one Markdown document. The public
// caller supplies customerCliCommands so advanced commands are not advertised.
// The public web app vendors the committed output verbatim
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
	for i, c := range cmds {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintf(w, "## %s\n\n", c.Name)
		_, _ = fmt.Fprintf(w, "%s\n\n", mdText(c.Short))
		synopsisSuffix := "\n\n"
		if i == len(cmds)-1 {
			synopsisSuffix = "\n"
		}
		_, _ = fmt.Fprintf(w, "`%s`%s", mdSynopsis(c), synopsisSuffix)
		if len(c.ClosedSet) > 0 && len(c.Positionals) > 0 {
			_, _ = fmt.Fprintf(w, "%s is one of %s.\n\n", mdText(c.Positionals[0]), mdCodeList(c.ClosedSet))
		}
		if len(c.Flags) > 0 {
			mdFlagTable(w, c.Flags)
		}
		if len(c.Examples) > 0 {
			writeMarkdownExamples(w, c.Examples)
		}
		for _, s := range c.Subcommands {
			_, _ = fmt.Fprintf(w, "### %s %s\n\n%s\n\n", c.Name, s.Name, mdText(s.Short))
			if len(s.Positionals) > 0 || len(s.Flags) > 0 || c.SubcommandsAfterPositionals {
				_, _ = fmt.Fprintf(w, "`%s`\n\n", mdSubSynopsis(c, []string{s.Name}, s.Positionals, s.Flags))
			}
			if len(s.Flags) > 0 {
				mdFlagTable(w, s.Flags)
			}
			if len(s.Examples) > 0 {
				writeMarkdownExamples(w, s.Examples)
			}
			for _, child := range s.Subcommands {
				renderMarkdownSubtree(w, c, []string{s.Name}, child, 4)
			}
		}
	}
}

func renderMarkdownSubtree(w io.Writer, command cliCommand, ancestors []string, child cliSub, depth int) {
	names := append(append([]string(nil), ancestors...), child.Name)
	_, _ = fmt.Fprintf(w, "%s %s %s\n\n%s\n\n", strings.Repeat("#", min(depth, 6)), command.Name, strings.Join(names, " "), mdText(child.Short))
	_, _ = fmt.Fprintf(w, "`%s`\n\n", mdSubSynopsis(command, names, child.Positionals, child.Flags))
	if len(child.Flags) > 0 {
		mdFlagTable(w, child.Flags)
	}
	if len(child.Examples) > 0 {
		writeMarkdownExamples(w, child.Examples)
	}
	for _, next := range child.Subcommands {
		renderMarkdownSubtree(w, command, names, next, depth+1)
	}
}

func mdSubSynopsis(command cliCommand, names, positionals []string, flags []cliFlag) string {
	path := localHelpSubcommandPath(command, names)
	var terminal cliSub
	var found bool
	choices := command.Subcommands
	for _, name := range names {
		terminal, found = findCliSubcommand(choices, name)
		if !found {
			break
		}
		choices = terminal.Subcommands
	}
	parts := []string{path}
	if found && len(terminal.Subcommands) > 0 && !terminal.SubcommandsAfterPositionals {
		parts = append(parts, "<"+terminal.subcommandChoice()+">")
	}
	appendPositionals := !found || !terminal.SubcommandsAfterPositionals
	if appendPositionals && found && terminal.FlagsAfterPositionals {
		parts = append(parts, positionals...)
	}
	for _, flag := range flags {
		parts = append(parts, mdFlagSyntax(flag))
	}
	if appendPositionals && (!found || !terminal.FlagsAfterPositionals) {
		parts = append(parts, positionals...)
	}
	if found && len(terminal.Subcommands) > 0 && terminal.SubcommandsAfterPositionals {
		parts = append(parts, "<"+terminal.subcommandChoice()+">")
	}
	return strings.Join(parts, " ")
}

func writeMarkdownExamples(w io.Writer, examples []string) {
	_, _ = fmt.Fprintln(w, "Examples:")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "```sh")
	for _, example := range examples {
		_, _ = fmt.Fprintln(w, example)
	}
	_, _ = fmt.Fprintln(w, "```")
	_, _ = fmt.Fprintln(w)
}

func mdSynopsis(c cliCommand) string {
	parts := []string{"gregale", c.Name}
	if len(c.Subcommands) > 0 && !c.SubcommandsAfterPositionals {
		parts = append(parts, "[<subcommand>]")
	}
	parts = append(parts, c.Positionals...)
	if len(c.Subcommands) > 0 && c.SubcommandsAfterPositionals {
		parts = append(parts, "[<subcommand>]")
	}
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
	label := mdFlagLabel(f)
	if !f.Req {
		label = "[" + label + "]"
	}
	if f.Repeatable {
		label += "..."
	}
	return label
}

func mdFlagLabel(f cliFlag) string {
	label := strings.Join(cliFlagSpellings(f), "|")
	if f.Bool && len(f.ClosedSet) > 0 {
		label += "[=" + strings.Join(f.ClosedSet, "|") + "]"
	} else if value := mdFlagValue(f); value != "" {
		label += " <" + value + ">"
	}
	return label
}

func mdFlagValue(f cliFlag) string {
	if f.Bool {
		return ""
	}
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
