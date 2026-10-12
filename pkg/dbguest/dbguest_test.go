// adr: 1027
package dbguest

import (
	"strings"
	"testing"
)

func TestParseCmdline(t *testing.T) {
	cases := []struct {
		name     string
		cmdline  string
		database bool
		major    int
		wantErr  bool
	}{
		{name: "app boot", cmdline: "console=ttyS0 root=/dev/vda ro init=/sbin/init"},
		{name: "database 17", cmdline: "root=/dev/vda ro gregale.mode=database gregale.pg_major=17", database: true, major: 17},
		{name: "major first", cmdline: "gregale.pg_major=14 gregale.mode=database", database: true, major: 14},
		{name: "missing major", cmdline: "gregale.mode=database", database: true, wantErr: true},
		{name: "too old", cmdline: "gregale.mode=database gregale.pg_major=12", database: true, wantErr: true},
		{name: "too new", cmdline: "gregale.mode=database gregale.pg_major=19", database: true, wantErr: true},
		{name: "not a number", cmdline: "gregale.mode=database gregale.pg_major=17x", database: true, wantErr: true},
		{name: "major without mode is ignored", cmdline: "gregale.pg_major=17"},
		{name: "prefix lookalike is not the mode", cmdline: "gregale.mode=databases gregale.pg_major=17"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, major, err := ParseCmdline(tc.cmdline)
			if database != tc.database || (err != nil) != tc.wantErr || (!tc.wantErr && major != tc.major) {
				t.Fatalf("ParseCmdline(%q) = %v, %d, %v; want %v, %d, err=%v", tc.cmdline, database, major, err, tc.database, tc.major, tc.wantErr)
			}
		})
	}
}

func TestBootArgsRoundTrip(t *testing.T) {
	for major := MinMajor; major <= MaxMajor; major++ {
		args, err := BootArgs(major)
		if err != nil {
			t.Fatalf("BootArgs(%d): %v", major, err)
		}
		database, got, err := ParseCmdline("root=/dev/vda ro " + args)
		if err != nil || !database || got != major {
			t.Fatalf("round trip %d: database=%v major=%d err=%v", major, database, got, err)
		}
	}
	if _, err := BootArgs(MaxMajor + 1); err == nil {
		t.Fatal("BootArgs accepted an unsupported major")
	}
}

func TestRequestValidate(t *testing.T) {
	cases := []struct {
		name    string
		req     Request
		wantErr bool
	}{
		{name: "status", req: Request{Op: OpStatus}},
		{name: "stop", req: Request{Op: OpStop}},
		{name: "bootstrap", req: Request{Op: OpBootstrap}},
		{name: "admin sql", req: Request{Op: OpAdminSQL, SQL: "create role app login"}},
		{name: "bootstrap with sql", req: Request{Op: OpBootstrap, SQL: "select 1"}, wantErr: true},
		{name: "status with sql", req: Request{Op: OpStatus, SQL: "select 1"}, wantErr: true},
		{name: "admin sql empty", req: Request{Op: OpAdminSQL, SQL: "  "}, wantErr: true},
		{name: "admin sql too large", req: Request{Op: OpAdminSQL, SQL: strings.Repeat("x", MaxAdminSQLBytes+1)}, wantErr: true},
		{name: "admin sql NUL", req: Request{Op: OpAdminSQL, SQL: "select 1\x00"}, wantErr: true},
		{name: "unknown op", req: Request{Op: "drop"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.req.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
