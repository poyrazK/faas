package objectstorage

import (
	"context"
	"time"
)

type ObjectAttributesRequest struct {
	Key                        string
	ProviderVersionID          string `json:"-"`
	Attributes                 []string
	PartNumberMarker, MaxParts int32
}

type ObjectChecksums struct {
	CRC32, CRC32C, CRC64NVME, SHA1, SHA256, Type string
}

type ObjectAttributePart struct {
	Number    int32
	Size      int64
	Checksums ObjectChecksums
}

type ObjectAttributeParts struct {
	Count, Marker, NextMarker, MaxParts int32
	Truncated                           bool
	Parts                               []ObjectAttributePart
}

type ObjectAttributes struct {
	ETag, StorageClass string
	ProviderVersionID  string `json:"-"`
	Size               *int64
	LastModified       time.Time
	Checksums          *ObjectChecksums
	Parts              *ObjectAttributeParts
}

type ObjectAttributesProvider interface {
	GetObjectAttributes(context.Context, string, ObjectAttributesRequest) (ObjectAttributes, error)
}

// ObjectAttributesDeleteMarker carries only validated native marker metadata.
// The gateway maps the private version before returning it to the customer.
type ObjectAttributesDeleteMarker struct {
	ProviderVersionID string `json:"-"`
	LastModified      time.Time
}

func (*ObjectAttributesDeleteMarker) Error() string { return "object is a delete marker" }
