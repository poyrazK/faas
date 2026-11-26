package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

func cmdPostgresAutovacuum(args []string) int {
	const usage = "usage: gregale postgres autovacuum DATABASE [--profile default|high-churn]"
	fs := newFlagSet("postgres autovacuum", 0)
	profileName := fs.String("profile", "", "select the default or high-churn profile")
	if err := fs.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	profile := dbguest.AutovacuumProfile(*profileName)
	set := flagWasSet(fs, "profile")
	if _, ok := profile.Settings(); set && !ok {
		printCommandValidation(os.Stderr, "gregale postgres autovacuum: profile must be default or high-churn\n")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	id, err := postgresConditions.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	org := client.ProjectOrganization()
	request := api.SetManagedPostgresAutovacuumRequest{Profile: profile}
	var out api.ManagedPostgresAutovacuum
	switch {
	case org != "" && set:
		out, err = client.SetOrgManagedPostgresAutovacuum(ctx, org, id, request)
	case org != "":
		out, err = client.GetOrgManagedPostgresAutovacuum(ctx, org, id)
	case set:
		out, err = client.SetManagedPostgresAutovacuum(ctx, id, request)
	default:
		out, err = client.GetManagedPostgresAutovacuum(ctx, id)
	}
	if err != nil {
		return printErr("Could not access the PostgreSQL autovacuum profile", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderPostgresAutovacuum(osStdout, out.AutovacuumStatus)
	return 0
}

func renderPostgresAutovacuum(w io.Writer, report dbguest.AutovacuumStatus) {
	_, _ = fmt.Fprintf(w, "Autovacuum profile: %s (%s)\n", report.Profile, report.State)
	if report.Effective != nil {
		_, _ = fmt.Fprintf(w, "Primary scale factors: vacuum %g, analyze %g\n",
			report.Effective.VacuumScaleFactor, report.Effective.AnalyzeScaleFactor)
	}
	if report.ReplicaEffective != nil {
		_, _ = fmt.Fprintf(w, "Standby scale factors: vacuum %g, analyze %g\n",
			report.ReplicaEffective.VacuumScaleFactor, report.ReplicaEffective.AnalyzeScaleFactor)
	}
	if report.State == dbguest.DiagnosticsParked {
		_, _ = fmt.Fprintln(w, "The profile applies at the next wake.")
	}
}
