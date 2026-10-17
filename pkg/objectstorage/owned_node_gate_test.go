package objectstorage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var ownedNodeTestFlags = []string{
	OwnedNodeEnabledEnv, OwnedNodeServerEnabledEnv, OwnedNodeBackendsEnabledEnv,
	OwnedNodeSettlementsEnabledEnv, OwnedNodeUsageCountersEnabledEnv,
}

func TestOwnedNodeGateDefaultsOff(t *testing.T) {
	for _, name := range ownedNodeTestFlags {
		t.Run(name, func(t *testing.T) {
			if !IsOwnedNodeFeatureEnv(name) {
				t.Fatal("qualification cannot isolate this flag")
			}
			for _, value := range []string{"", "0", "false", "off", "no", "enabled", "tru", "2"} {
				err := requireOwnedNodeFlags(func(string) string { return value }, name)
				if !errors.Is(err, ErrOwnedNodeDisabled) || !errors.Is(err, ErrConfiguration) || !strings.Contains(err.Error(), name) {
					t.Fatal("private feature enabled or missing diagnostic", value, err)
				}
			}
			for _, value := range []string{"1", "true", "yes", "on", " TRUE "} {
				if err := requireOwnedNodeFlags(func(string) string { return value }, name); err != nil {
					t.Fatal("explicit development opt-in rejected", value, err)
				}
			}
		})
	}
	if OwnedNodeEnabled(func(string) string { return "" }) || !OwnedNodeEnabled(func(string) string { return "1" }) {
		t.Fatal("master flag parsing changed")
	}
	for _, name := range []string{"s3_enabled", "FAAS_OBJECT_NODE_ACCESS_KEY_ID", "FAAS_OBJECT_NODE_ENABLED_EXTRA"} {
		if IsOwnedNodeFeatureEnv(name) {
			t.Fatal("unrelated environment variable is a private feature", name)
		}
	}
}

func TestOwnedNodeServerGateDependencies(t *testing.T) {
	for mask := range 1 << len(ownedNodeTestFlags) {
		flags := nodeGateValues(mask)
		err := RequireOwnedNodeServer(func(name string) string { return flags[name] })
		want := flags[OwnedNodeEnabledEnv] == "1" && flags[OwnedNodeServerEnabledEnv] == "1"
		if want && err != nil || !want && !errors.Is(err, ErrOwnedNodeDisabled) {
			t.Fatal("unexpected serving decision", flags, err)
		}
	}
}

func TestLoadOwnedNodeRequiresSeparateGateBeforeCredentials(t *testing.T) {
	for _, kind := range []string{"receipts", "settlements", "counters"} {
		t.Run(kind, func(t *testing.T) {
			b := BackendConfig{ID: "private", Driver: "s3", Region: "us-east-1", S3Region: "us-east-1", Namespace: "development", Endpoint: "http://127.0.0.1:19000", AllowHTTP: true, AccessKeyEnv: "NODE_KEY", SecretKeyEnv: "NODE_SECRET"}
			switch kind {
			case "receipts":
				b.OwnedNodeReceipts = true
			case "settlements":
				b.OwnedNodeSettlements = true
			case "counters":
				b.OwnedNodeUsageCounters = true
			}
			path := writeNodeGateConfig(t, b)
			for _, value := range []string{"", "0", "false", "invalid"} {
				credentials := 0
				registry, err := Load(func(name string) string {
					switch name {
					case "FAAS_OBJECT_STORAGE_CONFIG":
						return path
					case OwnedNodeEnabledEnv:
						return value
					case "NODE_KEY", "NODE_SECRET":
						credentials++
						return "synthetic"
					case "s3_enabled":
						return "true"
					}
					return ""
				})
				if !errors.Is(err, ErrOwnedNodeDisabled) || registry != nil || credentials != 0 {
					t.Fatal("private backend escaped its gate", value, registry, err, credentials)
				}
			}
		})
	}
}

