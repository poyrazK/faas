// adr: 20261025111015

package manifest

import (
	"strings"
	"testing"
)

func TestObjectStorageGatewayValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    ObjectStorage
		want string
	}{
		{name: "absent"},
		{name: "off", o: ObjectStorage{Gateway: "off"}},
		{name: "managed", o: ObjectStorage{Gateway: "managed", ConfigSrc: "/operator/object-storage.json", ProviderEnvSrc: "/operator/provider.env"}},
		{name: "unknown mode", o: ObjectStorage{Gateway: "auto"}, want: "object_storage.gateway"},
		{name: "managed without config", o: ObjectStorage{Gateway: "managed"}, want: "object_storage.config_src"},
		{name: "relative config", o: ObjectStorage{Gateway: "managed", ConfigSrc: "object-storage.json"}, want: "object_storage.config_src"},
		{name: "relative env", o: ObjectStorage{Gateway: "managed", ConfigSrc: "/c.json", ProviderEnvSrc: "p.env"}, want: "object_storage.provider_env_src"},
		{name: "paths without management", o: ObjectStorage{ConfigSrc: "/c.json"}, want: "object_storage.gateway"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.o.validate()
			if tc.want == "" {
				if len(errs) != 0 {
					t.Fatalf("errors = %v", errs)
				}
				return
			}
			if len(errs) == 0 || !strings.Contains(errs[0].Path, tc.want) {
				t.Fatalf("errors = %v, want %s", errs, tc.want)
			}
		})
	}
}
