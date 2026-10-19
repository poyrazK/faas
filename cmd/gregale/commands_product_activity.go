package main

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func validateProductCustomerActivity(page api.ProductCustomerActivityPage, c productCommand) error {
	if page.TenantID != c.tenant {
		return errors.New("activity response belongs to a different customer")
	}
	if err := productcontrol.ValidateActivityPage(page, c.cursor); err != nil {
		return err
	}
	for _, e := range page.Events {
		if e.ReviewURL != productcontrol.ActivityReviewURL(c.app, c.tenant, e) {
			return errors.New("activity source link does not match the requested product/customer")
		}
	}
	return nil
}

func writeProductCustomerActivity(out io.Writer, page api.ProductCustomerActivityPage) error {
	if _, err := fmt.Fprintf(out, "Customer %s · observed %s\nRetained Product Control records, newest first; historical evidence, not a complete audit log.\n", page.TenantID, page.ObservedAt.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	for _, e := range page.Events {
		if _, err := fmt.Fprintf(out, "\n%s · %s\n%s\nSource: %s\n", e.OccurredAt.UTC().Format(time.RFC3339Nano), productcontrol.ActivityKindLabel(e.Kind), productcontrol.ActivitySummary(e), e.ID); err != nil {
			return err
		}
		if e.Reason != "" {
			if _, err := fmt.Fprintln(out, "Reason: "+e.Reason); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(out, "Review: "+e.ReviewURL); err != nil {
			return err
		}
	}
	if len(page.Events) == 0 {
		_, err := fmt.Fprintln(out, "No retained events on this page. Missing history does not establish inactivity or failure.")
		return err
	}
	if page.NextCursor != "" {
		_, err := fmt.Fprintln(out, "\nOlder events: --cursor "+page.NextCursor)
		return err
	}
	return nil
}
