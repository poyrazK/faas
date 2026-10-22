package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"regexp"
	"strings"
	"testing"
)

// Issue #3362: per-command help printed docsURL + "/" + DocSlug, and 76 of
// 78 of those pages returned 404. Help must only link pages the docs site
// serves (docsPageSlugs) or fall back to the CLI reference.
func TestHelpDocsLinksPointAtServedPages(t *testing.T) {
	docsLine := regexp.MustCompile(`(?m)^Docs: (\S+)$`)
	commands := append(main.CustomerCliCommands(), main.AdvancedCliCommands()...)
	if len(commands) == 0 {
		t.Fatal("no CLI commands registered")
	}
	for _, command := range commands {
		var buf bytes.Buffer
		main.PrintLocalCommandHelp(&buf, command)
		for _, sub := range command.Subcommands {
			main.PrintLocalSubcommandHelp(&buf, command, sub)
		}
		for _, m := range docsLine.FindAllStringSubmatch(buf.String(), -1) {
			url := m[1]
			if url == main.DocsSiteURL || url == main.CliDocsURL {
				continue
			}
			slug := strings.TrimPrefix(url, main.DocsSiteURL+"/")
			if _, ok := (*main.DocsPageSlugs)[slug]; !ok || slug == url {
				t.Errorf("gregale %s --help links %s, which is not a served docs page", command.Name, url)
			}
		}
	}
}
