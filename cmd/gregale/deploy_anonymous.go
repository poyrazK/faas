package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Claimable deploys (ADR-20261124080000). `gregale deploy` with no saved
// login and no GREGALE_TOKEN deploys without an account when a known coding
// agent runs it (api.CodingAgents, not CI) or when --anonymous is given: the
// CLI asks for an anonymous session, keeps its deploy key in a file of its
// own (never the login store), deploys, and prints the claim link a person
// opens to keep the app. Later commands use that key only while no login
// exists and only until the claim window ends.

// anonymousDeploySession is the session this invocation created, so the
// deploy can print the claim link when it ends. Reset per deploy.
var anonymousDeploySession *api.AnonymousSessionResponse

// anonymousDeployDisabledEnv turns the automatic fallback off.
const anonymousDeployDisabledEnv = "GREGALE_NO_ANONYMOUS_DEPLOY"

// envSet reports a variable set to anything but empty, 0 or false.
func envSet(getenv func(string) string, name string) bool {
	v := strings.TrimSpace(getenv(name))
	return v != "" && v != "0" && !strings.EqualFold(v, "false")
}

// anonymousDeployByDefault reports whether a deploy with no credential
// should deploy without an account rather than fail. Only a known coding
// agent's shell qualifies: a cron job, CI runner or build server that lost
// its token must fail rather than deploy somewhere nobody owns.
func anonymousDeployByDefault(getenv func(string) string) bool {
	if envSet(getenv, anonymousDeployDisabledEnv) || envSet(getenv, "CI") {
		return false
	}
	return api.CodingAgentFromEnv(getenv) != ""
}

// deployClientOrAnonymous returns the deploy client: the saved login, or an
// anonymous session when allowed. The error is the usual "not logged in"
// one when no session is allowed or the server offers none.
func deployClientOrAnonymous(ctx context.Context, timeout time.Duration, anonymous, project bool) (*Client, error) {
	if loadToken() != "" || (!anonymous && !anonymousDeployByDefault(os.Getenv)) {
		return authedClientWithDeployTimeout(timeout)
	}
	if project {
		// A project deploy creates databases and several apps, which an
		// unclaimed account may not; refuse before starting a session.
		return nil, errAuth(errors.New("project deploys need a Gregale account — " + authenticationHint()))
	}
	if key := loadAnonymousSessionToken(); key != "" {
		// Redeploy with the open anonymous deploy rather than start another.
		if err := checkAnonymousKey(ctx, key); err != nil {
			return nil, err
		}
		return NewClientWithDeployTimeout(apiBase(), key, timeout), nil
	}
	sess, err := createAnonymousDeploySession(ctx)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusNotFound {
			return nil, errAuth(errors.New("not logged in — " + authenticationHint()))
		}
		return nil, errAuth(fmt.Errorf("not logged in, and deploying without an account failed: %w — %s", err, authenticationHint()))
	}
	if err := saveAnonymousSession(sess); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save the anonymous deploy key (%v); later commands will need it again\n", err)
	}
	anonymousDeploySession = &sess
	fmt.Fprintf(os.Stderr, "Deploying without a Gregale account. A person must claim this app by %s or it is deleted:\n  %s\n",
		sess.ExpiresAt.Local().Format("Mon 2 Jan 15:04 MST"), sess.ClaimURL)
	return NewClientWithDeployTimeout(apiBase(), sess.APIKey, timeout), nil
}

