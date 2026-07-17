// Command faas-nft-render prints the host nftables ruleset to stdout.
//
// Used by `make egress-render` to (re)generate the checked-in artifact at
// `deploy/ansible/roles/nftables/files/policy_nftables.conf`. The artifact is
// what ansible copies onto the host at `make bootstrap` time; this binary is
// the single source of truth.
//
// stdout, exit 0 only. Failure to render panics — that's a build-time bug,
// not a runtime concern.
package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/netns"
)

func main() {
	if _, err := os.Stdout.WriteString(netns.DefaultHostPolicy.Render()); err != nil {
		// Render() never returns an error today; this branch is the future
		// hook if the renderer ever returns one.
		fmt.Fprintln(os.Stderr, "faas-nft-render: write stdout:", err)
		os.Exit(1)
	}
}
