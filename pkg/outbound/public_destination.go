package outbound

import "github.com/onebox-faas/faas/pkg/publicdest"

// The public-destination guard lives in pkg/publicdest, which depends only on
// the standard library so daemons that must not link this package (the hosted
// MCP server's client metadata fetch) share it (ADR-20261030065746).
var (
	ErrUnsafeDestination       = publicdest.ErrUnsafe
	NewPublicDestinationDialer = publicdest.NewDialer
	ValidatePublicOrigin       = publicdest.ValidateOrigin
)

// PublicDestinationDialer dials only globally reachable addresses.
type PublicDestinationDialer = publicdest.Dialer
