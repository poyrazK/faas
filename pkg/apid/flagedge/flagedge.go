// Package flagedge is apid's publish policy for edge flag routing
// (ADR-20261124212701): while the edge_flags platform flag is off for a
// project's owner, a configuration may keep the edge routes it already has
// but may not add or change them.
package flagedge

import (
	"context"

	"github.com/onebox-faas/faas/pkg/flags"
)

// PublishAllowed reports whether next may be published. released is the
// edge_flags decision for the project's owner; current loads the
// environment's latest configuration and is read only when routes would
// change. A configuration whose routes cannot be compared is refused.
func PublishAllowed(ctx context.Context, released bool, next flags.Config, current func(context.Context) (flags.Config, error)) bool {
	if released || !flags.HasEdge(next) {
		return true
	}
	cur, err := current(ctx)
	return err == nil && flags.SameEdge(cur, next)
}
