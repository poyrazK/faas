package main

import (
	"context"
	"fmt"
)

// domainBoundToApp refuses a `domain delete --app` whose domain is bound to
// another app: the binding is global, so --app is a check, not a selector.
func domainBoundToApp(ctx context.Context, client *Client, domain, app string) error {
	d, err := client.GetDomain(ctx, domain)
	if err != nil {
		return fmt.Errorf("read domain %s: %w", domain, err)
	}
	a, err := client.GetApp(ctx, app)
	if err != nil {
		return fmt.Errorf("read app %s: %w", app, err)
	}
	if d.AppID != a.ID {
		return fmt.Errorf("%s is not bound to %s; nothing was removed (gregale domain list shows its app)", domain, app)
	}
	return nil
}
