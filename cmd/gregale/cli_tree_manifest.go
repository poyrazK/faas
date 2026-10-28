package main

// cli_tree_manifest.go — ADR-20261026145832 step 5.
//
// cliTreeManifest is a customer manifest in the command tree's names: the
// shortcuts as they are, one command per noun whose verbs are today's
// commands and verbs under their tree names, then the customer commands the
// tree does not place yet. The Markdown reference renders it; dispatch still
// runs today's commands through rewriteCliTree.

import (
	"slices"
	"strings"
)

func cliTreeManifest(commands []cliCommand) []cliCommand {
	d := cliTreeDiscoveryOf(commands)
	var manifest []cliCommand
	for _, name := range cliTreeShortcutCommands {
		if command, ok := d.commands[name]; ok && command.Audience == cliAudienceCustomer {
			manifest = append(manifest, command)
		}
	}
	for _, noun := range cliTreeNouns {
		if command, ok := d.treeCommand(noun); ok {
			manifest = append(manifest, command)
		}
	}
	for _, command := range customerAudience(commands) {
		if _, ok := d.commands[command.Name]; ok && !cliTreePlaces(command.Name) {
			manifest = append(manifest, command)
		}
	}
	return manifest
}

// cliTreeBarePaths run today's command without a verb; that command's verbs
// have tree names elsewhere (`apps tcp` is `app listener`).
var cliTreeBarePaths = map[string]bool{"app list": true}

// treeCommand builds a noun's command. A noun that names one of today's
// paths takes that path's flags, notes and examples.
func (d cliTreeDiscovery) treeCommand(noun cliTreeNoun) (cliCommand, bool) {
	subs := d.treeSubs([]string{noun.Name})
	if len(subs) == 0 {
		return cliCommand{}, false
	}
	command := cliCommand{Name: noun.Name, DocSlug: "cli", Short: noun.Short, Subcommands: subs, Audience: cliAudienceCustomer}
	if old, ok := cliTreeOldPath(noun.Name); ok {
		if today, ok := d.commands[strings.Fields(old)[0]]; ok {
			command.DocSlug = today.DocSlug
		}
		if node, ok := d.subAt(old); ok {
			command.Flags = node.Flags
			command.Positionals = node.Positionals
			command.Examples = renameHelpLines(node.Examples, old, noun.Name)
			command.HelpNotes = renameHelpLines(node.Notes, old, noun.Name)
		}
	}
	if noun.Name == appSlugFallback {
		command.Flags, command.Positionals = nil, nil
	}
	return command, true
}

// cliTreeOldPath returns today's path for a tree path the table names.
func cliTreeOldPath(path string) (string, bool) {
	for _, alias := range cliTreeAliases {
		if alias.New == path {
			return alias.Old, true
		}
	}
	return "", false
}

// subAt returns today's command or verb at path as a verb, so a top-level
// command can sit under a noun.
func (d cliTreeDiscovery) subAt(path string) (cliSub, bool) {
	words := strings.Fields(path)
	command, ok := d.commands[words[0]]
	if !ok || command.Audience != cliAudienceCustomer {
		return cliSub{}, false
	}
	sub := cliSub{
		Name: command.Name, Short: command.Short, Notes: command.HelpNotes, Examples: command.Examples,
		Positionals: command.Positionals, Flags: command.Flags, Subcommands: command.Subcommands,
		SubcommandsAfterPositionals: command.SubcommandsAfterPositionals,
	}
	for _, word := range words[1:] {
		next, ok := findCliSubcommand(sub.Subcommands, word)
		if !ok {
			return cliSub{}, false
		}
		sub = next
	}
	return sub, true
}

