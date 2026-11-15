package apidserver

// ScanDockerfileForStatefulShape exposes the accept-time Dockerfile scan.
func ScanDockerfileForStatefulShape(dockerfile []byte) string {
	return scanDockerfileForStatefulShape(dockerfile)
}
