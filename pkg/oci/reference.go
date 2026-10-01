package oci

// adr: 387. Reference parsing is shared with private evidence storage.
import "github.com/onebox-faas/faas/pkg/ociref"
const (
 defaultRegistry = "docker.io"
 dockerAPIHost = "registry-1.docker.io"
 defaultTag = "latest"
 digestAlgo = "sha256:"
 digestHexLen = 64
)
type Reference = ociref.Reference
func ParseReference(s string) (Reference,error) { return ociref.ParseReference(s) }
func validateDigest(s string) error { return ociref.ValidateDigest(s) }
func isLowerHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' }
