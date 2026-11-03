package main

// api_transport_security.go keeps the bearer token off plain HTTP. Every
// request the CLI makes carries the API key, and an interactive login
// receives a fresh one in the response, so an http:// API base on another
// host hands the credential to anyone on the network path. Loopback stays
// open for local development; a self-hosted Gregale on a trusted private
// network opts in with GREGALE_ALLOW_INSECURE_API=1 and gets a warning.

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// allowInsecureAPIEnv is the one escape hatch for plain-HTTP API bases on
// non-loopback hosts. It accepts the same true values as config booleans.
const allowInsecureAPIEnv = "GREGALE_ALLOW_INSECURE_API"

// insecureAPIWarned limits the opt-in warning to one line per run; run
// resets it so each invocation (and each test calling run) warns afresh.
var insecureAPIWarned bool

// insecureAPIBase reports whether base is plain HTTP to a host other than
// this machine.
func insecureAPIBase(base string) bool {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || !strings.EqualFold(u.Scheme, "http") {
		return false
	}
	return !loopbackHost(u.Hostname())
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func insecureAPIAllowed() bool {
	allowed, err := parseConfigBool(os.Getenv(allowInsecureAPIEnv))
	return err == nil && allowed
}

// checkAPIBaseTransport returns nil for an HTTPS or loopback API base. A
// plain-HTTP base on another host is an error unless allowInsecureAPIEnv
// opts in, in which case the run prints one warning on stderr.
func checkAPIBaseTransport(base string) error {
	if !insecureAPIBase(base) {
		return nil
	}
	if !insecureAPIAllowed() {
		return insecureAPIBaseError(base)
	}
	if !insecureAPIWarned {
		insecureAPIWarned = true
		PrintWarn(osStderr, "API %s uses plain HTTP: %s=1 lets the CLI send your API key unencrypted.", displayAPIBase(base), allowInsecureAPIEnv)
	}
	return nil
}

func insecureAPIBaseError(base string) error {
	return fmt.Errorf("API %s uses plain HTTP, which would send your API key unencrypted; use https://, or set %s=1 for a self-hosted Gregale on a trusted private network", displayAPIBase(base), allowInsecureAPIEnv)
}

// validateAcceptedAPIBase is validateConfigAPIBase plus the transport rule,
// for the commands that save an API base. Loading a saved config keeps only
// the structural check, so a base saved by an older release can still be
// replaced with `config set api-base`; using it is refused at run time.
func validateAcceptedAPIBase(raw string) (string, error) {
	normalized, err := validateConfigAPIBase(raw)
	if err != nil {
		return "", err
	}
	if err := checkAPIBaseTransport(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

// displayAPIBase drops URL credentials, query and fragment so an API base
// can be printed. FAAS_API is not validated like saved config and may carry
// user:password@ (or a token as the user name).
func displayAPIBase(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		if strings.Contains(raw, "@") {
			return "(invalid API address)"
		}
		return raw
	}
	u.User, u.RawQuery, u.Fragment, u.ForceQuery = nil, "", "", false
	return u.String()
}

// refusedTransport fails every request without dialing. guardAPIClient
// installs it on a client whose base would carry the token in clear text,
// covering requests made before or outside run's preflight (capability
// discovery, completion helpers, logout's saved session base).
type refusedTransport struct{ err error }

func (t refusedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	return nil, t.err
}

func apiClientRefused(base string) bool {
	return insecureAPIBase(base) && !insecureAPIAllowed()
}

func guardAPIClient(c *Client) *Client {
	if apiClientRefused(c.BaseURL()) {
		c.HTTPClient().Transport = refusedTransport{err: insecureAPIBaseError(c.BaseURL())}
	}
	return c
}

// connectionPreflightExempt lists commands that run before any API request
// and must keep working to repair the connection: reading and changing the
// configuration, completion helpers and the manual. Their API calls (context
// --verify, config profile check) check the base themselves.
var connectionPreflightExempt = map[string]bool{
	"config": true, "profile": true, "context": true, "completion": true, "man": true,
}

// connectionPreflight runs once per invocation, before dispatch. It refuses
// an insecure API base and says when FAAS_TOKEN shadows a selected profile
// or a saved login.
func connectionPreflight(command string) (int, bool) {
	if connectionPreflightExempt[command] {
		return 0, false
	}
	if err := checkAPIBaseTransport(apiBase()); err != nil {
		return printErr("Insecure API connection", err), true
	}
	switch command {
	case "login", "logout", "start", dispatchSignup:
		// These commands handle FAAS_TOKEN themselves.
	default:
		warnEnvTokenOverride()
	}
	return 0, false
}
