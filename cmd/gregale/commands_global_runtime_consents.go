package main

// adr: 646
import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
)

func cmdGlobalRuntimeConsent(verb string, args []string) int {
	if len(args) == 0 || !globaldirectory.ValidSlug(args[0]) {
		return printErr("Invalid global app", fmt.Errorf("a canonical global app slug is required"))
	}
	fs := newFlagSet("global-app "+verb, flag.ContinueOnError)
	id := fs.String("consent", "", "stable consent UUID, reused across retries (required)")
	var env, region, expiry string
	if verb != "show-runtime-consent" {
		fs.StringVar(&env, "regional-token-env", "", "environment variable containing the original regional admin key (required)")
	}
	if verb == "setup-runtime-consent" {
		fs.StringVar(&region, "region", "", "selected canonical region (required)")
		fs.StringVar(&expiry, "expires-at", "", "explicit RFC3339 expiry, at most microsecond precision (required)")
	}
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	if fs.NArg() != 0 || !globaldirectory.ValidID(*id) {
		return printErr("Invalid consent", fmt.Errorf("--consent must be a canonical nonzero UUID"))
	}
	var key string
	var stamp time.Time
	if verb != "show-runtime-consent" {
		if !validDirectoryTokenEnv(env) {
			return printErr("Regional key unavailable", fmt.Errorf("--regional-token-env is required"))
		}
		key = os.Getenv(env)
		if !api.ValidAPIKeyFormat(key) {
			return printErr("Regional key unavailable", fmt.Errorf("the named variable must contain a regional admin API key"))
		}
	}
	if verb == "setup-runtime-consent" {
		var err error
		stamp, err = time.Parse(time.RFC3339Nano, expiry)
		if err != nil || !stamp.Equal(stamp.Truncate(time.Microsecond)) || !stamp.After(time.Now()) || stamp.After(time.Now().Add(api.RegionalRuntimeConsentMaxLifetime)) || region == "" || !api.ValidComputeRegion(region) {
			return printErr("Invalid consent scope", fmt.Errorf("provide --region and an explicit RFC3339 --expires-at within the consent lifetime; reuse the same value on retries"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.GlobalRuntimeConsentResponse
	ctx := context.Background()
	switch verb {
	case "setup-runtime-consent":
		out, err = client.PutGlobalRuntimeConsent(ctx, args[0], *id, api.PutGlobalRuntimeConsentRequest{Region: region, ExpiresAt: stamp, RegionalAPIKey: key})
	case "show-runtime-consent":
		out, err = client.GetGlobalRuntimeConsent(ctx, args[0], *id)
	case "revoke-runtime-consent":
		out, err = client.RevokeGlobalRuntimeConsent(ctx, args[0], *id, api.RevokeGlobalRuntimeConsentRequest{RegionalAPIKey: key})
	default:
		return 1
	}
	if err != nil {
		return printErr("Could not access runtime delivery consent", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\texpires=%s\n", out.ID, out.Binding.Region, out.Status, out.ExpiresAt.Format(time.RFC3339Nano))
	return 0
}
