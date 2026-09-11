package secretscan

import (
	"strings"
	"testing"
)

func TestScanEnvContent_GregaleDeployToken(t *testing.T) {
	token := "fp" + "_deploy_" + strings.Repeat("a", 48)
	findings := ScanEnvContent(".env", []byte("GREGale_DEPLOY_TOKEN="+token+"\n"))
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want one deploy-token finding", findings)
	}
	if findings[0].Provider != "gregale_deploy_token" || findings[0].Severity != SeverityHigh {
		t.Fatalf("finding = %+v, want high gregale_deploy_token", findings[0])
	}
	if findings[0].Snippet == token {
		t.Fatal("deploy token plaintext leaked in scanner snippet")
	}
}
