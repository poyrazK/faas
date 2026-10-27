package main

// adr: 20261023115741 — the CLI hides the commands of a capability whose
// platform flag is off for the account. The product catalog says which
// command paths each capability owns (`cli`); GET /v1/capabilities says
// which capabilities are hidden for the account, and the completion cache
// keeps that answer for an hour.
//
// Help, completion, man pages and suggestions follow the account's view, or
// the catalog's tier defaults (GA and beta shown) when no account view is
// known. Running a command is refused only when the server said its
// capability is hidden, after asking again, so an offline or logged-out CLI
// never blocks a command it cannot judge. The apid gate still refuses the
// API calls of a hidden capability; hiding is discovery, not enforcement.

import (
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/productcap"
)

const (
	// capabilityViewTTL is how long a cached account view is used before
	// discovery reads it again.
	capabilityViewTTL = time.Hour
	// capabilityViewTimeout bounds the read, so help never waits long on a
	// slow or unreachable API.
	capabilityViewTimeout = 2 * time.Second
)

// cliCapabilityIndex maps command paths to the capability that owns them.
type cliCapabilityIndex struct {
	owner map[string]string
	// tierHidden are the capabilities hidden by tier default: preview or
	// internal, themselves or through an ancestor.
	tierHidden map[string]bool
}

var cliCapabilities = sync.OnceValue(func() cliCapabilityIndex {
	index := cliCapabilityIndex{owner: map[string]string{}, tierHidden: map[string]bool{}}
	catalog, err := productcap.Load()
	if err != nil {
		return index
	}
	byID := make(map[string]productcap.Capability, len(catalog.Capabilities))
	for _, capability := range catalog.Capabilities {
		byID[capability.ID] = capability
		for _, path := range capability.CLI {
			index.owner[path] = capability.ID
		}
	}
	for _, capability := range catalog.Capabilities {
		for id, depth := capability.ID, 0; id != "" && depth < 8; id, depth = byID[id].Parent, depth+1 {
			if maturity := byID[id].Maturity; maturity == productcap.MaturityPreview || maturity == productcap.MaturityInternal {
				index.tierHidden[capability.ID] = true
			}
		}
	}
	return index
})

// cliPathCapability returns the capability owning the longest prefix of a
// command path ("bucket lifecycle rules" → "bucket lifecycle" → "bucket").
func cliPathCapability(path string) string {
	index := cliCapabilities()
	for path != "" {
		if capability, ok := index.owner[path]; ok {
			return capability
		}
		cut := strings.LastIndexByte(path, ' ')
		if cut < 0 {
			return ""
		}
		path = path[:cut]
	}
	return ""
}

// capabilityView decides which capabilities are hidden. A view with all set
// hides nothing; otherwise hidden decides, falling back to tier defaults
// when tier is set.
type capabilityView struct {
	all    bool
	tier   bool
	hidden map[string]bool
}

func (v capabilityView) hides(capability string) bool {
	switch {
	case capability == "" || v.all:
		return false
	case v.tier:
		return cliCapabilities().tierHidden[capability]
	}
	return v.hidden[capability]
}

func (v capabilityView) hidesPath(path string) bool { return v.hides(cliPathCapability(path)) }

func accountCapabilityView(hidden []string) capabilityView {
	set := make(map[string]bool, len(hidden))
	for _, id := range hidden {
		set[id] = true
	}
	return capabilityView{hidden: set}
}

// capabilityViewMemo holds the discovery view for one run.
var capabilityViewMemo struct {
	sync.Mutex
	set  bool
	view capabilityView
}

// resetCapabilityView forgets the discovery view; run calls it first.
func resetCapabilityView() {
	capabilityViewMemo.Lock()
	capabilityViewMemo.set = false
	capabilityViewMemo.Unlock()
}

