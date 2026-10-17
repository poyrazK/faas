//go:build !linux || !amd64 || !metal || !promotionacceptance

package promotionfault

import "context"

// Hit cannot activate fault injection in ordinary production or metal builds.
func Hit(context.Context, string, string, string, string) error { return nil }
