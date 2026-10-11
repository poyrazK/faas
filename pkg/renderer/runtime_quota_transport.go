package renderer

// adr: 793
import (
	"strconv"

	"github.com/onebox-faas/faas/pkg/manifest"
)

func runtimeQuotaTableValue(c *manifest.RuntimeQuotaTransportConfig, key string) string {
	switch key {
	case "directory_issuer_id":
		return c.DirectoryIssuerID
	case "private_key_path":
		return c.PrivateKeyPath
	case "trust_bundle_path":
		return c.TrustBundlePath
	case "regional_api_base":
		return c.RegionalAPIBase
	case "delivery_worker":
		return strconv.FormatBool(c.DeliveryWorker)
	}
	return ""
}
