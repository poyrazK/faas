package objectstorage

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectTransferConfig declares the operator's edge contract and bounds local
// upload resources. A direct profile requires a TLS origin without a body-size
// limiting CDN in front of the branded endpoint.
type ObjectTransferConfig struct {
	Profile              string `json:"profile,omitempty"`
	TimeoutSeconds       int64  `json:"timeout_seconds,omitempty"`
	MaxConcurrentUploads int    `json:"max_concurrent_uploads,omitempty"`
	MaxSpoolBytes        int64  `json:"max_spool_bytes,omitempty"`
	MinSpoolFreeBytes    int64  `json:"min_spool_free_bytes,omitempty"`
}

func NormalizeObjectTransfer(c ObjectTransferConfig, single, part int64) (ObjectTransferConfig, error) {
	bad := errors.New("object storage: invalid transfer profile or resource limits")
	if single < 1 || single > api.MaxObjectSinglePutBytes || part < 1 || part > api.MaxObjectSinglePutBytes {
		return c, bad
	}
	// Preserve existing registries' byte limits. Deployed examples explicitly
	// declare proxied so increasing a request size cannot silently exceed it.
	if c.Profile == "" {
		c.Profile = "direct"
	}
	if c.Profile != "direct" && c.Profile != "proxied" {
		return c, bad
	}
	if c.Profile == "proxied" && max(single, part) > api.MaxObjectProxiedRequestBytes {
		return c, bad
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = int64(api.ObjectTransferTimeout / time.Second)
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > int64(api.MaxObjectTransferTimeout/time.Second) {
		return c, bad
	}
	if c.MaxConcurrentUploads == 0 {
		c.MaxConcurrentUploads = api.DefaultObjectConcurrentUploads
	}
	if c.MaxConcurrentUploads < 1 || c.MaxConcurrentUploads > api.MaxObjectConcurrentUploads {
		return c, bad
	}
	if c.MaxSpoolBytes == 0 {
		c.MaxSpoolBytes = min(single*int64(c.MaxConcurrentUploads), api.MaxObjectUploadSpoolBytes)
	}
	if c.MaxSpoolBytes < single || c.MaxSpoolBytes > api.MaxObjectUploadSpoolBytes {
		return c, bad
	}
	if c.MinSpoolFreeBytes == 0 {
		c.MinSpoolFreeBytes = api.ObjectUploadSpoolMinFreeBytes
	}
	if c.MinSpoolFreeBytes < 1 || c.MinSpoolFreeBytes > api.MaxObjectUploadSpoolBytes {
		return c, bad
	}
	return c, nil
}

func (r *Registry) TransferTimeout() time.Duration {
	if r == nil || r.Transfer.TimeoutSeconds == 0 {
		return api.ObjectTransferTimeout
	}
	return time.Duration(r.Transfer.TimeoutSeconds) * time.Second
}
