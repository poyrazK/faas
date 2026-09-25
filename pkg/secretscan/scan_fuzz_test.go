package secretscan_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/secretscan"
)

// FuzzScanNeverPanics — ScanFile and ScanEnvContent run on customer source
// inside `gregale deploy`, apid and imaged. unquoteKey once indexed empty and
// one-byte keys, so ordinary source panicked the scanner.
func FuzzScanNeverPanics(f *testing.F) {
	f.Add("app.py", []byte("API_KEY = 'sk_live_abcdef0123456789'\nOPS = {'=': 1}\n"))
	f.Add(".env", []byte("export TOKEN=\"ghp_16C7e42F292c6912E7710c838347Ae178B4a\"\n"))
	f.Add("k.pem", []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----\n"))
	f.Fuzz(func(t *testing.T, name string, b []byte) {
		_ = secretscan.ScanFile(name, b)
		_ = secretscan.ScanEnvContent(name, b)
	})
}
