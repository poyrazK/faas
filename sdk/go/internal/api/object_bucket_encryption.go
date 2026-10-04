package api

import "time"

type ObjectBucketEncryptionRequest struct {
	Encryption ObjectEncryption `json:"encryption"`
}

type ObjectBucketEncryption struct {
	BucketID          string            `json:"bucket_id"`
	State             string            `json:"state"`
	Revision          int64             `json:"revision"`
	Encryption        *ObjectEncryption `json:"encryption,omitempty"`
	DesiredEncryption *ObjectEncryption `json:"desired_encryption,omitempty"`
	UpdatedAt         time.Time         `json:"updated_at"`
}
