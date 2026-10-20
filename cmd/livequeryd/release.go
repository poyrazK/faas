package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Scale to zero (ADR-20261016151947): with -release-after, an idle hub releases the
// database so a parked or suspended database stays down, and -awake says
// how livequeryd learns that it is running again.
var (
	releaseAfter = flag.Duration("release-after", 0, "release the database after this long with no transaction to apply, so it can scale to zero (0 never releases); needs -awake")
	awakeSource  = flag.String("awake", "", "how a released hub learns the database runs again: neon (polls the Neon API for the endpoint's compute state)")
	awakeEvery   = flag.Duration("awake-every", 5*time.Second, "how often -awake is polled while released; commits made meanwhile reach subscribers within it")
	neonAPI      = flag.String("neon-api-url", "https://console.neon.tech/api/v2", "Neon API base URL, for -awake neon")
	neonKeyFile  = flag.String("neon-api-key-file", "", "file holding a Neon API key that may read the project's endpoints, for -awake neon")
	neonProject  = flag.String("neon-project", "", "Neon project ID of the database, for -awake neon")
	neonEndpoint = flag.String("neon-endpoint", "", "Neon endpoint (compute) ID of the database, for -awake neon")
)

// awakeConfig is how a database's hub learns that the database it released
// runs again: the -release-after, -awake and -neon-* flags, or the matching
// fields of a -databases-file entry.
type awakeConfig struct {
	ReleaseAfter time.Duration
	Source       string // "" or neon
	NeonKeyFile  string
	NeonProject  string
	NeonEndpoint string
}

// awakeProbe returns the activity probe the flags configure, or nil when
// the hub never releases.
func awakeProbe() (func(context.Context) (bool, error), error) {
	return newAwakeProbe(awakeConfig{ReleaseAfter: *releaseAfter, Source: *awakeSource, NeonKeyFile: *neonKeyFile, NeonProject: *neonProject, NeonEndpoint: *neonEndpoint})
}

// newAwakeProbe returns c's activity probe, or nil when the hub never
// releases.
func newAwakeProbe(c awakeConfig) (func(context.Context) (bool, error), error) {
	if c.ReleaseAfter <= 0 {
		if c.Source != "" {
			return nil, errors.New("livequeryd: awake needs release-after")
		}
		return nil, nil
	}
	switch c.Source {
	case "neon":
		if c.NeonKeyFile == "" || c.NeonProject == "" || c.NeonEndpoint == "" {
			return nil, errors.New("livequeryd: awake neon needs the Neon API key file, project and endpoint")
		}
		key, err := os.ReadFile(c.NeonKeyFile) //nolint:forbidigo // operator-provisioned credential named by a flag or the databases file
		if err != nil {
			return nil, fmt.Errorf("livequeryd: read the Neon API key file: %w", err)
		}
		return neonAwake(&http.Client{Timeout: 10 * time.Second}, *neonAPI, strings.TrimSpace(string(key)), c.NeonProject, c.NeonEndpoint), nil
	case "":
		// A released hub must notice the database waking, or commits made
		// meanwhile would wait for the next subscriber. A native database's
		// wake comes from schedd, which is not wired to livequeryd yet.
		return nil, errors.New("livequeryd: release-after needs awake")
	default:
		return nil, fmt.Errorf("livequeryd: unknown awake %q", c.Source)
	}
}

// neonAwake reports whether a Neon endpoint's compute is running. A compute
// that is starting counts as running: reconnecting then waits for it rather
// than waking it. idle and stopping do not.
func neonAwake(client *http.Client, base, key, project, endpoint string) func(context.Context) (bool, error) {
	target := strings.TrimRight(base, "/") + "/projects/" + url.PathEscape(project) + "/endpoints/" + url.PathEscape(endpoint)
	return func(ctx context.Context) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return false, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return false, fmt.Errorf("neon endpoint state: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("neon endpoint state: HTTP %d", resp.StatusCode)
		}
		var body struct {
			Endpoint struct {
				CurrentState string `json:"current_state"`
			} `json:"endpoint"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
			return false, fmt.Errorf("neon endpoint state: %w", err)
		}
		switch state := strings.ToLower(body.Endpoint.CurrentState); state {
		case "active", "init", "starting", "resuming":
			return true, nil
		case "idle", "stopping":
			return false, nil
		default:
			return false, fmt.Errorf("neon endpoint state %q", state)
		}
	}
}
