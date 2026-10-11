package manifest

// adr: 793
import (
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Manifests carry key paths; signing material remains on the owning host.
type RuntimeQuotaTransportConfig struct {
	DirectoryIssuerID string `yaml:"directory_issuer_id,omitempty"`
	PrivateKeyPath    string `yaml:"private_key_path,omitempty"`
	TrustBundlePath   string `yaml:"trust_bundle_path,omitempty"`
	RegionalAPIBase   string `yaml:"regional_api_base,omitempty"`
	DeliveryWorker    bool   `yaml:"delivery_worker,omitempty"`
}

type GlobalIngressPublisherConfig struct {
	ManifestPath string `yaml:"manifest_path"`
}

func (m *Manifest) validateRuntimeQuotaTransport() Errors {
	apid := m.Daemons.Apid
	if apid == nil {
		return nil
	}
	var errs Errors
	c := apid.RuntimeQuotaTransport
	configured := c != nil && *c != (RuntimeQuotaTransportConfig{})
	directory := apid.GlobalAppDirectoryEnabled != nil && *apid.GlobalAppDirectoryEnabled
	if configured {
		path := "daemons.apid.runtime_quota_transport"
		if !validQuotaConfigPath(c.TrustBundlePath) {
			errs = append(errs, Error{path + ".trust_bundle_path", "must be a clean absolute file path"})
		}
		if directory {
			id, err := uuid.Parse(c.DirectoryIssuerID)
			if m.ControlPlaneCell != nil || err != nil || id == uuid.Nil || id.String() != c.DirectoryIssuerID || !validQuotaConfigPath(c.PrivateKeyPath) || c.PrivateKeyPath == c.TrustBundlePath || c.RegionalAPIBase != "" {
				errs = append(errs, Error{path, "directory authority requires an unbound database, canonical issuer UUID, distinct key/trust file paths and no regional API base"})
			}
		} else if m.ControlPlaneCell == nil || !api.ValidGlobalDirectoryAPIBase(c.RegionalAPIBase) || c.DirectoryIssuerID != "" || c.PrivateKeyPath != "" || c.DeliveryWorker {
			errs = append(errs, Error{path, "regional receiver requires a bound cell and canonical HTTPS API base; directory signing and delivery worker settings are forbidden"})
		}
	}
	if p := apid.GlobalIngressPublisher; p != nil && p.ManifestPath != "" {
		if !directory || m.ControlPlaneCell != nil || !configured || !validQuotaConfigPath(p.ManifestPath) {
			errs = append(errs, Error{"daemons.apid.global_ingress_publisher.manifest_path", "requires directory quota authority in an unbound database and a clean absolute output file path"})
		} else if p.ManifestPath == c.PrivateKeyPath || p.ManifestPath == c.TrustBundlePath {
			errs = append(errs, Error{"daemons.apid.global_ingress_publisher.manifest_path", "must differ from signing key and trust bundle paths"})
		}
		if gateway := m.Daemons.GatewaydInternal; gateway != nil && p.ManifestPath == gateway.GlobalIngressTrustBundlePath {
			errs = append(errs, Error{"daemons.apid.global_ingress_publisher.manifest_path", "must differ from the ingress consumer trust bundle path"})
		}
	}
	return errs
}

func validQuotaConfigPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/" && !strings.ContainsAny(path, "\x00\r\n")
}
