package apidsource

// UploadedImage describes a committed single-image OCI archive that apid
// inspected (ADR-20261127120000).
type UploadedImage struct {
	ManifestDigest string
	// Name is the local repository tag, informational only.
	Name string
	// Port is the image's single exposed TCP port, or 0 for the default.
	Port int
}
