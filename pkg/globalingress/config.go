package globalingress

// adr: 784
import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Config is an opt-in directory projection consumer, never a runtime owner.
type Config struct {
	DirectoryID, AppsDomain, ManifestPath, TrustBundlePath string
}

func (c Config) Enabled() bool {
	return c.DirectoryID != "" || c.AppsDomain != "" || c.ManifestPath != "" || c.TrustBundlePath != ""
}

func (c Config) Validate(regionalDomain string) error {
	if !c.Enabled() {
		return nil
	}
	id, err := uuid.Parse(c.DirectoryID)
	if err != nil || id == uuid.Nil || id.String() != c.DirectoryID || !ValidDomain(c.AppsDomain) || !filepath.IsAbs(c.ManifestPath) || !filepath.IsAbs(c.TrustBundlePath) {
		return fmt.Errorf("global ingress requires a canonical directory UUID, apps domain and absolute manifest/trust bundle paths: %w", ErrUnavailable)
	}
	regionalDomain = strings.ToLower(strings.TrimSuffix(regionalDomain, "."))
	if regionalDomain != "" && (regionalDomain == c.AppsDomain || strings.HasSuffix(regionalDomain, "."+c.AppsDomain) || strings.HasSuffix(c.AppsDomain, "."+regionalDomain)) {
		return fmt.Errorf("global ingress apps domain overlaps regional apps domain: %w", ErrUnavailable)
	}
	if c.ManifestPath == c.TrustBundlePath {
		return fmt.Errorf("global ingress manifest and trust bundle paths must differ: %w", ErrUnavailable)
	}
	return nil
}
