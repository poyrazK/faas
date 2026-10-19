package state

import "github.com/onebox-faas/faas/pkg/api"

func hostingPayloadBytes(m api.HostingManifest) int64 {
	var size int64
	for _, file := range m.Files {
		size += file.Size
	}
	return size
}

func hostingReservedBytes(payload int64) int64 {
	return api.HostingStorageCopies*payload + api.HostingManifestMaxBytes
}

func checkHostingAdmission(usage api.HostingUsage, payload int64) error {
	observed := usage.ReservedBytes + hostingReservedBytes(payload)
	if observed > api.HostingAccountStorageMaxBytes {
		return &HostingQuotaError{Limit: api.HostingAccountStorageMaxBytes, Observed: observed, Resource: "storage_bytes"}
	}
	if usage.PendingUploads >= api.HostingAccountPendingUploadsMax {
		return &HostingQuotaError{Limit: api.HostingAccountPendingUploadsMax, Observed: usage.PendingUploads + 1, Resource: "pending_uploads"}
	}
	return nil
}