// treeSubs lists the verbs and subnouns under prefix, each with the flags,
// positionals and verbs of the command it runs today.
func (d cliTreeDiscovery) treeSubs(prefix []string) []cliSub {
	name := strings.Join(prefix, " ")
	renamed := map[string]bool{}
	for _, alias := range cliTreeAliases {
		if alias.New != alias.Old {
			renamed[alias.Old] = true
		}
	}
	var subs []cliSub
	seen := map[string]bool{}
	add := func(sub cliSub) {
		if !seen[sub.Name] {
			seen[sub.Name] = true
			subs = append(subs, sub)
		}
	}
	// Words the table places directly under prefix come first: they win
	// over today's verb of the same name.
	for _, alias := range cliTreeAliases {
		path := strings.Fields(alias.New)
		if len(path) <= len(prefix) || !slices.Equal(path[:len(prefix)], prefix) || seen[path[len(prefix)]] {
			continue
		}
		head := path[len(prefix)]
		child := append(slices.Clone(prefix), head)
		if old, ok := cliTreeOldPath(strings.Join(child, " ")); ok {
			if sub, ok := d.subAt(old); ok {
				sub = renameCliSub(sub, old, strings.Join(child, " "))
				sub.Name, sub.Aliases = head, nil
				if short, ok := cliTreeShorts[strings.Join(child, " ")]; ok {
					sub.Short = short
				}
				switch {
				case cliTreeBarePaths[strings.Join(child, " ")]:
					sub.Subcommands = nil
				case !sub.SubcommandsAfterPositionals:
					sub.Subcommands = d.treeSubs(child)
				}
				add(sub)
			}
			continue
		}
		if group := d.treeSubs(child); len(group) > 0 {
			verbs := make([]string, 0, len(group))
			for _, sub := range group {
				verbs = append(verbs, sub.Name)
			}
			add(cliSub{Name: head, Short: strings.Join(verbs, " · "), Subcommands: group})
		}
	}
	if old, ok := cliTreeOldPath(name); ok {
		if node, ok := d.subAt(old); ok && !node.SubcommandsAfterPositionals {
			for _, sub := range node.Subcommands {
				verb, ok := cliTreeVerbFor(sub.Name, node.Subcommands)
				if !ok || renamed[old+" "+sub.Name] {
					continue
				}
				sub = renameCliSub(sub, old+" "+sub.Name, name+" "+verb)
				sub = renameCliSub(sub, old, name)
				sub.Name = verb
				add(sub)
			}
		}
	}
	if name == appSlugFallback {
		for _, sub := range d.appTreeSubs() {
			add(sub)
		}
	}
	return subs
}

// appTreeSubs lists today's `app <app> <verb>` verbs in the tree's
// verb-first form, `app <verb> [<app>]` (rewriteAppPath).
func (d cliTreeDiscovery) appTreeSubs() []cliSub {
	command, ok := d.commands[appSlugFallback]
	if !ok {
		return nil
	}
	target := "[<app>]"
	subs := []cliSub{
		{Name: "get", Short: "Show an app's settings and status", Positionals: []string{target}},
		{Name: "update", Short: "Change an app's settings", Positionals: []string{target}, Flags: command.Flags},
	}
	listener := false
	for _, sub := range command.Subcommands {
		switch {
		case sub.Name != "tcp" && sub.Name != "udp":
		case listener:
			continue
		default:
			// rewriteAppListener maps standard verbs; the others keep
			// today's spelling, which rewriteAppPath dispatches.
			listener = true
			sub.Name, sub.Short = "listener", "Manage TCP and UDP listeners (--protocol tcp|udp)"
			sub.Subcommands = standardCliVerbs(sub.Subcommands)
		}
		subs = append(subs, withAppTarget(sub, target))
	}
	return subs
}

// withAppTarget puts the app after the last verb of a tree `app` path.
func withAppTarget(sub cliSub, target string) cliSub {
	if len(sub.Subcommands) == 0 || sub.SubcommandsAfterPositionals {
		sub.Positionals = append([]string{target}, sub.Positionals...)
		return sub
	}
	nested := make([]cliSub, len(sub.Subcommands))
	for i, child := range sub.Subcommands {
		nested[i] = withAppTarget(child, target)
	}
	sub.Subcommands = nested
	return sub
}

// standardCliVerbs spells verbs in their standard form, dropping a synonym
// whose standard verb is also listed (`rm` beside `delete`).
func standardCliVerbs(subs []cliSub) []cliSub {
	standard := make([]cliSub, 0, len(subs))
	for _, sub := range subs {
		if verb, ok := cliTreeVerbFor(sub.Name, subs); ok {
			sub.Name = verb
			standard = append(standard, sub)
		}
	}
	return standard
}

// renameCliSub spells today's path as the tree path in a verb's examples
// and notes, and in those of its verbs.
func renameCliSub(sub cliSub, from, to string) cliSub {
	sub.Examples = renameHelpLines(sub.Examples, from, to)
	sub.Notes = renameHelpLines(sub.Notes, from, to)
	if len(sub.Subcommands) > 0 {
		nested := make([]cliSub, len(sub.Subcommands))
		for i, child := range sub.Subcommands {
			nested[i] = renameCliSub(child, from, to)
		}
		sub.Subcommands = nested
	}
	return sub
}

func renameHelpLines(lines []string, from, to string) []string {
	if len(lines) == 0 {
		return lines
	}
	renamed := make([]string, len(lines))
	for i, line := range lines {
		renamed[i] = strings.TrimSuffix(renameHelpPath(line+"\n", from, to), "\n")
	}
	return renamed
}
