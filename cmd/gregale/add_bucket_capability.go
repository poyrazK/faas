package main

import (
	"context"
	"errors"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// addBucketCapabilities is the subset of the API client add bucket reads.
type addBucketCapabilities interface {
	GetCapabilities(context.Context) (api.CapabilitiesResponse, error)
}

// addBucketBindingUnavailable returns why the account cannot bind a bucket to
// an app (object-storage-compute), or nil when it can or the answer is
// unknown. An unreadable capability list never blocks: the server still
// refuses the bind, as before. Free accounts used to get a created bucket and
// then "Could not attach object-storage bucket".
func addBucketBindingUnavailable(ctx context.Context, client addBucketCapabilities) error {
	capabilities, err := client.GetCapabilities(ctx)
	if err != nil {
		return nil //nolint:nilerr // an unreadable capability list never blocks; the server still refuses the bind
	}
	for _, capability := range capabilities.Capabilities {
		if capability.Key != "object-storage-compute" || capability.Enabled {
			continue
		}
		detail := strings.TrimSpace(capability.UnavailableDetail)
		if detail == "" {
			detail = "Binding a bucket to an app is not available to this account."
		}
		if capability.UnavailableReason == api.CapabilityUnavailablePlan && len(capability.Plans) > 0 {
			detail += " Binding buckets to apps is included on: " + strings.Join(capability.Plans, ", ") + "."
		}
		return errors.New(detail + " No bucket was created.")
	}
	return nil
}
