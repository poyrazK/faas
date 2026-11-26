package extensionpb

import (
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

func EncodeInstalled(installed []pgextension.Installed) []*vmmdpb.DatabaseExtensionVersion {
	var out []*vmmdpb.DatabaseExtensionVersion
	for _, i := range installed {
		if len(out) >= pgextension.MaxEntries {
			break
		}
		if i.Valid() {
			out = append(out, &vmmdpb.DatabaseExtensionVersion{Name: i.Name, Version: i.Version})
		}
	}
	return out
}

func DecodeInstalled(in []*vmmdpb.DatabaseExtensionVersion) []pgextension.Installed {
	out := make([]pgextension.Installed, 0, min(len(in), pgextension.MaxEntries))
	for _, v := range in {
		if len(out) >= pgextension.MaxEntries {
			break
		}
		i := pgextension.Installed{Name: v.GetName(), Version: v.GetVersion()}
		if i.Valid() {
			out = append(out, i)
		}
	}
	return out
}

func Encode(c *pgextension.Catalog) *vmmdpb.DatabaseExtensionCatalog {
	if c == nil || !c.Valid() {
		return nil
	}
	out := &vmmdpb.DatabaseExtensionCatalog{Installed: EncodeInstalled(c.Installed)}
	for _, a := range c.Available {
		out.Available = append(out.Available, &vmmdpb.DatabaseExtensionVersion{Name: a.Name, Version: a.Version, DefaultVersion: a.Default})
	}
	return out
}

func Decode(in *vmmdpb.DatabaseExtensionCatalog) *pgextension.Catalog {
	if in == nil || len(in.GetInstalled()) > pgextension.MaxEntries || len(in.GetAvailable()) > pgextension.MaxEntries {
		return nil
	}
	out := &pgextension.Catalog{Installed: DecodeInstalled(in.GetInstalled()), Available: make([]pgextension.Available, 0, len(in.GetAvailable()))}
	for _, a := range in.GetAvailable() {
		out.Available = append(out.Available, pgextension.Available{Name: a.GetName(), Version: a.GetVersion(), Default: a.GetDefaultVersion()})
	}
	if len(out.Installed) != len(in.GetInstalled()) || !out.Valid() {
		return nil
	}
	return out
}
