package main

// nestedVerbPath names the one verb group under noun that owns verb, so an
// unknown `release promote` can point at `release traffic promote` (prod hunt
// r32). It returns "" when no group, or more than one, has that verb.
func nestedVerbPath(noun cliTreeNoun, verb string) string {
	tree, ok := newCliTreeDiscovery().treeCommand(noun)
	if !ok || verb == "" {
		return ""
	}
	match := ""
	for _, group := range tree.Subcommands {
		if _, ok := findCliSubcommand(group.Subcommands, verb); !ok {
			continue
		}
		if match != "" {
			return ""
		}
		match = noun.Name + " " + group.Name + " " + verb
	}
	return match
}