func capabilityReleaseAll() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(capflags.ReleaseAllEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// discoveryCapabilityView is the view help, completion, man pages and
// suggestions use: the account's, read again once it is an hour old, or the
// tier defaults when logged out or never read.
func discoveryCapabilityView() capabilityView {
	capabilityViewMemo.Lock()
	defer capabilityViewMemo.Unlock()
	if capabilityViewMemo.set {
		return capabilityViewMemo.view
	}
	capabilityViewMemo.view, capabilityViewMemo.set = computeDiscoveryView(), true
	return capabilityViewMemo.view
}

// offlineCapabilityView is the run's discovery view when it is known, else
// the cached account view, else the tier defaults. It never asks the API, so
// an error path that lists verbs does not wait on it (Hunt 14 H14-89).
func offlineCapabilityView() capabilityView {
	capabilityViewMemo.Lock()
	defer capabilityViewMemo.Unlock()
	if capabilityViewMemo.set {
		return capabilityViewMemo.view
	}
	if capabilityReleaseAll() {
		return capabilityView{all: true}
	}
	token := loadTokenWithDiagnostics(io.Discard)
	if token == "" {
		return capabilityView{tier: true}
	}
	if hidden, _, ok := cachedHiddenCapabilities(token); ok {
		return accountCapabilityView(hidden)
	}
	return capabilityView{tier: true}
}

func computeDiscoveryView() capabilityView {
	if capabilityReleaseAll() {
		return capabilityView{all: true}
	}
	token := loadTokenWithDiagnostics(io.Discard)
	if token == "" {
		return capabilityView{tier: true}
	}
	hidden, savedAt, ok := cachedHiddenCapabilities(token)
	if ok && time.Since(savedAt) < capabilityViewTTL {
		return accountCapabilityView(hidden)
	}
	if fresh, read := readHiddenCapabilities(token); read {
		return accountCapabilityView(fresh)
	}
	if ok {
		return accountCapabilityView(hidden) // stale beats tier defaults
	}
	return capabilityView{tier: true}
}

func cachedHiddenCapabilities(token string) ([]string, time.Time, bool) {
	return NewClient(apiBase(), token).CompletionCache().HiddenCapabilities()
}

// readHiddenCapabilities asks the API; the client stores the answer in the
// completion cache on the way.
func readHiddenCapabilities(token string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), capabilityViewTimeout)
	defer cancel()
	response, err := NewClient(apiBase(), token).GetCapabilities(ctx)
	if err != nil {
		return nil, false
	}
	return response.HiddenCapabilities, true
}

// commandCapabilityPath is the manifest path of an invocation: the command
// and up to two verbs ("bucket lifecycle", "app tcp list" for
// `app SLUG tcp list`).
func commandCapabilityPath(args []string) string {
	if len(args) == 0 {
		return ""
	}
	command, ok := lookupCliCommand(args[0])
	if !ok {
		return args[0]
	}
	path, rest := command.Name, args[1:]
	if command.SubcommandsAfterPositionals && command.hasSlugFirst() && len(rest) > 0 {
		rest = rest[1:]
	}
	subs := command.Subcommands
	for depth := 0; depth < 2 && len(rest) > 0 && !strings.HasPrefix(rest[0], "-"); depth++ {
		sub, ok := findCliSubcommand(subs, rest[0])
		if !ok {
			break
		}
		path += " " + sub.Name
		subs, rest = sub.Subcommands, rest[1:]
		if sub.SubcommandsAfterPositionals && len(rest) > 0 {
			rest = rest[1:]
		}
	}
	return path
}

// localCommands never call the platform. A hidden capability leaves them out
// of help like its other commands but does not refuse to run them.
var localCommands = map[string]bool{"data-api dev": true}

// hiddenCommand reports an invocation whose command is hidden, with its
// path. A help request follows the discovery view. Running a command is
// refused only when the cached account view hides it and a fresh read
// agrees, so a capability released a minute ago runs at once.
func hiddenCommand(args []string, helpRequest bool) (string, bool) {
	path := commandCapabilityPath(args)
	capability := cliPathCapability(path)
	if capability == "" || capabilityReleaseAll() {
		return path, false
	}
	if helpRequest {
		return path, discoveryCapabilityView().hides(capability)
	}
	if localCommands[path] {
		return path, false
	}
	token := loadTokenWithDiagnostics(io.Discard)
	if token == "" {
		return path, false
	}
	cached, _, ok := cachedHiddenCapabilities(token)
	if !ok || !slices.Contains(cached, capability) {
		return path, false
	}
	fresh, read := readHiddenCapabilities(token)
	if !read {
		return path, true
	}
	return path, slices.Contains(fresh, capability)
}

// discoverableCliCommands is the manifest discovery surfaces read: the
// released commands without those, or the verbs, the capability view hides.
func discoverableCliCommands() []cliCommand {
	commands := visibleCliCommands()
	view := discoveryCapabilityView()
	if view.all {
		return commands
	}
	discoverable := make([]cliCommand, 0, len(commands))
	for _, command := range commands {
		if view.hidesPath(command.Name) {
			continue
		}
		command.Subcommands = view.discoverableSubcommands(command.Name, command.Subcommands)
		discoverable = append(discoverable, command)
	}
	return discoverable
}

func (v capabilityView) discoverableSubcommands(parent string, subs []cliSub) []cliSub {
	if subs == nil {
		return nil
	}
	kept := make([]cliSub, 0, len(subs))
	for _, sub := range subs {
		path := parent + " " + sub.Name
		if v.hidesPath(path) {
			continue
		}
		sub.Subcommands = v.discoverableSubcommands(path, sub.Subcommands)
		kept = append(kept, sub)
	}
	return kept
}

// lookupDiscoverableCommand is lookupCliCommand over the discoverable
// manifest, for help and man pages.
func lookupDiscoverableCommand(name string) (cliCommand, bool) {
	for _, command := range discoverableCliCommands() {
		if command.Name == name {
			return command, true
		}
	}
	return cliCommand{}, false
}
