// Package authsettings maps typed application Auth settings (ADR-1190) onto
// the Auth app's secrets. The CLI and the dashboard share it, so both accept
// exactly the same values. The Auth template (cmd/gregale/templates/auth/
// config.mjs) reads these secrets; TestAuthTemplateSettingsContract in
// cmd/gregale keeps the provider names and character classes in step.
package authsettings

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

// Providers are the OAuth providers GoTrue v2.197.0 supports that the Auth
// app configures.
var Providers = []string{"apple", "azure", "bitbucket", "discord", "facebook", "figma", "github", "gitlab", "google", "kakao", "keycloak", "linkedin_oidc", "notion", "slack_oidc", "spotify", "twitch", "workos", "x", "zoom"}

// providerURL lists providers with a configurable server URL; Keycloak has
// no default and requires one.
var providerURL = map[string]bool{"azure": false, "gitlab": false, "keycloak": true, "workos": false}

// ClassOrder and CharacterClasses define the password character classes.
// GOTRUE_PASSWORD_REQUIRED_CHARACTERS separates classes with ':', and no
// class contains ':' or a backslash, so none needs escaping.
var ClassOrder = []string{"lower", "upper", "digit", "symbol"}

var CharacterClasses = map[string]string{
	"lower":  "abcdefghijklmnopqrstuvwxyz",
	"upper":  "ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	"digit":  "0123456789",
	"symbol": "!@#$%^&*()_+-=[]{};'\"|<>?,./`~",
}

const (
	secretDisableSignup   = "AUTH_DISABLE_SIGNUP"
	secretAutoconfirm     = "AUTH_AUTOCONFIRM"
	secretSiteURL         = "AUTH_SITE_URL"
	secretRedirectURLs    = "AUTH_REDIRECT_URLS"
	secretMinLength       = "GOTRUE_PASSWORD_MIN_LENGTH"
	secretRequired        = "GOTRUE_PASSWORD_REQUIRED_CHARACTERS"
	secretLeakedPasswords = "GOTRUE_PASSWORD_HIBP_ENABLED"
	secretTOTPEnroll      = "GOTRUE_MFA_TOTP_ENROLL_ENABLED"
	secretTOTPVerify      = "GOTRUE_MFA_TOTP_VERIFY_ENABLED"
)

// Change is a settings change; nil fields stay as they are.
type Change struct {
	SiteURL      *string
	RedirectURLs *[]string
	Signup       *bool
	Autoconfirm  *bool
	// PasswordMinLength is between AuthPasswordMinLengthFloor and
	// AuthPasswordMinLengthCeiling.
	PasswordMinLength *int
	// PasswordRequire lists character classes from ClassOrder; empty
	// removes the requirement.
	PasswordRequire     *[]string
	LeakedPasswordCheck *bool
	// MFA turns TOTP enrolment on or off. Verification stays on, so users
	// who already enrolled an authenticator keep using it.
	MFA *bool
}

// Empty reports whether the change sets nothing.
func (c Change) Empty() bool {
	return c == Change{}
}

