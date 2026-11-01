// adr: 1027
package dbguest

import (
	"slices"
	"strconv"
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
		{name: "promote", req: Request{Op: OpPromote}},
		{name: "promote with sql", req: Request{Op: OpPromote, SQL: "select 1"}, wantErr: true},
		{name: "replication report", req: Request{Op: OpReplication}},
		{name: "replication sync", req: Request{Op: OpReplication, Replication: ReplicationSync}},
		{name: "replication off", req: Request{Op: OpReplication, Replication: ReplicationOff}},
		{name: "replication unknown mode", req: Request{Op: OpReplication, Replication: "quorum"}, wantErr: true},
		{name: "replication with sql", req: Request{Op: OpReplication, SQL: "select 1"}, wantErr: true},
		{name: "mode on another op", req: Request{Op: OpStatus, Replication: ReplicationSync}, wantErr: true},
		{name: "import", req: Request{Op: OpImport, Import: validImport()}},
		{name: "import status", req: Request{Op: OpImportStatus}},
		{name: "import without source", req: Request{Op: OpImport}, wantErr: true},
		{name: "source on another op", req: Request{Op: OpStatus, Import: validImport()}, wantErr: true},
		{name: "import quoted database", req: Request{Op: OpImport, Import: withImport(func(s *ImportSource) { s.Database = "app; drop" })}, wantErr: true},
		{name: "import into postgres", req: Request{Op: OpImport, Import: withImport(func(s *ImportSource) { s.TargetDatabase = "postgres" })}, wantErr: true},
		{name: "import uppercase user", req: Request{Op: OpImport, Import: withImport(func(s *ImportSource) { s.User = "Admin" })}, wantErr: true},
		{name: "import empty password", req: Request{Op: OpImport, Import: withImport(func(s *ImportSource) { s.Password = "" })}, wantErr: true},
		{name: "import newline password", req: Request{Op: OpImport, Import: withImport(func(s *ImportSource) { s.Password = "a\nb" })}, wantErr: true},
		{name: "import owned by the superuser", req: Request{Op: OpImport, Import: withImport(func(s *ImportSource) { s.Owner = "postgres" })}, wantErr: true},
		{name: "import without an operation", req: Request{Op: OpImport, Import: withImport(func(s *ImportSource) { s.OperationID = "" })}, wantErr: true},
		{name: "import uppercase operation", req: Request{Op: OpImport, Import: withImport(func(s *ImportSource) { s.OperationID = strings.ToUpper(s.OperationID) })}, wantErr: true},
		{name: "mark drill", req: Request{Op: OpMarkDrill, Drill: "0b0b8c1e-5f0a-4d36-9a7e-1c2d3e4f5a6b"}},
		{name: "check drill", req: Request{Op: OpCheckDrill, Drill: "0b0b8c1e-5f0a-4d36-9a7e-1c2d3e4f5a6b"}},
		{name: "mark without a drill", req: Request{Op: OpMarkDrill}, wantErr: true},
		{name: "mark a quoted drill", req: Request{Op: OpMarkDrill, Drill: "x'); drop table t; --"}, wantErr: true},
		{name: "check an uppercase drill", req: Request{Op: OpCheckDrill, Drill: "0B0B8C1E-5F0A-4D36-9A7E-1C2D3E4F5A6B"}, wantErr: true},
		{name: "mark with sql", req: Request{Op: OpMarkDrill, Drill: "0b0b8c1e-5f0a-4d36-9a7e-1c2d3e4f5a6b", SQL: "select 1"}, wantErr: true},
		{name: "drill on another op", req: Request{Op: OpStatus, Drill: "0b0b8c1e-5f0a-4d36-9a7e-1c2d3e4f5a6b"}, wantErr: true},
		{name: "import bad extension", req: Request{Op: OpImport, Import: withImport(func(s *ImportSource) { s.SkipExtensions = []string{"neon; x"} })}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.req.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func validImport() *ImportSource {
	return &ImportSource{Database: "gregale", User: "gregale_mig_abc", Password: "p@ss w0rd'\"", TargetDatabase: "gregale", Owner: "gregale_schema_owner",
		SkipExtensions: []string{"neon"}, OperationID: "0b0b8c1e-5f0a-4d36-9a7e-1c2d3e4f5a6b"}
}

func withImport(mutate func(*ImportSource)) *ImportSource {
	s := validImport()
	mutate(s)
	return s
}

// Only plain identifiers leave the guest, and only a bounded number.
func TestReportableExtensions(t *testing.T) {
	got := ReportableExtensions([]string{"pg_stat_statements", "host=10.0.0.1 password=x", "Neon", "", "timescaledb"})
	if !slices.Equal(got, []string{"pg_stat_statements", "timescaledb"}) {
		t.Fatalf("reportable = %q", got)
	}
	many := make([]string, 40)
	for i := range many {
		many[i] = "ext_" + strconv.Itoa(i)
	}
	if got := ReportableExtensions(many); len(got) != maxReportedExtensions || got[0] != "ext_0" {
		t.Fatalf("reportable of 40 = %d names", len(got))
	}
}

func TestUpgradeFromArg(t *testing.T) {
	line := "console=ttyS0 " + ModeArg + " " + MajorArgKey + "=17 " + UpgradeFromArg(14)
	if from, err := UpgradeFrom(line, 17); err != nil || from != 14 {
		t.Fatalf("upgrade from = %d, %v", from, err)
	}
	if from, err := UpgradeFrom("console=ttyS0 "+ModeArg, 17); err != nil || from != 0 {
		t.Fatalf("no upgrade = %d, %v", from, err)
	}
	for _, bad := range []string{UpgradeFromArg(17), UpgradeFromArg(18), UpgradeFromArg(MinMajor - 1), UpgradeFromArgKey + "=x"} {
		if _, err := UpgradeFrom(bad, 17); err == nil {
			t.Errorf("accepted %q for major 17", bad)
		}
	}
}
