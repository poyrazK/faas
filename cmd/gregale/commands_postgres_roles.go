package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

// Custom Postgres group roles (ADR-20261116213000): NOLOGIN roles a managed
// database's own logins can SET ROLE to, created by Gregale.

const postgresRolesUsage = "usage: gregale postgres roles <list|create|grant|revoke|delete> <database> [<role>] ..."

// postgresRoleGrantable are the access modes a custom role can be granted to.
var postgresRoleGrantable = []string{"migration", "read_write", "read_only"}

// postgresRoleDefaultable are the access modes whose logins can start as a
// custom role (ADR-20261116213000 amendment 2).
var postgresRoleDefaultable = []string{"read_write", "read_only"}

// parsePostgresRoleDefaults validates --default-for against the role's grants.
func parsePostgresRoleDefaults(value string, grants []string) ([]string, error) {
	defaults := []string{}
	for _, mode := range parsePostgresRoleList(value) {
		if mode == "session" {
			mode = "read_write"
		}
		if !slices.Contains(postgresRoleDefaultable, mode) {
			return nil, fmt.Errorf("%q cannot start as a custom role (choose from %s)", mode, strings.Join(postgresRoleDefaultable, ", "))
		}
		if !slices.Contains(grants, mode) {
			return nil, fmt.Errorf("the role is not granted to %s; add it to --grant-to first", mode)
		}
		if !slices.Contains(defaults, mode) {
			defaults = append(defaults, mode)
		}
	}
	return defaults, nil
}

// flagSet reports whether name was given on the command line.
func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func cmdPostgresRoles(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, postgresRolesUsage, "postgres")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdPostgresRolesList(args[1:])
	case subCreate:
		return cmdPostgresRolesCreate(args[1:])
	case "grant":
		return cmdPostgresRolesGrant(args[1:], true)
	case "revoke":
		return cmdPostgresRolesGrant(args[1:], false)
	case "delete":
		return cmdPostgresRolesDelete(args[1:])
	default:
		printCommandValidation(os.Stderr, "unknown postgres roles subcommand %q\n", args[0])
		return unknownSubcommandChoices(os.Stderr, "postgres roles", nestedCliCommand("postgres", "roles"), args[0])
	}
}

// parsePostgresRoleList splits a comma-separated list, dropping blanks.
func parsePostgresRoleList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" && !slices.Contains(out, item) {
			out = append(out, item)
		}
	}
	return out
}

func validPostgresRoleGrants(grants []string) error {
	for _, grant := range grants {
		if grant == "session" {
			continue
		}
		if !slices.Contains(postgresRoleGrantable, grant) {
			return fmt.Errorf("%q is not an access mode a role can be granted to (choose from %s)", grant, strings.Join(postgresRoleGrantable, ", "))
		}
	}
	return nil
}

func cmdPostgresRolesList(args []string) int {
	databaseRef, ok := onePostgresID("postgres roles list", args)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	databaseID, err := postgresDatabaseID(context.Background(), client, databaseRef)
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	roles, err := client.ListManagedPostgresCustomRoles(context.Background(), databaseID)
	if err != nil {
		return printErr("Could not list Postgres roles", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(roles))
	}
	renderPostgresRoles(osStdout, roles.Items)
	return 0
}

