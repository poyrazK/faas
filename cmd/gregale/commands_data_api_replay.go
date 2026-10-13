// adr: 981 — reuse owner-defined authorization scenarios before releasing a refresh.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIHostedReplayConfig struct {
	Collection      string   `json:"collection"`
	Identities      string   `json:"identities"`
	Scenarios       []string `json:"scenarios,omitempty"`
	RefreshTokenEnv string   `json:"refresh_token_env,omitempty"`
	mappings        map[string]string
}
type dataAPIHostedReplayOptions struct {
	Collection      string            `json:"collection"`
	Identities      map[string]string `json:"identities"`
	Scenarios       []string          `json:"scenarios"`
	RefreshTokenEnv string            `json:"refresh_token_env,omitempty"`
	URL             string            `json:"url"`
	Fingerprint     string            `json:"fingerprint,omitempty"`
	RequirePaused   bool              `json:"require_paused,omitempty"`
	ValidateOnly    bool              `json:"validate_only,omitempty"`
	JSON            bool              `json:"json"`
}

var dataAPIReplayIdentity = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func prepareDataAPIHostedReplay(base string, config *dataAPIHostedReplayConfig, coordinatorTokenEnv ...string) error {
	if config.Collection == "" || config.Identities == "" {
		return errors.New("authorization replay requires collection and identities files")
	}
	config.Collection, config.Identities = dataAPISyncPath(base, config.Collection), dataAPISyncPath(base, config.Identities)
	for _, path := range []string{config.Collection, config.Identities} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
			return errors.New("replay inputs must be regular files under 64 KiB")
		}
	}
	file, err := openCustomerFile(config.Identities)
	if err != nil {
		return errors.New("could not open replay identities file")
	}
	defer func() { _ = file.Close() }()
	bytes, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(bytes) > 65536 || json.Unmarshal(bytes, &config.mappings) != nil {
		return errors.New("identities file must map identity names to JWT environment names")
	}
	if len(config.mappings) < 2 || len(config.mappings) > 100 {
		return errors.New("configure two to 100 replay identities")
	}
	for name, variable := range config.mappings {
		if !dataAPIReplayIdentity.MatchString(name) || name == "constructor" || name == "prototype" || !dataAPITokenEnvName.MatchString(variable) {
			return errors.New("invalid replay identity or JWT environment name")
		}
		if !validDataAPIAccessToken(os.Getenv(variable)) {
			return errors.New("configure every replay identity JWT through its mapped environment variable")
		}
	}
	seen := map[string]bool{}
	for _, scenario := range config.Scenarios {
		if !dataAPIReplayIdentity.MatchString(scenario) || seen[scenario] {
			return errors.New("replay scenarios must be unique identifiers")
		}
		seen[scenario] = true
	}
	if config.RefreshTokenEnv == "" {
		config.RefreshTokenEnv = "GREGALE_DATA_API_REFRESH_TOKEN"
	}
	refreshEnv := config.RefreshTokenEnv
	if len(coordinatorTokenEnv) > 0 && coordinatorTokenEnv[0] != "" {
		refreshEnv = coordinatorTokenEnv[0]
	}
	if !dataAPITokenEnvName.MatchString(refreshEnv) {
		return errors.New("invalid replay refresh_token_env")
	}
	if token := os.Getenv(refreshEnv); token != "" && !dataAPIApprovalFingerprint(token) {
		return errors.New("invalid private replay refresh token")
	}
	if _, err := exec.LookPath("node"); err != nil {
		return errors.New("install Node 22+ before hosted replay")
	}
	return nil
}

func runDataAPIHostedReplay(ctx context.Context, config *dataAPIHostedReplayConfig, healthURL, fingerprint string, validateOnly, coordinated bool, extra map[string]string) error {
	origin := ""
	if healthURL != "" {
		u, err := url.Parse(healthURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/healthz" {
			return errors.New("invalid hosted replay app URL")
		}
		u.Path = "/"
		origin = u.String()
	}
	runtime, err := os.MkdirTemp("", "gregale-hosted-replay-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(runtime) }()
	for _, name := range []string{"hosted-replay.mjs", "request-replay.mjs", "dev-requests.mjs"} {
		source, err := templates.FS.ReadFile("data-api/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(runtime, name), source, 0600); err != nil {
			return err
		}
	}
	scenarios := config.Scenarios
	if scenarios == nil {
		scenarios = []string{}
	}
	refreshEnv := config.RefreshTokenEnv
	if coordinated {
		refreshEnv = "GREGALE_DATA_API_REFRESH_TOKEN"
	}
	options := dataAPIHostedReplayOptions{Collection: config.Collection, Identities: config.mappings, Scenarios: scenarios, RefreshTokenEnv: refreshEnv, URL: origin, Fingerprint: fingerprint, RequirePaused: coordinated, ValidateOnly: validateOnly, JSON: jsonOutput || coordinated}
	content, err := json.Marshal(options)
	if err != nil {
		return err
	}
	path := filepath.Join(runtime, "options.json")
	if err := os.WriteFile(path, content, 0600); err != nil {
		return err
	}
	step := dataAPISyncCommand{Command: []string{"node", filepath.Join(runtime, "hosted-replay.mjs"), path}, Directory: runtime}
	if coordinated || validateOnly {
		return runDataAPISyncCommandEnv(ctx, step, extra)
	}
	// The standalone command owns stdout; sync keeps nested reports on stderr.
	return runDataAPISyncCommandEnvOutput(ctx, step, extra, osStdout)
}

func cmdDataAPIReplay(args []string) int {
	fs := newFlagSet("data-api replay", flag.ContinueOnError)
	collection := fs.String("collection", "", "saved request collection")
	identities := fs.String("identities", "", "JSON map of identity names to JWT environment variables")
	scenario := fs.String("scenario", "", "select one named scenario")
	refreshEnv := fs.String("refresh-token-env", "GREGALE_DATA_API_REFRESH_TOKEN", "optional private refresh token environment variable")
	timeout := fs.Duration("timeout", 5*time.Minute, "replay deadline (maximum 1h)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || *collection == "" || *identities == "" || *timeout <= 0 || *timeout > time.Hour {
		PrintUsage(os.Stderr, "usage: gregale data-api replay NAME --collection FILE --identities FILE [--scenario NAME] [--refresh-token-env ENV] [--timeout DURATION]", "data-api")
		return 1
	}
	base, err := os.Getwd()
	if err != nil {
		return printErr("Could not inspect project", err)
	}
	config := dataAPIHostedReplayConfig{Collection: *collection, Identities: *identities, RefreshTokenEnv: *refreshEnv}
	if *scenario != "" {
		config.Scenarios = []string{*scenario}
	}
	if err := prepareDataAPIHostedReplay(base, &config); err != nil {
		return printErr("Invalid hosted replay", err)
	}
	interrupted, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupted, *timeout)
	defer cancel()
	if err := runDataAPIHostedReplay(ctx, &config, "", "", true, false, nil); err != nil {
		return printErr("Invalid hosted replay", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	app, err := client.GetApp(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not inspect Data API app", err)
	}
	endpoint, err := dataAPIReadinessURL(app)
	if err != nil {
		return printErr("Could not inspect Data API app", err)
	}
	if err := runDataAPIHostedReplay(ctx, &config, endpoint, "", false, false, nil); err != nil {
		if ctx.Err() != nil {
			return printErr("Hosted replay interrupted", ctx.Err())
		}
		return printErr("Hosted authorization replay failed", errors.New("replay did not complete successfully; inspect assertion reports, credentials and app reachability"))
	}
	return 0
}
