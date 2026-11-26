package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/onebox-faas/faas/pkg/api"
)

// This query describes the existing binding's role, not the prospective API
// role. Keep it a fixed, read-only transaction: no customer SQL is executed.
const dataAPIConnectInspectionSQL = `BEGIN READ ONLY;
SET LOCAL statement_timeout = '10s';
SELECT current_user AS inspected_role, n.nspname AS schema, c.relname AS relation,
 c.relkind AS kind, c.relrowsecurity AS rls, c.relforcerowsecurity AS force_rls,
 has_table_privilege(current_user,c.oid,'SELECT') AS can_select,
 COALESCE(array_to_string(c.reloptions, ','), '') AS relation_options,
 (SELECT count(*) FROM pg_policy p WHERE p.polrelid=c.oid) AS policies,
 count(*) OVER () AS total_relations
FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','f')
ORDER BY c.relname LIMIT 1000;
COMMIT;`

// connect never adopts an app by name; a resume requires a recorded and
// independently revalidated deployment and binding.
func cmdDataAPIConnect(args []string) int {
	for _, arg := range args {
		if arg == "--requalify-plan" || arg == "-requalify-plan" || strings.HasPrefix(arg, "--requalify-plan=") || strings.HasPrefix(arg, "-requalify-plan=") || arg == "--requalify-apply" || arg == "-requalify-apply" || strings.HasPrefix(arg, "--requalify-apply=") || strings.HasPrefix(arg, "-requalify-apply=") {
			return cmdDataAPIConnectRequalify(args)
		}
	}
	fs := newFlagSet("data-api connect", flag.ContinueOnError)
	database := fs.String("database", "", "existing managed PostgreSQL database")
	inspectApp := fs.String("inspect-app", "", "optional app with an existing ready database binding")
	inspectEnv := fs.String("inspect-env", "", "binding environment key when the app has several")
	issuer := fs.String("issuer", "", "HTTPS application JWT issuer")
	jwks := fs.String("jwks-url", "", "HTTPS application JWKS URL")
	audience := fs.String("audience", "", "application JWT audience")
	origins := fs.String("origins", "", "comma-separated browser origins")
	output := fs.String("output", "", "new TypeScript types file in the application")
	numericMode := fs.String("numeric-mode", "number", "number, lossless, or exact")
	collection := fs.String("collection", "", "read-only two-subject request collection")
	prepareChecks := fs.String("prepare-checks", "", "generate a collection from a reviewed access matrix")
	identities := fs.String("identities", "", "identity-to-JWT-environment mapping")
	apply := fs.Bool("apply", false, "create the API after inspection and preflight")
	resume := fs.Bool("resume", false, "resume post-deployment verification from a receipt")
	verify := fs.Bool("verify", false, "rerun the recorded read-only authorization checks")
	status := fs.Bool("status", false, "inspect a connection receipt and recorded resources")
	receiptPath := fs.String("receipt", "", "connection receipt path (default .gregale-data-api-NAME.connect.json)")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	preparing := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "prepare-checks" {
			preparing = true
		}
	})
	if preparing {
		invalid := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name != "prepare-checks" && f.Name != "collection" {
				invalid = true
			}
		})
		if invalid || fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || *prepareChecks == "" || *collection == "" {
			return printErr("Invalid checks options", errors.New("use NAME --prepare-checks SPEC --collection NEW_FILE without other options"))
		}
		return prepareDataAPIConnectChecks(*prepareChecks, *collection)
	}
	if *resume || *status || *verify {
		invalid := false
		fs.Visit(func(f *flag.Flag) {
			allowed := f.Name == "receipt" || ((*resume || *verify) && (f.Name == "resume" || f.Name == "verify" || f.Name == "collection" || f.Name == "identities")) || (*status && f.Name == "status")
			if !allowed {
				invalid = true
			}
		})
		if invalid {
			return printErr("Invalid connection options", errors.New("resume and status use the recorded app, database, output and settings"))
		}
	}
	if jsonOutput && !*status && (*apply || *inspectApp != "") {
		return printErr("JSON output unavailable", errors.New("apply and existing-binding inspection use interactive output"))
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || (*apply && (*resume || *status || *verify)) || (*resume && (*status || *verify)) || (*status && *verify) || (!*resume && !*status && !*verify && (*database == "" || (*inspectApp != "" && !api.ValidAppSlug(*inspectApp)) || (*inspectEnv != "" && *inspectApp == ""))) || !dataAPIDiagnosticAllowed(*numericMode, "number", "lossless", "exact") {
		PrintUsage(os.Stderr, "usage: gregale data-api connect NAME (--database DATABASE [--inspect-app APP] [--apply ...] | --prepare-checks SPEC --collection FILE | --verify --collection FILE --identities FILE | --resume --collection FILE --identities FILE | --status) [--receipt FILE]", "data-api")
		return 1
	}
	if *receiptPath == "" {
		*receiptPath = ".gregale-data-api-" + fs.Arg(0) + ".connect.json"
	}
	path, err := filepath.Abs(*receiptPath)
	if err != nil {
		return printErr("Invalid receipt path", err)
	}
	var receipt dataAPIConnectReceipt
	var client *api.Client
	if *apply || *resume || *status || *verify {
		lock := flock.New(filepath.Join(os.TempDir(), "gregale-data-connect-"+dataAPIConnectHash([]byte(path))+".lock"))
		locked, lockErr := lock.TryLock()
		if lockErr != nil || !locked {
			return printErr("Connection is busy", errors.New("could not lock the receipt"))
		}
		defer func() { _ = lock.Unlock() }()
		if *resume || *status || *verify {
			receipt, err = readDataAPIConnectReceipt(path)
			if err != nil {
				return printErr("Could not read connection receipt", err)
			}
			if receipt.App != fs.Arg(0) {
				return printErr("Invalid connection receipt", errors.New("receipt names a different app"))
			}
			client, err = authedClient()
			if err != nil {
				return printErr("Not logged in", err)
			}
			if receipt.APIHash != dataAPIConnectHash([]byte(client.BaseURL())) {
				return printErr("Invalid connection receipt", errors.New("receipt belongs to a different control plane"))
			}
			observeCtx, cancelObserve := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancelObserve()
			if *status {
				return reportDataAPIConnectStatus(observeCtx, client, receipt)
			}
			if *verify && receipt.Stage != "verified" {
				return printErr("Cannot verify connection", errors.New("a verified connection receipt is required"))
			}
			if receipt.Stage == "creating" {
				return printErr("Cannot resume uncertain creation", errors.New("inspect the app and bindings manually; creation was not acknowledged in the receipt"))
			}
			if !*verify {
				if err := dataAPIConnectSnapshot(observeCtx, client, &receipt, false); err != nil {
					return printErr("Cannot resume connection", err)
				}
			}
			*output, *numericMode = receipt.Output, receipt.NumericMode
		}
	}
	var config dataAPIHostedReplayConfig
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if *apply || *resume || *verify {
		if (*apply && (!dataAPIHTTPS(*issuer) || !dataAPIHTTPS(*jwks) || strings.TrimSpace(*audience) == "")) || ((!*verify) && *output == "") || *collection == "" || *identities == "" {
			return printErr("Missing connection options", errors.New("--apply requires issuer, JWKS URL, audience, output, collection and identities; --resume requires collection and identities"))
		}
		for _, origin := range strings.Split(*origins, ",") {
			if origin != "" && !dataAPIOrigin(origin) {
				return printErr("Invalid origin", errors.New("origins must be HTTPS origins"))
			}
		}
		if _, err := os.Lstat(*output); *apply && !errors.Is(err, os.ErrNotExist) {
			return printErr("Invalid types destination", errors.New("choose a new output file"))
		}
		if !*verify {
			if info, err := os.Stat(filepath.Dir(*output)); err != nil || !info.IsDir() {
				return printErr("Invalid types destination", errors.New("the output directory must already exist"))
			}
		}
		base, err := os.Getwd()
		if err != nil {
			return printErr("Could not inspect project", err)
		}
		config = dataAPIHostedReplayConfig{Collection: *collection, Identities: *identities, ReadOnly: true}
		if err := prepareDataAPIHostedReplay(base, &config); err != nil {
			return printErr("Invalid hosted replay", err)
		}
		// Pin the collection bytes across preflight and execution. Identities are
		// already mapped in memory; JWT values remain in the environment.
		file, err := openCustomerFile(config.Collection)
		if err != nil {
			return printErr("Invalid hosted replay", err)
		}
		content, err := io.ReadAll(io.LimitReader(file, 65537))
		_ = file.Close()
		if err != nil || len(content) > 65536 {
			return printErr("Invalid hosted replay", errors.New("collection exceeds 64 KiB"))
		}
		if (*resume || *verify) && dataAPIConnectHash(content) != receipt.CollectionHash {
			return printErr("Cannot verify connection", errors.New("request collection changed since the original run"))
		}
		tmp, err := os.MkdirTemp("", "gregale-connect-")
		if err != nil {
			return printErr("Could not prepare replay", err)
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		config.Collection = filepath.Join(tmp, "requests.json")
		if err := os.WriteFile(config.Collection, content, 0600); err != nil {
			return printErr("Could not prepare replay", err)
		}
		if err := runDataAPIHostedReplay(ctx, &config, "", "", true, false, nil); err != nil {
			return printErr("Invalid hosted replay", errors.New("provide GET relation assertions for two distinct subjects"))
		}
		if *verify {
			return verifyDataAPIConnectedAuthorization(ctx, client, receipt, &config)
		}
		if *resume {
			return continueDataAPIConnect(ctx, client, path, &receipt, &config)
		}
		client, err = authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		db, err := resolveManagedPostgresDatabase(ctx, client, *database)
		if err != nil {
			return printErr("Could not resolve PostgreSQL database", err)
		}
		absoluteOutput, err := filepath.Abs(*output)
		if err != nil {
			return printErr("Invalid types destination", err)
		}
		if absoluteOutput == path {
			return printErr("Invalid types destination", errors.New("types and receipt must use different paths"))
		}
		receipt = dataAPIConnectReceipt{Version: 1, App: fs.Arg(0), APIHash: dataAPIConnectHash([]byte(client.BaseURL())), DatabaseID: db.ID, CollectionHash: dataAPIConnectHash(content), Output: absoluteOutput, NumericMode: *numericMode, Stage: "creating"}
	}
	inspectionDatabase := *database
	if *apply {
		inspectionDatabase = receipt.DatabaseID
	}
	if *inspectApp != "" {
		_, _ = fmt.Fprintln(osStdout, "Inspecting public-schema relations using the selected existing binding. SELECT grants describe that binding's role; the new API role and its effective policies can differ. Review view options and policies before exposing data.")
		inspect := []string{inspectionDatabase, "--app", *inspectApp}
		if *inspectEnv != "" {
			inspect = append(inspect, "--env", *inspectEnv)
		}
		inspect = append(inspect, "--", "-X", "-v", "ON_ERROR_STOP=1", "-c", dataAPIConnectInspectionSQL)
		if code := cmdPostgresConnect(inspect); code != 0 {
			return code
		}
	} else {
		if client == nil {
			client, err = authedClient()
			if err != nil {
				return printErr("Not logged in", err)
			}
		}
		if code := previewDataAPICatalog(ctx, client, inspectionDatabase); code != 0 {
			return code
		}
	}
	if !*apply {
		if !jsonOutput {
			_, _ = fmt.Fprintln(osStdout, "Preview complete. No API app or binding was created. Use --apply with identity settings, a new output path, and reviewed two-subject assertions to continue.")
		}
		return 0
	}
	if err := writeDataAPIConnectReceipt(path, receipt, true); err != nil {
		return printErr("Could not create connection receipt", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Connection receipt: %s\n", path)
	create := []string{fs.Arg(0), "--database", receipt.DatabaseID, "--read-only", "--issuer", *issuer, "--jwks-url", *jwks, "--audience", *audience}
	if *origins != "" {
		create = append(create, "--origins", *origins)
	}
	var deploymentReceiptErr error
	hooks := dataAPICreateReceiptHooks{app: func(app api.AppResponse, db api.ManagedPostgresDatabase) error {
		if app.ID == "" || app.Slug != receipt.App {
			return errors.New("created app identity does not match the connection")
		}
		receipt.AppID = app.ID
		if err := writeDataAPIConnectReceipt(path, receipt, false); err != nil {
			return err
		}
		if db.ID != receipt.DatabaseID {
			return errors.New("the selected database identity changed during creation")
		}
		return nil
	}, binding: func(binding api.ManagedPostgresBinding) error {
		if binding.ID == "" || binding.AppID != receipt.AppID || binding.DatabaseID != receipt.DatabaseID || binding.Scope != api.DefaultEnvScope || binding.EnvironmentKey != "DATABASE_URL" || binding.Access != "read_only" || binding.State != "ready" || binding.CredentialGeneration < 1 || binding.RotationPending {
			return errors.New("created binding identity does not match the connection")
		}
		receipt.BindingID, receipt.CredentialGeneration = binding.ID, binding.CredentialGeneration
		return writeDataAPIConnectReceipt(path, receipt, false)
	}, deployment: func(deployment api.DeploymentResponse) {
		if deployment.ID == "" || deployment.AppID != receipt.AppID {
			deploymentReceiptErr = errors.New("accepted deployment identity does not match the created app")
			return
		}
		receipt.DeploymentID = deployment.ID
		deploymentReceiptErr = writeDataAPIConnectReceipt(path, receipt, false)
	}}
	if code := cmdDataAPICreateContextWithReceipt(ctx, create, hooks); code != 0 {
		_, _ = fmt.Fprintln(os.Stderr, "Creation may have retained an app or binding. Inspect it before using data-api create --resume.")
		return code
	}
	if deploymentReceiptErr != nil {
		return printErr("API created; recording deployment failed", deploymentReceiptErr)
	}
	if err := dataAPIConnectSnapshot(ctx, client, &receipt, true); err != nil {
		return printErr("API created; recording identity failed", err)
	}
	receipt.Stage = "deployed"
	if err := writeDataAPIConnectReceipt(path, receipt, false); err != nil {
		return printErr("API created; recording identity failed", err)
	}
	return continueDataAPIConnect(ctx, client, path, &receipt, &config)
}

func writeDataAPIConnectTypes(path, content string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".gregale-connect-types-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, writeErr := io.WriteString(f, content)
	if writeErr == nil {
		writeErr = f.Chmod(0644)
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	// Linking within the same directory publishes complete content atomically
	// and fails if a customer file appeared since the preflight check.
	return os.Link(f.Name(), path)
}
