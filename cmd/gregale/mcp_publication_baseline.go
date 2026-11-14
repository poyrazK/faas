package main

import (
	"context"
	"errors"
)

func checkMCPPublicationBaseline(ctx context.Context, c *Client, slug, candidate string, pin *mcpCatalogPinReceipt) error {
	if pin.expectedAppID != "" {
		app, err := c.GetApp(ctx, slug)
		if err != nil {
			return err
		}
		if app.ID != pin.expectedAppID {
			return errors.New("app identity changed since review; candidate remains unpromoted")
		}
	}
	if pin.expectedServing != nil {
		serving, err := mcpServingDeployment(ctx, c, slug, candidate)
		if err != nil {
			return err
		}
		if serving != *pin.expectedServing {
			return errors.New("serving release changed since review; candidate remains unpromoted")
		}
	}
	return nil
}
