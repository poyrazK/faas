package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dataapi/integration"
)

func readHostedIntegrationInput(path string, limit int64) ([]byte, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("input must be a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("could not read bounded input")
	}
	return data, nil
}

func cmdDataAPIIntegrateHosted(args []string) int {
	fs := newFlagSet("data-api integrate", flag.ContinueOnError)
	hosted := fs.String("hosted", "", "verified hosted Data API name")
	receiptFile := fs.String("receipt", "", "connection receipt")
	directory := fs.String("directory", ".", "existing TypeScript project directory")
	clientDir := fs.String("client-directory", ".", "npm client directory relative to project")
	bundle := fs.String("bundle", "", "approved local Data API bundle")
	version := fs.String("sdk-version", "", "exact published @gregale/data version")
	numericMode := fs.String("numeric-mode", "", "must match the hosted connection's numeric mode")
	relation := fs.String("relation", "", "public relation to read")
	order := fs.String("order-by", "", "optional relation column for ordering")
	assertion := fs.String("assertion", "", "JSON file containing expected rows")
	tokenEnv := fs.String("token-env", "", "environment variable containing an application JWT")
	dryRun := fs.Bool("dry-run", false, "preview without network or changes")
	resume := fs.Bool("resume", false, "resume an unchanged hosted integration")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 0 || !api.ValidAppSlug(*hosted) || *directory == "" || *relation == "" || *assertion == "" || !dataAPITokenEnvName.MatchString(*tokenEnv) || (*bundle == "") == (*version == "") || (*version != "" && !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`).MatchString(*version)) {
		return printErr("Invalid hosted integration options", errors.New("provide --hosted NAME, --relation, --assertion, --token-env and exactly one of --bundle or --sdk-version"))
	}
	if *tokenEnv == "FAAS_TOKEN" || *tokenEnv == "PATH" || *tokenEnv == "HOME" || *tokenEnv == "NODE_OPTIONS" || *tokenEnv == "GREGALE_ARTIFACT_TOKEN" {
		return printErr("Invalid token environment", errors.New("use a dedicated application JWT environment variable"))
	}
	if *receiptFile == "" {
		*receiptFile = ".gregale-data-api-" + *hosted + ".connect.json"
	}
	receipt, err := readDataAPIConnectReceipt(*receiptFile)
	if err != nil {
		return printErr("Could not read connection receipt", err)
	}
	if receipt.App != *hosted || receipt.Stage != "verified" {
		return printErr("Invalid connection receipt", errors.New("use the verified receipt for this hosted API"))
	}
	if *numericMode != "" && *numericMode != receipt.NumericMode {
		return printErr("Invalid numeric mode", errors.New("hosted integration uses the connection receipt's numeric mode"))
	}
	expected, err := readHostedIntegrationInput(*assertion, 65536)
	var rows []json.RawMessage
	if err != nil || len(bytes.TrimSpace(expected)) == 0 || bytes.TrimSpace(expected)[0] != '[' || json.Unmarshal(expected, &rows) != nil || len(rows) > 100 {
		return printErr("Invalid read assertion", errors.New("provide a JSON array of at most 100 expected rows under 64 KiB"))
	}
	installKey := dataAPIConnectHash([]byte("npm:" + *version))
	manifest := ""
	var bundleBytes []byte
	if *bundle != "" {
		manifest, err = filepath.Abs(*bundle)
		if err != nil {
			return printErr("Invalid bundle", err)
		}
		bundleBytes, err = readHostedIntegrationInput(manifest, 65536)
		if err != nil {
			return printErr("Invalid bundle", err)
		}
		installKey = dataAPIConnectHash(append([]byte("bundle:"), bundleBytes...))
	}
	spec := integration.HostedSpec{AppID: receipt.AppID, DeploymentID: receipt.DeploymentID, APIHash: receipt.APIHash, Relation: *relation, OrderBy: *order, Fingerprint: receipt.Fingerprint, NumericMode: receipt.NumericMode, InstallKey: installKey, AssertionHash: dataAPIConnectHash(expected), Bundle: manifest != ""}
	plan, err := integration.BuildHosted(*directory, *clientDir, spec, *resume)
	if err != nil {
		return printErr("Could not plan hosted integration", err)
	}
	if jsonOutput {
		if err := writeNDJSON([]map[string]any{{"event": "hosted_integration_plan", "plan": plan, "dry_run": *dryRun, "relation": *relation, "remote_checked": false, "bundle_validated": false}}); err != nil {
			return printErr("Could not write integration plan", err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Hosted integration for %s (public.%s):\n", *hosted, *relation)
		for _, file := range plan.Files {
			_, _ = fmt.Fprintf(osStdout, "  %s %s\n", file.Action, file.Path)
		}
		_, _ = fmt.Fprintf(osStdout, "  verify generated types at %s; install the selected SDK and check the client\n", plan.GeneratedTypes)
	}
	if *dryRun {
		if !jsonOutput {
			_, _ = fmt.Fprintln(osStdout, "Preview is offline; the hosted schema, SDK archive and read assertion have not been verified.")
		}
		return 0
	}
	token := os.Getenv(*tokenEnv)
	if !validDataAPIAccessToken(token) || token == os.Getenv("FAAS_TOKEN") {
		return printErr("Invalid application JWT", errors.New("set the selected environment variable to an application JWT"))
	}
	for _, tool := range []string{"node", "npm"} {
		if _, err := exec.LookPath(tool); err != nil {
			return printErr("Missing integration prerequisite", fmt.Errorf("install %s before integration", tool))
		}
	}
	prereqCtx, prereqCancel := context.WithTimeout(context.Background(), 10*time.Second)
	prereq := exec.CommandContext(prereqCtx, "node", "-e", "if (Number(process.versions.node.split('.')[0]) < 22) process.exit(1)")
	prereq.Env = hostedCommandEnv(map[string]string{*tokenEnv: "", "FAAS_TOKEN": "", "GREGALE_DATA_API_ACCESS_TOKEN": "", "GREGALE_ARTIFACT_TOKEN": ""})
	prereqErr := prereq.Run()
	prereqCancel()
	if prereqErr != nil {
		return printErr("Missing integration prerequisite", errors.New("install Node.js 22 or newer"))
	}
	lock := flock.New(filepath.Join(os.TempDir(), "gregale-data-integrate-"+dataAPIConnectHash([]byte(plan.Directory))+".lock"))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return printErr("Integration already running", errors.New("could not lock the selected project"))
	}
	defer func() { _ = lock.Unlock() }()
	// Replan under the lock before any project writes.
	plan, err = integration.BuildHosted(plan.Directory, plan.ClientDirectory, spec, *resume)
	if err != nil {
		return printErr("Could not plan hosted integration", err)
	}
	if manifest != "" {
		if err := checkHostedBundleDestinations(plan, bundleBytes, *resume); err != nil {
			return printErr("Cannot install SDK", err)
		}
		var cleanup func()
		manifest, cleanup, err = freezeHostedBundle(manifest, bundleBytes)
		if err != nil {
			return printErr("Invalid SDK bundle", err)
		}
		defer cleanup()
		if err := validateHostedBundle(manifest, *tokenEnv); err != nil {
			return printErr("Invalid SDK bundle", err)
		}
	}
	if err := checkHostedSDKDependency(plan, *version, manifest != ""); err != nil {
		return printErr("Cannot install SDK", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if dataAPIConnectHash([]byte(client.BaseURL())) != receipt.APIHash {
		return printErr("Invalid connection receipt", errors.New("receipt belongs to another control plane"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if err := dataAPIConnectSnapshot(ctx, client, &receipt, false); err != nil {
		return printErr("Hosted connection changed", err)
	}
	app, err := client.GetApp(ctx, receipt.App)
	if err != nil || app.ID != receipt.AppID {
		return printErr("Hosted connection changed", errors.New("could not confirm recorded app"))
	}
	endpoint, err := dataAPIReadinessURL(app)
	if err != nil {
		return printErr("Invalid hosted API", err)
	}
	if err := verifyDataAPIServingContract(ctx, endpoint, token, receipt.Fingerprint); err != nil {
		return printErr("Hosted contract verification failed", err)
	}
	snapshotTask, err := generateDataAPIContract(ctx, client, receipt.App)
	if err != nil {
		return printErr("Could not inspect hosted schema", err)
	}
	if snapshotTask.DeploymentID != receipt.DeploymentID {
		return printErr("Hosted schema changed", errors.New("schema inspection used a different deployment"))
	}
	contract, err := parseDataAPIContract([]byte(snapshotTask.StdoutTail))
	if err != nil || contract.Fingerprint != receipt.Fingerprint {
		return printErr("Hosted schema changed", errors.New("snapshot does not match the verified fingerprint"))
	}
	var found, orderFound bool
	for _, table := range contract.Parsed.Tables {
		if table.Schema == "public" && table.Name == *relation {
			found = true
			for _, column := range table.Columns {
				if column.Name == *order {
					orderFound = true
				}
			}
		}
	}
	if !found || (*order != "" && !orderFound) {
		return printErr("Invalid hosted relation", errors.New("selected public relation or order column is absent from the verified contract"))
	}
	task, err := generateDataAPITypes(ctx, client, receipt.App)
	if err != nil {
		return printErr("Could not generate hosted types", err)
	}
	match := dataAPIFingerprint.FindStringSubmatch(task.StdoutTail)
	if task.DeploymentID != receipt.DeploymentID || len(match) != 2 || match[1] != receipt.Fingerprint {
		return printErr("Hosted schema changed", errors.New("generated types do not match the recorded deployment and fingerprint"))
	}
	types := dataAPITypesForMode(task, receipt.NumericMode)
	if dataAPIConnectHash([]byte(types)) != receipt.TypesHash {
		return printErr("Hosted types changed", errors.New("types differ from the connection receipt"))
	}
	if err := dataAPIConnectSnapshot(ctx, client, &receipt, false); err != nil {
		return printErr("Hosted connection changed", err)
	}
	if !*resume {
		if err := plan.Apply(); err != nil {
			return printErr("Could not apply hosted integration", err)
		}
	}
	if err := ensureHostedIntegrationTypes(plan, types); err != nil {
		return printErr("Hosted types changed", err)
	}
	scrub := map[string]string{*tokenEnv: "", "FAAS_TOKEN": "", "GREGALE_DATA_API_ACCESS_TOKEN": "", "GREGALE_ARTIFACT_TOKEN": ""}
	if manifest != "" {
		install := []string{"pin", manifest, "--sdk-only"}
		if plan.WorkspaceRoot != "" {
			install = append(install, "--workspace-root", plan.WorkspaceRoot)
		}
		original := osStdout
		if jsonOutput {
			osStdout = osStderr
		}
		code := installDataAPIBundleEnv(plan.Directory, plan.ClientDirectory, install, scrub)
		osStdout = original
		if code != 0 {
			return code
		}
	} else {
		root := filepath.Join(plan.Directory, plan.ClientDirectory)
		install := []string{"install", "--save-exact", "--ignore-scripts", "--no-audit", "--no-fund", "@gregale/data@" + *version}
		if plan.WorkspaceRoot != "" {
			root = filepath.Join(plan.Directory, plan.WorkspaceRoot)
			workspace, e := filepath.Rel(root, filepath.Join(plan.Directory, plan.ClientDirectory))
			if e != nil {
				return printErr("Invalid workspace", e)
			}
			install = append([]string{"install", "--workspace", filepath.ToSlash(workspace)}, install[1:]...)
		}
		if err := runHostedIntegrationCommand(ctx, root, append([]string{"npm"}, install...), scrub); err != nil {
			return printErr("SDK installation failed; retry with --resume", err)
		}
	}
	root := filepath.Join(plan.Directory, plan.ClientDirectory)
	if _, err := integration.BuildHosted(plan.Directory, plan.ClientDirectory, spec, true); err != nil {
		return printErr("Hosted scaffold changed", err)
	}
	if err := ensureHostedIntegrationTypes(plan, types); err != nil {
		return printErr("Hosted types changed", err)
	}
	if err := runHostedIntegrationCommand(ctx, root, []string{"npm", "exec", "--offline", "--", "tsc", "--project", integration.HostedClientSource + "/tsconfig.json"}, scrub); err != nil {
		return printErr("Hosted client typecheck failed; retry with --resume", err)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return printErr("Invalid hosted API", err)
	}
	u.Path = "/"
	if _, err := integration.BuildHosted(plan.Directory, plan.ClientDirectory, spec, true); err != nil {
		return printErr("Hosted scaffold changed", err)
	}
	if err := ensureHostedIntegrationTypes(plan, types); err != nil {
		return printErr("Hosted types changed", err)
	}
	if err := verifyHostedTypedClient(ctx, root, u.String(), receipt.Fingerprint, *relation, receipt.NumericMode, expected, token, scrub); err != nil {
		return printErr("Hosted typed read failed; retry with --resume", err)
	}
	if err := dataAPIConnectSnapshot(ctx, client, &receipt, false); err != nil {
		return printErr("Hosted connection changed", err)
	}
	if jsonOutput {
		if err := writeNDJSON([]map[string]any{{"event": "hosted_integration_result", "status": "passed", "app": receipt.App, "fingerprint": receipt.Fingerprint}}); err != nil {
			return printErr("Could not write integration result", err)
		}
	} else {
		_, _ = fmt.Fprintln(osStdout, "Hosted integration complete; the generated typed read matched the supplied assertion. Review the generated files and dependency changes.")
	}
	return 0
}

func ensureHostedIntegrationTypes(plan *integration.Plan, content string) error {
	root, err := os.OpenRoot(plan.Directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	dir, err := root.OpenRoot(filepath.Dir(plan.GeneratedTypes))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	const name = "database.types.ts"
	info, err := dir.Lstat(name)
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() != int64(len(content)) {
			return errors.New("existing types differ from the generated contract")
		}
		f, openErr := dir.Open(name)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = f.Close() }()
		opened, statErr := f.Stat()
		if statErr != nil || !os.SameFile(info, opened) {
			return errors.New("types changed during inspection")
		}
		data, readErr := io.ReadAll(io.LimitReader(f, int64(len(content))+1))
		if readErr != nil || string(data) != content {
			return errors.New("existing types differ from the generated contract")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	temporary := ".types-" + hex.EncodeToString(random[:]) + ".tmp"
	f, err := dir.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Remove(temporary) }()
	_, writeErr := io.WriteString(f, content)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return dir.Link(temporary, name)
}

func hostedCommandEnv(extra map[string]string) []string {
	env := os.Environ()
	for key, value := range extra {
		prefix := key + "="
		filtered := env[:0]
		for _, item := range env {
			if !strings.HasPrefix(item, prefix) {
				filtered = append(filtered, item)
			}
		}
		env = append(filtered, prefix+value)
	}
	return env
}

func runHostedIntegrationCommand(ctx context.Context, dir string, args []string, extra map[string]string) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = hostedCommandEnv(extra)
	cmd.Stdout = osStderr
	cmd.Stderr = osStderr
	if err := cmd.Run(); err != nil {
		return errors.New("command did not complete successfully; check the preceding diagnostics")
	}
	return nil
}

func checkHostedSDKDependency(plan *integration.Plan, version string, bundled bool) error {
	data, err := readHostedIntegrationInput(filepath.Join(plan.Directory, plan.ClientDirectory, "package.json"), 1<<20)
	if err != nil {
		return err
	}
	var pkg struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return errors.New("invalid client package.json")
	}
	if pkg.OptionalDependencies["@gregale/data"] != "" || pkg.PeerDependencies["@gregale/data"] != "" {
		return errors.New("the client already declares @gregale/data as a peer or optional dependency")
	}
	for _, deps := range []map[string]string{pkg.Dependencies, pkg.DevDependencies} {
		if current := deps["@gregale/data"]; current != "" {
			if (!bundled && current != version) || (bundled && current != "file:vendor/gregale-data.tgz" && current != "file:./vendor/gregale-data.tgz") {
				return errors.New("the client already declares a different @gregale/data dependency")
			}
		}
	}
	return nil
}

func checkHostedBundleDestinations(plan *integration.Plan, manifest []byte, resume bool) error {
	var bundle struct {
		SDK struct {
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"sdk"`
	}
	if json.Unmarshal(manifest, &bundle) != nil || !dataAPIApprovalFingerprint(bundle.SDK.SHA256) || bundle.SDK.Bytes <= 0 || bundle.SDK.Bytes > 8<<20 {
		return errors.New("invalid bundle SDK metadata")
	}
	if !resume {
		return nil
	}
	vendor := filepath.Join(plan.Directory, plan.ClientDirectory, "vendor/gregale-data.tgz")
	if data, err := readHostedIntegrationInput(vendor, 8<<20); err == nil {
		if int64(len(data)) != bundle.SDK.Bytes || dataAPIConnectHash(data) != bundle.SDK.SHA256 {
			return errors.New("existing vendored SDK differs from the approved bundle")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	pinPath := filepath.Join(plan.Directory, "data-api-artifacts.json")
	if data, err := readHostedIntegrationInput(pinPath, 65536); err == nil {
		var pin struct {
			Format  int             `json:"format"`
			BaseURL *string         `json:"base_url"`
			Bundle  json.RawMessage `json:"bundle"`
		}
		var old, new any
		if json.Unmarshal(data, &pin) != nil || pin.Format != 1 || pin.BaseURL != nil || json.Unmarshal(pin.Bundle, &old) != nil || json.Unmarshal(manifest, &new) != nil || !reflect.DeepEqual(old, new) {
			return errors.New("existing artifact pin differs from the approved bundle")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func validateHostedBundle(manifest, tokenEnv string) error {
	tmp, err := os.MkdirTemp("", "gregale-hosted-bundle-check-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	lib, err := templates.FS.ReadFile("data-api-starter/tools/artifact-lib.mjs")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "artifact-lib.mjs"), lib, 0600); err != nil {
		return err
	}
	script := `import {readJSON, validateBundle, verifyFile} from './artifact-lib.mjs'; import {dirname, join} from 'node:path'; const bundle=validateBundle(await readJSON(process.argv[2])); await verifyFile(join(dirname(process.argv[2]),bundle.sdk.file),bundle.sdk);`
	if err := os.WriteFile(filepath.Join(tmp, "check.mjs"), []byte(script), 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", filepath.Join(tmp, "check.mjs"), manifest)
	cmd.Dir = tmp
	cmd.Env = hostedCommandEnv(map[string]string{tokenEnv: "", "FAAS_TOKEN": "", "GREGALE_DATA_API_ACCESS_TOKEN": "", "GREGALE_ARTIFACT_TOKEN": ""})
	if err := cmd.Run(); err != nil {
		return errors.New("the bundle manifest or SDK archive failed validation")
	}
	return nil
}

func freezeHostedBundle(manifest string, content []byte) (string, func(), error) {
	var bundle struct {
		SDK struct {
			File      string `json:"file"`
			Bytes     int64  `json:"bytes"`
			SHA256    string `json:"sha256"`
			Integrity string `json:"integrity"`
		} `json:"sdk"`
	}
	if json.Unmarshal(content, &bundle) != nil || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.tgz$`).MatchString(bundle.SDK.File) || bundle.SDK.Bytes <= 0 || bundle.SDK.Bytes > 8<<20 || !dataAPIApprovalFingerprint(bundle.SDK.SHA256) {
		return "", nil, errors.New("invalid SDK bundle metadata")
	}
	archive, err := readHostedIntegrationInput(filepath.Join(filepath.Dir(manifest), bundle.SDK.File), 8<<20)
	if err != nil {
		return "", nil, errors.New("could not read the SDK archive")
	}
	sha := sha512.Sum512(archive)
	if int64(len(archive)) != bundle.SDK.Bytes || dataAPIConnectHash(archive) != bundle.SDK.SHA256 || "sha512-"+base64.StdEncoding.EncodeToString(sha[:]) != bundle.SDK.Integrity {
		return "", nil, errors.New("SDK archive does not match the approved bundle")
	}
	tmp, err := os.MkdirTemp("", "gregale-hosted-bundle-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	if err := os.WriteFile(filepath.Join(tmp, bundle.SDK.File), archive, 0600); err != nil {
		cleanup()
		return "", nil, err
	}
	path := filepath.Join(tmp, "data-api-bundle.json")
	if err := os.WriteFile(path, content, 0600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func verifyHostedTypedClient(ctx context.Context, root, origin, fingerprint, relation, numericMode string, expected []byte, token string, scrub map[string]string) error {
	tmp, err := os.MkdirTemp(root, ".gregale-hosted-verify-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	source := filepath.Join(root, integration.HostedClientSource)
	config := map[string]any{"compilerOptions": map[string]any{"target": "ES2022", "module": "ESNext", "moduleResolution": "Bundler", "lib": []string{"ES2022", "DOM"}, "strict": true, "types": []string{}, "outDir": tmp, "rootDir": source}, "files": []string{filepath.Join(source, "client.ts"), filepath.Join(source, "database.types.ts")}}
	encoded, _ := json.Marshal(config)
	for name, content := range map[string][]byte{"tsconfig.json": encoded, "package.json": []byte(`{"type":"module"}`), "expected.json": expected} {
		if err := os.WriteFile(filepath.Join(tmp, name), content, 0600); err != nil {
			return err
		}
	}
	if err := runHostedIntegrationCommand(ctx, root, []string{"npm", "exec", "--offline", "--", "tsc", "--project", filepath.Join(tmp, "tsconfig.json")}, scrub); err != nil {
		return err
	}
	script, err := templates.FS.ReadFile("data-api/hosted-integration-verify.mjs")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "verify.mjs"), script, 0600); err != nil {
		return err
	}
	opts, _ := json.Marshal(map[string]string{"origin": origin, "fingerprint": fingerprint, "relation": relation, "numericMode": numericMode, "assertion": filepath.Join(tmp, "expected.json")})
	if err := os.WriteFile(filepath.Join(tmp, "options.json"), opts, 0600); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "node", filepath.Join(tmp, "verify.mjs"), filepath.Join(tmp, "options.json"))
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=" + os.Getenv("LANG"), "GREGALE_INTEGRATION_TOKEN=" + token}
	if cert := os.Getenv("NODE_EXTRA_CA_CERTS"); cert != "" {
		cmd.Env = append(cmd.Env, "NODE_EXTRA_CA_CERTS="+cert)
	}
	cmd.Stdout = osStderr
	cmd.Stderr = osStderr
	if err := cmd.Run(); err != nil {
		return errors.New("typed client did not pass its read assertion")
	}
	return nil
}
