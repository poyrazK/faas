// adr: 20261123180000

package main

import "context"

// GuideClip shortens a docs snippet as `gregale guide "<query>"` shows it.
var GuideClip = clip

// GuideSearchCommands returns the command lines `gregale guide "<query>"`
// lists, best first, without reading the network.
func GuideSearchCommands(query string) []string {
	ctx := context.Background()
	// The manifest read memoizes the capability view for the process; reset
	// it on both sides so other tests see their own view, as Run does.
	resetCapabilityView()
	defer resetCapabilityView()
	var out []string
	for _, hit := range searchGuideCommands(ctx, query) {
		out = append(out, hit.Command)
	}
	return out
}
