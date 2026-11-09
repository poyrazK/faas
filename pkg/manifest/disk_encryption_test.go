package manifest

import (
	"strings"
	"testing"
)

const testAgeRecipient = "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"

// ADR-20261109120000: encrypted data volumes need an operator recovery
// recipient and at least one other host to unlock from.
func TestDiskEncryptionValidation(t *testing.T) {
	two := []Host{{Name: "cp-1"}, {Name: "compute-1"}}
	for _, tc := range []struct {
		name  string
		hosts []Host
		d     *DiskEncryption
		want  string // substring of the error; empty means valid
	}{
		{name: "absent", hosts: two[:1]},
		{name: "valid", hosts: two, d: &DiskEncryption{RecoveryRecipients: []string{testAgeRecipient}}},
		{name: "custom port", hosts: two, d: &DiskEncryption{TangPort: 7600, RecoveryRecipients: []string{testAgeRecipient}}},
		{name: "low port", hosts: two, d: &DiskEncryption{TangPort: 80, RecoveryRecipients: []string{testAgeRecipient}}, want: "disk_encryption.tang_port"},
		{name: "no recipients", hosts: two, d: &DiskEncryption{}, want: "at least one operator age recipient"},
		{name: "identity not recipient", hosts: two, d: &DiskEncryption{RecoveryRecipients: []string{"AGE-SECRET-KEY-1QQQ"}}, want: "recovery_recipients[0]"},
		{name: "single host", hosts: two[:1], d: &DiskEncryption{RecoveryRecipients: []string{testAgeRecipient}}, want: "at least two hosts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Fleet: Fleet{Hosts: tc.hosts}, DiskEncryption: tc.d}
			errs := m.validateDiskEncryption()
			if tc.want == "" && len(errs) > 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
			if tc.want != "" && !strings.Contains(errs.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %q", tc.want, errs.Error())
			}
		})
	}
}

func TestDiskEncryptionPortDefault(t *testing.T) {
	var none *DiskEncryption
	if none.Port() != DefaultTangPort || (&DiskEncryption{}).Port() != DefaultTangPort || (&DiskEncryption{TangPort: 7600}).Port() != 7600 {
		t.Fatal("Port() must default to 7500 and keep an explicit port")
	}
}
