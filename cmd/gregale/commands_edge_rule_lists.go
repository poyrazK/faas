package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdEdgeRuleLists dispatches `gregale edge-rule-lists <sub>` (ADR-833):
// account-level named lists referenced from match conditions with
// {"op":"in_list","list":"<name>"}.
func cmdEdgeRuleLists(args []string) int {
	parent, _ := lookupCliCommand("edge-rule-lists")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale edge-rule-lists <list|get|create|update|rm> [args]", "edge-rule-lists")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdEdgeRuleListsList(args[1:])
	case subGet:
		return cmdEdgeRuleListsGet(args[1:])
	case subCreate:
		return cmdEdgeRuleListsCreate(args[1:])
	case subUpdate:
		return cmdEdgeRuleListsUpdate(args[1:])
	case subRm:
		return cmdEdgeRuleListsRm(args[1:])
	}
	printCommandValidation(os.Stderr, "unknown edge-rule-lists subcommand %q\n", args[0])
	if sug, _ := suggestSubcommand(args[0], parent); sug != "" {
		maybeSuggestSub(sug)
	}
	return 1
}

func cmdEdgeRuleListsList(args []string) int {
	fs := newFlagSet("edge-rule-lists list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListEdgeRuleLists(context.Background())
	if err != nil {
		return printErr("List failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "%-32s %-8s %7s %5s  %s\n", "NAME", "KIND", "ITEMS", "RULES", "DESCRIPTION")
	for _, l := range out.Lists {
		_, _ = fmt.Fprintf(osStdout, "%-32s %-8s %7d %5d  %s\n", l.Name, l.Kind, l.ItemCount, len(l.ReferencedBy), truncate(l.Description, 40))
	}
	return 0
}

func edgeRuleListNameArg(fs *flag.FlagSet, sub, usage string) (string, bool) {
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale edge-rule-lists "+sub+" "+usage, "edge-rule-lists")
		return "", false
	}
	return fs.Arg(0), true
}

func printEdgeRuleList(l api.EdgeRuleListResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(l))
	}
	_, _ = fmt.Fprintf(osStdout, "%s (%s, %d items)\n", l.Name, l.Kind, l.ItemCount)
	if l.Description != "" {
		_, _ = fmt.Fprintf(osStdout, "  %s\n", l.Description)
	}
	if len(l.ReferencedBy) > 0 {
		_, _ = fmt.Fprintf(osStdout, "  used by: %s\n", strings.Join(l.ReferencedBy, ", "))
	}
	for _, item := range l.Items {
		_, _ = fmt.Fprintf(osStdout, "  %s\n", item)
	}
	return 0
}

func cmdEdgeRuleListsGet(args []string) int {
	fs := newFlagSet("edge-rule-lists get", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	name, ok := edgeRuleListNameArg(fs, "get", "<name>")
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	l, err := client.GetEdgeRuleList(context.Background(), name)
	if err != nil {
		return printErr("Get failed", err)
	}
	return printEdgeRuleList(l)
}

// readEdgeRuleListItems reads one item per line from path ("-" is stdin),
// skipping blank lines and # comments.
func readEdgeRuleListItems(path string) ([]string, error) {
	var r io.Reader = os.Stdin
	if path != "-" {
		f, err := openCustomerFile(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	var items []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			items = append(items, line)
		}
	}
	return items, sc.Err()
}

func cmdEdgeRuleListsCreate(args []string) int {
	fs := newFlagSet("edge-rule-lists create", flag.ContinueOnError)
	kind := fs.String("kind", "", "list kind: ip, country, host or string (required)")
	description := fs.String("description", "", "free-text description")
	var items multiFlag
	fs.Var(&items, "item", "list item (repeat)")
	itemsFile := fs.String("items-file", "", "file with one item per line, or - for stdin")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	const usage = "<name> --kind ip|country|host|string [--item V]... [--items-file path|-] [--description D]"
	name, ok := edgeRuleListNameArg(fs, "create", usage)
	if !ok {
		return 1
	}
	if *kind == "" {
		PrintUsage(os.Stderr, "usage: gregale edge-rule-lists create "+usage, "edge-rule-lists")
		return 1
	}
	req := api.CreateEdgeRuleListRequest{Name: name, Kind: *kind, Description: *description, Items: []string(items)}
	if *itemsFile != "" {
		fromFile, err := readEdgeRuleListItems(*itemsFile)
		if err != nil {
			return printErr("Read items failed", err)
		}
		req.Items = append(req.Items, fromFile...)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	l, err := client.CreateEdgeRuleList(context.Background(), req)
	if err != nil {
		return printErr("Create failed", err)
	}
	return printEdgeRuleList(l)
}

func cmdEdgeRuleListsUpdate(args []string) int {
	fs := newFlagSet("edge-rule-lists update", flag.ContinueOnError)
	description := fs.String("description", "", "new description")
	var add, remove multiFlag
	fs.Var(&add, "add", "item to add (repeat)")
	fs.Var(&remove, "remove", "item to remove (repeat)")
	replaceFile := fs.String("replace-file", "", "replace every item from a file (one per line), or - for stdin")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	name, ok := edgeRuleListNameArg(fs, "update", "<name> [--add V]... [--remove V]... [--replace-file path|-] [--description D]")
	if !ok {
		return 1
	}
	req := api.UpdateEdgeRuleListRequest{Add: []string(add), Remove: []string(remove)}
	if flagWasSet(fs, "description") {
		req.Description = description
	}
	if *replaceFile != "" {
		items, err := readEdgeRuleListItems(*replaceFile)
		if err != nil {
			return printErr("Read items failed", err)
		}
		req.Items = &items
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	l, err := client.UpdateEdgeRuleList(context.Background(), name, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	return printEdgeRuleList(l)
}

func cmdEdgeRuleListsRm(args []string) int {
	fs := newFlagSet("edge-rule-lists rm", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	name, ok := edgeRuleListNameArg(fs, "rm", "<name>")
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteEdgeRuleList(context.Background(), name); err != nil {
		return printErr("Delete failed", err)
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStdout, "Deleted edge rule list %s\n", name)
	}
	return 0
}