// savedAnonymousSession is the anonymous-deploy file: the key, the API it
// belongs to and when the claim window ends.
type savedAnonymousSession struct {
	APIKey    string    `json:"api_key"`
	APIBase   string    `json:"api_base"`
	ClaimURL  string    `json:"claim_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// anonymousSessionPath sits next to the profile's token file.
func anonymousSessionPath() (string, error) {
	p, err := tokenPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "anonymous-deploy.json"), nil
}

func saveAnonymousSession(sess api.AnonymousSessionResponse) error {
	p, err := anonymousSessionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(savedAnonymousSession{APIKey: sess.APIKey, APIBase: apiBase(), ClaimURL: sess.ClaimURL, ExpiresAt: sess.ExpiresAt})
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// loadAnonymousSessionToken returns the saved anonymous deploy key while its
// claim window is open and it belongs to the current API, else "".
func loadAnonymousSessionToken() string {
	p, err := anonymousSessionPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var saved savedAnonymousSession
	if json.Unmarshal(b, &saved) != nil || !time.Now().Before(saved.ExpiresAt) || saved.APIBase != apiBase() || !validPersistedToken(saved.APIKey) {
		return ""
	}
	return saved.APIKey
}

// errAnonymousEnded is the answer once the saved anonymous key stops
// working: the deploy was claimed (the claim revokes the key) or it ended.
var errAnonymousEnded = errors.New("this deploy without an account was claimed or has ended; run gregale login")

// checkAnonymousKey asks the API whether the saved anonymous key still
// works. A refused key is forgotten; any other failure is left to the
// command that uses the key.
func checkAnonymousKey(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := NewClient(apiBase(), key).Whoami(ctx)
	if anonymousKeyRefused(err) {
		deleteAnonymousSession()
		return errAuth(errAnonymousEnded)
	}
	return nil
}

// anonymousKeyRefused reports a 401 for the anonymous key.
func anonymousKeyRefused(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusUnauthorized
}

// deleteAnonymousSession forgets the anonymous deploy key (logout).
func deleteAnonymousSession() {
	if p, err := anonymousSessionPath(); err == nil {
		_ = os.Remove(p)
	}
}

func createAnonymousDeploySession(ctx context.Context) (api.AnonymousSessionResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return NewClient(apiBase(), "").CreateAnonymousSession(ctx, api.AnonymousSessionRequest{
		Client:      "gregale-cli",
		AcceptTerms: true,
	})
}

// printAnonymousClaimNotice repeats the claim link after the deploy, where
// the agent reading the output will look for it.
func printAnonymousClaimNotice(w io.Writer) {
	sess := anonymousDeploySession
	if sess == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "\nThis app has no owner yet. Give this link to the person you are working for;\n"+
		"they claim the app by opening it and confirming their email (by %s):\n  %s\n"+
		"Until then: Free plan limits, no custom domains, databases or API keys. Run `gregale claim` for a fresh link.\n",
		sess.ExpiresAt.Local().Format("Mon 2 Jan 15:04 MST"), sess.ClaimURL)
}

// cmdClaim implements `gregale claim`: print a fresh claim link for the
// anonymous deploy the saved key belongs to.
func cmdClaim(args []string) int {
	fs := newFlagSet("claim", flag.ContinueOnError)
	fs.Usage = func() {
		if jsonOutput && !jsonUsageHelp {
			return
		}
		PrintUsage(os.Stderr, "usage: gregale claim", "auth")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	// The login when there is one, else the saved anonymous deploy key.
	var client *Client
	anonymousKey := ""
	if loadToken() == "" {
		anonymousKey = loadAnonymousSessionToken()
	}
	if anonymousKey != "" {
		client = NewClient(apiBase(), anonymousKey)
	} else {
		c, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		client = c
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	link, err := client.CreateClaimLink(ctx)
	if err != nil {
		if anonymousKey != "" && anonymousKeyRefused(err) {
			deleteAnonymousSession()
			return printErr("Nothing to claim", errAnonymousEnded)
		}
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusConflict {
			return printErr("Nothing to claim", errors.New("this login is a regular Gregale account; only an app deployed without an account has a claim link"))
		}
		return printErr("Could not create a claim link", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(link))
	}
	_, _ = fmt.Fprintf(osStdout, "Claim link (valid until %s; earlier links stop working):\n  %s\n",
		link.ExpiresAt.Local().Format("Mon 2 Jan 15:04 MST"), link.ClaimURL)
	return 0
}
