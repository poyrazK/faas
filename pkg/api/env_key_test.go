package api

import "testing"

func TestValidateEnvKeyAcceptsPOSIXNames(t *testing.T) {
	for _, key := range []string{"DOTNET_EnableDiagnostics", "DOTNET_gcServer", "http_proxy", "lower", "PATH", "A1_b2"} {
		if problem := ValidateEnvKey(key); problem != nil {
			t.Errorf("ValidateEnvKey(%q) = %v, want accepted", key, problem.Detail)
		}
	}
}

func TestValidateEnvKeyRefusesBadShapes(t *testing.T) {
	for _, key := range []string{"", "_PRIVATE", "9LIVES", "DASH-KEY", "dot.key", "space key", "ünï"} {
		if ValidateEnvKey(key) == nil {
			t.Errorf("ValidateEnvKey(%q) accepted, want refused", key)
		}
	}
}

// Reserved-name checks compare the uppercase form, so another spelling of a
// platform key must not get through as a different variable.
func TestValidateEnvKeyRefusesOtherCasesOfPlatformKeys(t *testing.T) {
	for _, key := range []string{"Port", "port", "faas_token", "Gregale_Api", "gregale_service_x"} {
		if ValidateEnvKey(key) == nil {
			t.Errorf("ValidateEnvKey(%q) accepted, want refused as reserved", key)
		}
	}
}
