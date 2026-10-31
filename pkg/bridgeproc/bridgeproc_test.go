package bridgeproc

import (
	"strings"
	"testing"
)

const unprivilegedStatus = `Name:	vmmd-raw-bridge
Uid:	30007	30007	30007	30007
Gid:	30007	30007	30007	30007
Groups:	
CapInh:	0000000000000000
CapPrm:	0000000000000000
CapEff:	0000000000000000
CapBnd:	000001ffffffffff
CapAmb:	0000000000000000
NoNewPrivs:	1
`

func TestCheckStatus(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		uid     int
		wantErr string
	}{
		{name: "unprivileged", status: unprivilegedStatus, uid: 30007},
		{name: "other uid", status: unprivilegedStatus, uid: 30008, wantErr: "Uid"},
		{name: "root", status: strings.ReplaceAll(unprivilegedStatus, "30007", "0"), uid: 30007, wantErr: "Uid"},
		{name: "saved uid kept root", status: strings.Replace(unprivilegedStatus, "Uid:	30007	30007	30007	30007", "Uid:	30007	30007	0	30007", 1), uid: 30007, wantErr: "Uid"},
		{name: "gid not dropped", status: strings.Replace(unprivilegedStatus, "Gid:	30007	30007	30007	30007", "Gid:	0	0	0	0", 1), uid: 30007, wantErr: "Gid"},
		{name: "supplementary group", status: strings.Replace(unprivilegedStatus, "Groups:	", "Groups:	0 993", 1), uid: 30007, wantErr: "supplementary"},
		{name: "effective capability", status: strings.Replace(unprivilegedStatus, "CapEff:	0000000000000000", "CapEff:	0000000000200000", 1), uid: 30007, wantErr: "CapEff"},
		{name: "ambient capability", status: strings.Replace(unprivilegedStatus, "CapAmb:	0000000000000000", "CapAmb:	0000000000000400", 1), uid: 30007, wantErr: "CapAmb"},
		{name: "bounding set is allowed", status: unprivilegedStatus, uid: 30007},
		{name: "missing line", status: strings.Replace(unprivilegedStatus, "CapAmb:	0000000000000000\n", "", 1), uid: 30007, wantErr: "no CapAmb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkStatus(tt.status, tt.uid)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("checkStatus: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("checkStatus error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseExpectedUID(t *testing.T) {
	for _, raw := range []string{"", "0", "-1", "root", "30000x"} {
		if _, err := parseExpectedUID(raw); err == nil {
			t.Errorf("parseExpectedUID(%q) succeeded", raw)
		}
	}
	if uid, err := parseExpectedUID("30000"); err != nil || uid != 30000 {
		t.Fatalf("parseExpectedUID(30000) = %d, %v", uid, err)
	}
}

func TestHardenWithoutRequestsIsNoop(t *testing.T) {
	if err := harden(func(string) string { return "" }); err != nil {
		t.Fatalf("harden: %v", err)
	}
}