func TestLoadOwnedNodeExplicitDevelopmentAndManagedS3(t *testing.T) {
	for _, owned := range []bool{false, true} {
		b := BackendConfig{ID: "backend", Driver: "s3", Region: "us-east-1", S3Region: "us-east-1", Namespace: "isolated", Endpoint: "https://provider.example.test", AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET", OwnedNodeReceipts: owned}
		path := writeNodeGateConfig(t, b)
		registry, err := Load(func(name string) string {
			switch name {
			case "FAAS_OBJECT_STORAGE_CONFIG":
				return path
			case OwnedNodeEnabledEnv, OwnedNodeBackendsEnabledEnv:
				if owned {
					return "1"
				}
			case "KEY", "SECRET":
				return "synthetic"
			}
			return ""
		})
		if err != nil || registry == nil {
			t.Fatal(owned, err)
		}
		if _, err = registry.Default("us-east-1"); err != nil {
			t.Fatal(err)
		}
	}
	for _, driver := range []string{"s3", "gcs"} {
		if err := validateLoadedOwnedNodes(Config{Backends: []BackendConfig{{Driver: driver}}}, func(string) string { t.Fatal("external provider queried the private-node gate"); return "" }); err != nil {
			t.Fatal(driver, err)
		}
	}
}

func TestLoadOwnedNodeCapabilityMatrix(t *testing.T) {
	for _, settlements := range []bool{false, true} {
		for _, counters := range []bool{false, true} {
			b := BackendConfig{ID: "private", Driver: "s3", Region: "us-east-1", S3Region: "us-east-1", Namespace: "isolated", Endpoint: "https://provider.example.test", AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET", OwnedNodeReceipts: true, OwnedNodeSettlements: settlements, OwnedNodeUsageCounters: counters}
			path := writeNodeGateConfig(t, b)
			for mask := range 1 << len(ownedNodeTestFlags) {
				flags := nodeGateValues(mask)
				flags["FAAS_OBJECT_STORAGE_CONFIG"] = path
				want := flags[OwnedNodeEnabledEnv] == "1" && flags[OwnedNodeBackendsEnabledEnv] == "1" && (!settlements || flags[OwnedNodeSettlementsEnabledEnv] == "1") && (!counters || flags[OwnedNodeUsageCountersEnabledEnv] == "1")
				credentials := 0
				registry, err := Load(func(name string) string {
					if name == "KEY" || name == "SECRET" {
						credentials++
						return "synthetic"
					}
					return flags[name]
				})
				if !want {
					if !errors.Is(err, ErrOwnedNodeDisabled) || registry != nil || credentials != 0 {
						t.Fatal("private capability escaped its dependencies", settlements, counters, flags, err, credentials)
					}
					continue
				}
				if err != nil || registry == nil || credentials != 2 {
					t.Fatal("explicit capability opt-in failed", settlements, counters, flags, err, credentials)
				}
				backend, err := registry.Default(b.Region)
				if err != nil {
					t.Fatal(err)
				}
				provider := backend.Provider.(*S3)
				if !provider.ownedNodeReceipts || provider.ownedNodeSettlements != settlements || provider.ownedNodeUsageCounters != counters {
					t.Fatal("feature gates changed configured backend capabilities")
				}
			}
		}
	}
}

func TestLoadOwnedNodeRejectsInvalidCapabilityBeforeCredentials(t *testing.T) {
	b := BackendConfig{ID: "private", Driver: "s3", Region: "us-east-1", S3Region: "us-east-1", Namespace: "isolated", Endpoint: "https://provider.example.test", AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET", OwnedNodeReceipts: true, OwnedNodeSettlements: true, OwnedNodeUsageCounters: true}
	path := writeNodeGateConfig(t, b)
	for _, disabled := range []string{OwnedNodeBackendsEnabledEnv, OwnedNodeSettlementsEnabledEnv, OwnedNodeUsageCountersEnabledEnv} {
		for _, value := range []string{"", "false", "invalid"} {
			registry, err := Load(func(name string) string {
				switch {
				case name == "FAAS_OBJECT_STORAGE_CONFIG":
					return path
				case name == disabled:
					return value
				case IsOwnedNodeFeatureEnv(name):
					return "1"
				default:
					t.Fatal("disabled capability read credentials", name)
					return ""
				}
			})
			if !errors.Is(err, ErrOwnedNodeDisabled) || registry != nil || !strings.Contains(err.Error(), disabled) {
				t.Fatal("missing capability was not identified", disabled, value, err)
			}
		}
	}
}

func TestLoadChecksEveryOwnedBackendBeforeAnyProviderCredentials(t *testing.T) {
	managed := BackendConfig{ID: "managed", Driver: "s3", Region: "us-east-1", S3Region: "us-east-1", Namespace: "managed", Endpoint: "https://provider.example.test", AccessKeyEnv: "MANAGED_KEY", SecretKeyEnv: "MANAGED_SECRET"}
	private := managed
	private.ID, private.OwnedNodeReceipts = "private", true
	path := writeNodeGateConfig(t, managed, private)
	registry, err := Load(func(name string) string {
		switch name {
		case "FAAS_OBJECT_STORAGE_CONFIG":
			return path
		case OwnedNodeEnabledEnv:
			return "1"
		case OwnedNodeBackendsEnabledEnv:
			return ""
		default:
			t.Fatal("a provider read credentials before all gates passed", name)
			return ""
		}
	})
	if registry != nil || !errors.Is(err, ErrOwnedNodeDisabled) || !strings.Contains(err.Error(), OwnedNodeBackendsEnabledEnv) {
		t.Fatal("a later backend escaped startup validation", registry, err)
	}
}

func nodeGateValues(mask int) map[string]string {
	flags := map[string]string{"s3_enabled": "true"}
	for bit, name := range ownedNodeTestFlags {
		if mask&(1<<bit) != 0 {
			flags[name] = "1"
		}
	}
	return flags
}

func writeNodeGateConfig(t *testing.T, b BackendConfig, more ...BackendConfig) string {
	t.Helper()
	body, err := json.Marshal(Config{DefaultRegion: b.Region, Defaults: map[string]string{b.Region: b.ID}, Backends: append([]BackendConfig{b}, more...)})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "object-storage.json")
	if err = os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