func renderPostgresRoles(w io.Writer, roles []api.ManagedPostgresCustomRole) {
	if len(roles) == 0 {
		_, _ = fmt.Fprintln(w, "No custom roles. Create one with: gregale postgres roles create <database> <role>")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ROLE\tBYPASSRLS\tGRANTED TO\tDEFAULT FOR\tMEMBER OF\tSTATE")
	for _, role := range roles {
		state := role.State
		if role.LastErrorCode != "" && role.State != "ready" {
			state += " (" + role.LastErrorCode + ")"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", role.Name, yesNo(role.BypassRLS), dashIfEmpty(strings.Join(role.GrantedTo, ",")),
			dashIfEmpty(strings.Join(role.DefaultFor, ",")), dashIfEmpty(strings.Join(role.MemberOf, ",")), state)
	}
	_ = tw.Flush()
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func renderPostgresRole(w io.Writer, role api.ManagedPostgresCustomRole, verb string) {
	switch role.State {
	case "ready":
		_, _ = fmt.Fprintf(w, "Role %s %s.\n", role.Name, verb)
	case "deleting":
		_, _ = fmt.Fprintf(w, "Role %s is being dropped", role.Name)
		if role.LastErrorCode != "" {
			_, _ = fmt.Fprintf(w, " (last attempt: %s); Gregale retries automatically", role.LastErrorCode)
		}
		_, _ = fmt.Fprintln(w, ".")
		return
	default:
		_, _ = fmt.Fprintf(w, "Role %s is saved but not applied yet", role.Name)
		if role.LastErrorCode != "" {
			_, _ = fmt.Fprintf(w, " (last attempt: %s)", role.LastErrorCode)
		}
		_, _ = fmt.Fprintln(w, "; Gregale retries automatically. Check with: gregale postgres roles list "+role.DatabaseID)
		return
	}
	if len(role.GrantedTo) > 0 {
		_, _ = fmt.Fprintf(w, "Logins of %s can now run SET ROLE %s.\n", strings.Join(role.GrantedTo, ", "), role.Name)
	}
	if len(role.DefaultFor) > 0 {
		_, _ = fmt.Fprintf(w, "Logins of %s start every session as %s.\n", strings.Join(role.DefaultFor, ", "), role.Name)
	}
	if role.BypassRLS {
		_, _ = fmt.Fprintln(w, "It bypasses row-level security. Data API and Auth cannot be enabled on this database while it exists.")
	}
}

func cmdPostgresRolesCreate(args []string) int {
	flags, positional := splitArgsForFlags(args, "bypass-rls")
	fs := newFlagSet("postgres roles create", flag.ContinueOnError)
	bypass := fs.Bool("bypass-rls", false, "the role bypasses row-level security (refused on databases with Data API or Auth)")
	grantTo := fs.String("grant-to", "migration,read_write", "comma-separated access modes whose logins may SET ROLE to it: migration, read_write, read_only")
	memberOf := fs.String("member-of", "", "comma-separated custom roles of the database this role is a member of")
	defaultFor := fs.String("default-for", "", "comma-separated access modes whose logins start every session as this role: read_write, read_only (empty clears)")
	if err := fs.Parse(append(flags, positional...)); err != nil {
		return 1
	}
	if fs.NArg() != 2 {
		PrintUsage(os.Stderr, "usage: gregale postgres roles create <database> <role> [--bypass-rls] [--grant-to migration,read_write] [--member-of ROLE,...] [--default-for read_write]", "postgres")
		return 1
	}
	grants := parsePostgresRoleList(*grantTo)
	if err := validPostgresRoleGrants(grants); err != nil {
		printCommandValidation(os.Stderr, "%v\n", err)
		return 1
	}
	request := api.PutManagedPostgresCustomRoleRequest{BypassRLS: *bypass, GrantedTo: grants, MemberOf: parsePostgresRoleList(*memberOf)}
	if flagSet(fs, "default-for") {
		defaults, err := parsePostgresRoleDefaults(*defaultFor, grants)
		if err != nil {
			printCommandValidation(os.Stderr, "%v\n", err)
			return 1
		}
		request.DefaultFor = &defaults
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	databaseID, err := postgresDatabaseID(context.Background(), client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	role, err := client.PutManagedPostgresCustomRole(context.Background(), databaseID, fs.Arg(1), request)
	if err != nil {
		return printErr("Could not create the Postgres role", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(role))
	}
	renderPostgresRole(osStdout, role, "created")
	return 0
}

// cmdPostgresRolesGrant adds (grant) or removes (revoke) access modes of
// one role, keeping the rest of its declaration.
func cmdPostgresRolesGrant(args []string, grant bool) int {
	name, flagName, preposition := "postgres roles revoke", "from", "--from"
	if grant {
		name, flagName, preposition = "postgres roles grant", "to", "--to"
	}
	flags, positional := splitArgsForFlags(args, "default")
	fs := newFlagSet(name, flag.ContinueOnError)
	modes := fs.String(flagName, "", "comma-separated access modes: migration, read_write, read_only")
	makeDefault := false
	if grant {
		fs.BoolVar(&makeDefault, "default", false, "logins of these access modes also start every session as the role (read_write, read_only)")
	}
	if err := fs.Parse(append(flags, positional...)); err != nil {
		return 1
	}
	changes := parsePostgresRoleList(*modes)
	if fs.NArg() != 2 || len(changes) == 0 {
		PrintUsage(os.Stderr, "usage: gregale "+name+" <database> <role> "+preposition+" migration,read_write,read_only", "postgres")
		return 1
	}
	if err := validPostgresRoleGrants(changes); err != nil {
		printCommandValidation(os.Stderr, "%v\n", err)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	databaseID, err := postgresDatabaseID(context.Background(), client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	roles, err := client.ListManagedPostgresCustomRoles(context.Background(), databaseID)
	if err != nil {
		return printErr("Could not load the database's Postgres roles", err)
	}
	index := slices.IndexFunc(roles.Items, func(role api.ManagedPostgresCustomRole) bool { return role.Name == fs.Arg(1) })
	if index < 0 || roles.Items[index].State == "deleting" {
		return printErr("Could not find the Postgres role", errors.New("no custom role "+fs.Arg(1)+" on this database (gregale postgres roles list "+fs.Arg(0)+")"))
	}
	current := roles.Items[index]
	grants := slices.Clone(current.GrantedTo)
	for _, mode := range changes {
		if mode == "session" {
			mode = "read_write"
		}
		if grant && !slices.Contains(grants, mode) {
			grants = append(grants, mode)
		}
		if !grant {
			grants = slices.DeleteFunc(grants, func(existing string) bool { return existing == mode })
		}
	}
	request := api.PutManagedPostgresCustomRoleRequest{BypassRLS: current.BypassRLS, GrantedTo: grants, MemberOf: current.MemberOf}
	if makeDefault {
		defaults, err := parsePostgresRoleDefaults(strings.Join(append(slices.Clone(current.DefaultFor), changes...), ","), grants)
		if err != nil {
			printCommandValidation(os.Stderr, "%v\n", err)
			return 1
		}
		request.DefaultFor = &defaults
	}
	role, err := client.PutManagedPostgresCustomRole(context.Background(), databaseID, current.Name, request)
	if err != nil {
		return printErr("Could not change the Postgres role", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(role))
	}
	renderPostgresRole(osStdout, role, "updated")
	return 0
}

func cmdPostgresRolesDelete(args []string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("postgres roles delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm without prompting")
	if err := fs.Parse(append(flags, positional...)); err != nil {
		return 1
	}
	if fs.NArg() != 2 {
		PrintUsage(os.Stderr, "usage: gregale postgres roles delete <database> <role> [--yes]", "postgres")
		return 1
	}
	if code := requireAutomationConfirmation(*yes, "--yes"); code != 0 {
		return code
	}
	if !*yes && jsonOutput {
		return printErr("Confirmation required", errors.New("postgres roles delete requires --yes in JSON mode"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	databaseID, err := postgresDatabaseID(context.Background(), client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	if !*yes {
		_, _ = fmt.Fprintf(osStderr, "Dropping role %s revokes it from every login and hands what it owns to the database's schema owner.\nType the role name to confirm: ", fs.Arg(1))
		if !requireTyped(fs.Arg(1)) {
			return 130
		}
	}
	role, err := client.DeleteManagedPostgresCustomRole(context.Background(), databaseID, fs.Arg(1))
	if err != nil {
		return printErr("Could not delete the Postgres role", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(role))
	}
	if role.State == "deleted" {
		_, _ = fmt.Fprintf(osStdout, "Role %s dropped.\n", role.Name)
		return 0
	}
	renderPostgresRole(osStdout, role, "dropped")
	return 0
}
