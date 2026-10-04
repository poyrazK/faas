package api

// ObjectEncryptionCapabilities contains only public owned references. Enrollment
// does not establish current key availability or native write permissions.
type ObjectEncryptionCapabilities struct {
	Algorithms     []string `json:"algorithms"`
	KeyIDs         []string `json:"key_ids"`
	BucketDefaults bool     `json:"bucket_defaults"`
}