// Secrets validates the change and returns the secrets to set and those to
// delete.
func (c Change) Secrets() (map[string]string, []string, error) {
	set, unset := map[string]string{}, []string{}
	if c.SiteURL != nil {
		if !RedirectURL(*c.SiteURL) {
			return nil, nil, fmt.Errorf("site URL %q must be HTTPS, or HTTP on a loopback host, without credentials or a fragment", *c.SiteURL)
		}
		set[secretSiteURL] = *c.SiteURL
	}
	if c.RedirectURLs != nil {
		if len(*c.RedirectURLs) > api.AuthMaxRedirectURLs {
			return nil, nil, fmt.Errorf("at most %d redirect URLs are allowed; got %d", api.AuthMaxRedirectURLs, len(*c.RedirectURLs))
		}
		for _, value := range *c.RedirectURLs {
			if !RedirectURL(value) || strings.Contains(value, ",") {
				return nil, nil, fmt.Errorf("redirect URL %q must be HTTPS, or HTTP on a loopback host, without credentials, commas or a fragment", value)
			}
		}
		set[secretRedirectURLs] = strings.Join(*c.RedirectURLs, ",")
	}
	if c.Signup != nil {
		set[secretDisableSignup] = flag(!*c.Signup)
	}
	if c.Autoconfirm != nil {
		set[secretAutoconfirm] = flag(*c.Autoconfirm)
	}
	if c.PasswordMinLength != nil {
		n := *c.PasswordMinLength
		if n < api.AuthPasswordMinLengthFloor || n > api.AuthPasswordMinLengthCeiling {
			return nil, nil, fmt.Errorf("minimum password length must be %d to %d; got %d", api.AuthPasswordMinLengthFloor, api.AuthPasswordMinLengthCeiling, n)
		}
		set[secretMinLength] = strconv.Itoa(n)
	}
	if c.PasswordRequire != nil {
		required, err := requiredCharacters(*c.PasswordRequire)
		if err != nil {
			return nil, nil, err
		}
		if required == "" {
			unset = append(unset, secretRequired)
		} else {
			set[secretRequired] = required
		}
	}
	if c.LeakedPasswordCheck != nil {
		set[secretLeakedPasswords] = strconv.FormatBool(*c.LeakedPasswordCheck)
	}
	if c.MFA != nil {
		set[secretTOTPEnroll], set[secretTOTPVerify] = strconv.FormatBool(*c.MFA), "true"
	}
	return set, unset, nil
}

func requiredCharacters(classes []string) (string, error) {
	seen := map[string]bool{}
	for _, class := range classes {
		if _, ok := CharacterClasses[class]; !ok {
			return "", fmt.Errorf("unknown character class %q; use %s", class, strings.Join(ClassOrder, ", "))
		}
		seen[class] = true
	}
	parts := []string{}
	for _, class := range ClassOrder {
		if seen[class] {
			parts = append(parts, CharacterClasses[class])
		}
	}
	return strings.Join(parts, ":"), nil
}

func flag(enabled bool) string {
	if enabled {
		return "1"
	}
	return "0"
}

// ProviderPrefix is the secret-name prefix of one provider's settings.
func ProviderPrefix(provider string) string {
	return "GOTRUE_EXTERNAL_" + strings.ToUpper(provider) + "_"
}

// Provider validates an OAuth provider's credentials and returns its
// secrets. The Auth app sets the callback URL itself.
func Provider(provider, clientID, clientSecret, serverURL string) (map[string]string, error) {
	if !slices.Contains(Providers, provider) {
		return nil, fmt.Errorf("unknown provider %q; use one of %s", provider, strings.Join(Providers, ", "))
	}
	if !credential(clientID, api.AuthProviderClientIDMaxBytes) {
		return nil, errors.New("the client ID is required and must not contain spaces or control characters")
	}
	if !credential(clientSecret, api.AuthProviderSecretMaxBytes) {
		return nil, errors.New("the client secret is required and must not contain spaces or control characters")
	}
	prefix := ProviderPrefix(provider)
	set := map[string]string{prefix + "ENABLED": "true", prefix + "CLIENT_ID": clientID, prefix + "SECRET": clientSecret}
	required, configurable := providerURL[provider]
	switch {
	case serverURL == "" && required:
		return nil, fmt.Errorf("%s requires its server URL", provider)
	case serverURL != "" && !configurable:
		return nil, fmt.Errorf("%s does not take a server URL", provider)
	case serverURL != "":
		u, err := url.Parse(serverURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("server URL %q must be HTTPS without credentials, a query or a fragment", serverURL)
		}
		set[prefix+"URL"] = serverURL
	}
	return set, nil
}

func credential(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes {
		return false
	}
	return !strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

// RedirectURL mirrors the template: HTTPS, or HTTP only on a loopback
// development host; never credentials or fragments.
func RedirectURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		host := u.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	}
	return false
}
