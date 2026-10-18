// Package authsettings maps typed application Auth settings (ADR-1190) onto
// the Auth app's secrets. The CLI and the dashboard share it, so both accept
// exactly the same values. The Auth template (cmd/gregale/templates/auth/
// config.mjs) reads these secrets; TestAuthTemplateSettingsContract in
// cmd/gregale keeps the provider names and character classes in step.
package authsettings

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

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
	secretPhoneMFAEnroll  = "GOTRUE_MFA_PHONE_ENROLL_ENABLED"
	secretPhoneMFAVerify  = "GOTRUE_MFA_PHONE_VERIFY_ENABLED"
	secretRecoveryEnroll  = "GOTRUE_MFA_RECOVERY_CODES_ENROLL_ENABLED"
	secretRecoveryVerify  = "GOTRUE_MFA_RECOVERY_CODES_VERIFY_ENABLED"
	secretPasskeys        = "GOTRUE_PASSKEY_ENABLED"
	secretPasskeyRPID     = "AUTH_PASSKEY_RP_ID"
	secretUIName          = "AUTH_UI_NAME"
	secretUIColor         = "AUTH_UI_COLOR"
	secretUILogoURL       = "AUTH_UI_LOGO_URL"
	secretCaptchaEnabled  = "GOTRUE_SECURITY_CAPTCHA_ENABLED"
	secretCaptchaProvider = "GOTRUE_SECURITY_CAPTCHA_PROVIDER"
	secretCaptchaSecret   = "GOTRUE_SECURITY_CAPTCHA_SECRET"
	secretCaptchaSiteKey  = "AUTH_CAPTCHA_SITE_KEY"
	secretAuditRetention  = "AUTH_AUDIT_RETENTION_DAYS"
	secretAnonymous       = "GOTRUE_EXTERNAL_ANONYMOUS_USERS_ENABLED"
	secretAnonRetention   = "AUTH_ANONYMOUS_RETENTION_DAYS"
	secretIdentityLinking = "AUTH_IDENTITY_LINKING"
	secretSelfDeletion    = "AUTH_SELF_DELETION"
	secretOrgClaims       = "AUTH_ORG_CLAIMS"
	secretSignupAllowed   = "AUTH_SIGNUP_ALLOWED_DOMAINS"
	secretSignupBlocked   = "AUTH_SIGNUP_BLOCKED_DOMAINS"
	secretBlockDisposable = "AUTH_SIGNUP_BLOCK_DISPOSABLE"
	secretSignupAccess    = "AUTH_SIGNUP_ACCESS"
	secretImpersonation   = "AUTH_IMPERSONATION"
	secretSessionMaxAge   = "AUTH_SESSION_MAX_AGE_MINUTES"
	secretSessionIdle     = "AUTH_SESSION_IDLE_MINUTES"
	secretSingleSession   = "AUTH_SINGLE_SESSION"
)

// SignupAccessModes are who may sign up: anyone, invited or approved
// addresses only, or those plus a waitlist that refused sign-ups join.
var SignupAccessModes = []string{"open", "invite", "waitlist"}

// Web3Chains are the wallets GoTrue verifies sign-ins from, with the secret
// that turns each on.
var Web3Chains = map[string]string{"ethereum": "GOTRUE_EXTERNAL_WEB3_ETHEREUM_ENABLED", "solana": "GOTRUE_EXTERNAL_WEB3_SOLANA_ENABLED"}

// CaptchaProviders are the CAPTCHA services GoTrue verifies and the hosted
// pages render.
var CaptchaProviders = []string{"turnstile", "hcaptcha"}

var (
	brandColor  = regexp.MustCompile(`^#[0-9a-f]{6}$`)
	captchaSite = regexp.MustCompile(`^[A-Za-z0-9_-]{8,100}$`)
)

// hostname matches a lowercase DNS name; the Auth app also checks that the
// passkey domain is its own host or a parent of it.
var hostname = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

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
	// MFAPhone turns enrolment of SMS codes as a second factor on or off;
	// RecoveryCodes does the same for one-time recovery codes. As with
	// TOTP, verification stays on, so enrolled users keep signing in.
	MFAPhone      *bool
	RecoveryCodes *bool
	// Passkeys turns passkey sign-in on or off. PasskeyRPID names a parent
	// domain the Auth app shares with the site so both use the same
	// passkeys; empty returns to the Auth app's own host.
	Passkeys    *bool
	PasskeyRPID *string
	// UIName, UIColor (#rrggbb) and UILogoURL (HTTPS) brand the hosted
	// pages; empty removes a value.
	UIName    *string
	UIColor   *string
	UILogoURL *string
	// Captcha turns the CAPTCHA check on (a provider, its site key and
	// secret) or off (an empty provider).
	Captcha *Captcha
	// ActivityRetentionDays prunes older activity; 0 keeps it forever.
	ActivityRetentionDays *int
	// Email sets the HTTPS email provider, or removes it (empty Provider).
	Email *Email
	// SMS turns phone sign-in by text message on with a provider, or off
	// (empty Provider).
	SMS *SMS
	// SSO turns organization single sign-on (SAML) on or off. Turning it on
	// also needs SAMLKeySecret, which the caller creates once.
	SSO *bool
	// SMSCountries lists the country calling codes phone sign-in may text;
	// empty allows every country.
	SMSCountries *[]string
	// OrgSSO lets organizations verify their email domains and connect
	// their own SAML identity provider; it needs SSO.
	OrgSSO *bool
	// OAuthServer turns the OAuth 2.1 authorization server on or off.
	// OAuthRegistration lets clients register themselves (RFC 7591), as MCP
	// clients do; it takes effect only while the server is on.
	OAuthServer       *bool
	OAuthRegistration *bool
	// Anonymous lets people start without an account (signInAnonymously);
	// AnonymousRetentionDays deletes anonymous users idle that long, 0 keeps
	// them. IdentityLinking lets signed-in users link and unlink sign-in
	// methods, which is how an anonymous user becomes permanent with a
	// provider account.
	Anonymous              *bool
	AnonymousRetentionDays *int
	IdentityLinking        *bool
	// SelfDeletion lets signed-in users delete their own accounts after a
	// recent sign-in (the hosted account page, deleteAccount()).
	SelfDeletion *bool
	// OrgClaims puts each session's active organization, role and
	// permissions into its access tokens (org_* claims).
	OrgClaims *bool
	// SignupAllowedDomains limits sign-up to addresses at these domains;
	// SignupBlockedDomains refuses these domains and their subdomains. Empty
	// removes the list. BlockDisposable refuses throwaway addresses.
	SignupAllowedDomains *[]string
	SignupBlockedDomains *[]string
	BlockDisposable      *bool
	// SignupAccess is one of SignupAccessModes.
	SignupAccess *string
	// Impersonation lets the admin key open time-limited sessions as users.
	Impersonation *bool
	// Web3 lists the Web3Chains people may sign in with a wallet from;
	// empty turns wallet sign-in off.
	Web3 *[]string
	// SessionMaxAgeMinutes and SessionIdleMinutes end a session that long
	// after its sign-in or its last refresh; 0 removes the limit.
	// SingleSession keeps only each user's most recently refreshed session.
	SessionMaxAgeMinutes *int
	SessionIdleMinutes   *int
	SingleSession        *bool
}

var sessionLimit = regexp.MustCompile(`^([1-9][0-9]{0,5})([mhd])$`)

// ParseSessionLimit reads a session limit written in minutes (45m), hours
// (12h) or days (30d), or off (or empty) for none, as minutes.
func ParseSessionLimit(value string) (int, error) {
	if value == "" || value == "off" {
		return 0, nil
	}
	match := sessionLimit.FindStringSubmatch(value)
	if match == nil {
		return 0, fmt.Errorf("%q is not a duration such as 45m, 12h or 30d, or off", value)
	}
	minutes, _ := strconv.Atoi(match[1])
	minutes *= map[string]int{"m": 1, "h": 60, "d": 24 * 60}[match[2]]
	if minutes < api.AuthSessionMinMinutes || minutes > api.AuthSessionMaxMinutes {
		return 0, fmt.Errorf("a session limit must be %s to %s; got %s", FormatSessionLimit(api.AuthSessionMinMinutes), FormatSessionLimit(api.AuthSessionMaxMinutes), value)
	}
	return minutes, nil
}

// FormatSessionLimit writes minutes the way ParseSessionLimit reads them.
func FormatSessionLimit(minutes int) string {
	switch {
	case minutes <= 0:
		return "off"
	case minutes%(24*60) == 0:
		return strconv.Itoa(minutes/(24*60)) + "d"
	case minutes%60 == 0:
		return strconv.Itoa(minutes/60) + "h"
	}
	return strconv.Itoa(minutes) + "m"
}

// Captcha is a CAPTCHA provider and its keys; the secret stays in the app.
type Captcha struct{ Provider, SiteKey, Secret string }

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
	if c.MFAPhone != nil {
		set[secretPhoneMFAEnroll], set[secretPhoneMFAVerify] = strconv.FormatBool(*c.MFAPhone), "true"
	}
	if c.RecoveryCodes != nil {
		set[secretRecoveryEnroll], set[secretRecoveryVerify] = strconv.FormatBool(*c.RecoveryCodes), "true"
	}
	if c.Passkeys != nil {
		set[secretPasskeys] = strconv.FormatBool(*c.Passkeys)
	}
	if err := c.brandingSecrets(set, &unset); err != nil {
		return nil, nil, err
	}
	if err := c.captchaSecrets(set, &unset); err != nil {
		return nil, nil, err
	}
	if c.ActivityRetentionDays != nil {
		days := *c.ActivityRetentionDays
		switch {
		case days == 0:
			unset = append(unset, secretAuditRetention)
		case days < 0 || days > api.AuthAuditRetentionMaxDays:
			return nil, nil, fmt.Errorf("activity retention must be 1 to %d days, or 0 to keep activity forever; got %d", api.AuthAuditRetentionMaxDays, days)
		default:
			set[secretAuditRetention] = strconv.Itoa(days)
		}
	}
	if c.Email != nil {
		if c.Email.Provider == "" {
			unset = append(unset, EmailSecrets...)
		} else {
			email, err := c.Email.secrets()
			if err != nil {
				return nil, nil, err
			}
			maps.Copy(set, email)
		}
	}
	if c.SSO != nil {
		set["AUTH_SSO"] = flag(*c.SSO)
	}
	if c.SMSCountries != nil {
		value, err := smsCountries(*c.SMSCountries)
		if err != nil {
			return nil, nil, err
		}
		if value == "" {
			unset = append(unset, secretSMSCountries)
		} else {
			set[secretSMSCountries] = value
		}
	}
	if c.OrgSSO != nil {
		set["AUTH_ORG_SSO"] = flag(*c.OrgSSO)
	}
	if c.OAuthServer != nil {
		set["AUTH_OAUTH_SERVER"] = flag(*c.OAuthServer)
	}
	if c.OAuthRegistration != nil {
		set["AUTH_OAUTH_REGISTRATION"] = flag(*c.OAuthRegistration)
	}
	if c.Anonymous != nil {
		set[secretAnonymous] = strconv.FormatBool(*c.Anonymous)
	}
	if c.IdentityLinking != nil {
		set[secretIdentityLinking] = flag(*c.IdentityLinking)
	}
	if c.SelfDeletion != nil {
		set[secretSelfDeletion] = flag(*c.SelfDeletion)
	}
	if c.OrgClaims != nil {
		set[secretOrgClaims] = flag(*c.OrgClaims)
	}
	for secret, list := range map[string]*[]string{secretSignupAllowed: c.SignupAllowedDomains, secretSignupBlocked: c.SignupBlockedDomains} {
		if list == nil {
			continue
		}
		value, err := signupDomains(*list)
		if err != nil {
			return nil, nil, err
		}
		if value == "" {
			unset = append(unset, secret)
		} else {
			set[secret] = value
		}
	}
	if c.BlockDisposable != nil {
		set[secretBlockDisposable] = flag(*c.BlockDisposable)
	}
	if c.SignupAccess != nil {
		if !slices.Contains(SignupAccessModes, *c.SignupAccess) {
			return nil, nil, fmt.Errorf("unknown sign-up access %q; use %s", *c.SignupAccess, strings.Join(SignupAccessModes, ", "))
		}
		set[secretSignupAccess] = *c.SignupAccess
	}
	if c.Impersonation != nil {
		set[secretImpersonation] = flag(*c.Impersonation)
	}
	if c.Web3 != nil {
		on := map[string]bool{}
		for _, chain := range *c.Web3 {
			chain = strings.ToLower(strings.TrimSpace(chain))
			if _, known := Web3Chains[chain]; !known {
				return nil, nil, fmt.Errorf("unknown wallet chain %q; use ethereum, solana or both", chain)
			}
			on[chain] = true
		}
		for chain, secret := range Web3Chains {
			set[secret] = strconv.FormatBool(on[chain])
		}
	}
	for _, limit := range []struct {
		minutes      *int
		secret, name string
	}{{c.SessionMaxAgeMinutes, secretSessionMaxAge, "maximum session age"}, {c.SessionIdleMinutes, secretSessionIdle, "session idle timeout"}} {
		switch {
		case limit.minutes == nil:
		case *limit.minutes == 0:
			unset = append(unset, limit.secret)
		case *limit.minutes < api.AuthSessionMinMinutes || *limit.minutes > api.AuthSessionMaxMinutes:
			return nil, nil, fmt.Errorf("%s must be %d to %d minutes, or 0 for none; got %d", limit.name, api.AuthSessionMinMinutes, api.AuthSessionMaxMinutes, *limit.minutes)
		default:
			set[limit.secret] = strconv.Itoa(*limit.minutes)
		}
	}
	if c.SingleSession != nil {
		set[secretSingleSession] = flag(*c.SingleSession)
	}
	if c.AnonymousRetentionDays != nil {
		days := *c.AnonymousRetentionDays
		switch {
		case days == 0:
			unset = append(unset, secretAnonRetention)
		case days < 0 || days > api.AuthAnonymousRetentionMaxDays:
			return nil, nil, fmt.Errorf("anonymous user retention must be 1 to %d days, or 0 to keep them; got %d", api.AuthAnonymousRetentionMaxDays, days)
		default:
			set[secretAnonRetention] = strconv.Itoa(days)
		}
	}
	if c.SMS != nil {
		sms, removed, err := c.SMS.secrets()
		if err != nil {
			return nil, nil, err
		}
		maps.Copy(set, sms)
		unset = append(unset, removed...)
	}
	if c.PasskeyRPID != nil {
		switch {
		case *c.PasskeyRPID == "":
			unset = append(unset, secretPasskeyRPID)
		case len(*c.PasskeyRPID) > 253 || !hostname.MatchString(*c.PasskeyRPID):
			return nil, nil, fmt.Errorf("passkey domain %q must be a lowercase domain name such as example.com", *c.PasskeyRPID)
		default:
			set[secretPasskeyRPID] = *c.PasskeyRPID
		}
	}
	return set, unset, nil
}

func (c Change) brandingSecrets(set map[string]string, unset *[]string) error {
	optional := func(value *string, name string, valid func(string) bool, problem string) error {
		switch {
		case value == nil:
		case *value == "":
			*unset = append(*unset, name)
		case !valid(*value):
			return errors.New(problem)
		default:
			set[name] = *value
		}
		return nil
	}
	if c.UIColor != nil {
		lower := strings.ToLower(*c.UIColor)
		c.UIColor = &lower
	}
	if err := optional(c.UIName, secretUIName, func(v string) bool {
		return utf8.RuneCountInString(v) <= api.AuthUINameMaxChars && !strings.ContainsAny(v, "<>") && !strings.ContainsFunc(v, unicode.IsControl)
	}, fmt.Sprintf("the display name must be at most %d characters without angle brackets or control characters", api.AuthUINameMaxChars)); err != nil {
		return err
	}
	if err := optional(c.UIColor, secretUIColor, brandColor.MatchString, "the brand colour must be #rrggbb"); err != nil {
		return err
	}
	return optional(c.UILogoURL, secretUILogoURL, func(v string) bool {
		u, err := url.Parse(v)
		return err == nil && len(v) <= 2048 && u.Scheme == "https" && u.Host != "" && u.User == nil
	}, "the logo URL must be an HTTPS URL without credentials")
}

func (c Change) captchaSecrets(set map[string]string, unset *[]string) error {
	if c.Captcha == nil {
		return nil
	}
	if c.Captcha.Provider == "" {
		set[secretCaptchaEnabled] = "false"
		*unset = append(*unset, secretCaptchaProvider, secretCaptchaSecret, secretCaptchaSiteKey)
		return nil
	}
	if !slices.Contains(CaptchaProviders, c.Captcha.Provider) {
		return fmt.Errorf("unknown CAPTCHA provider %q; use %s", c.Captcha.Provider, strings.Join(CaptchaProviders, " or "))
	}
	if !captchaSite.MatchString(c.Captcha.SiteKey) {
		return errors.New("the CAPTCHA site key is required and must be 8 to 100 letters, digits, - or _")
	}
	if !credential(c.Captcha.Secret, api.AuthProviderSecretMaxBytes) {
		return errors.New("the CAPTCHA secret is required and must not contain spaces or control characters")
	}
	set[secretCaptchaEnabled], set[secretCaptchaProvider], set[secretCaptchaSecret], set[secretCaptchaSiteKey] = "true", c.Captcha.Provider, c.Captcha.Secret, c.Captcha.SiteKey
	return nil
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

// signupDomains validates a list of sign-up domains and joins it; empty
// clears the list.
func signupDomains(list []string) (string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range list {
		domain := strings.ToLower(strings.TrimSpace(raw))
		if domain == "" || seen[domain] {
			continue
		}
		if !hostname.MatchString(domain) {
			return "", fmt.Errorf("sign-up domain %q must be a domain name such as acme.com", raw)
		}
		seen[domain] = true
		out = append(out, domain)
	}
	if len(out) > api.AuthSignupDomainsMax {
		return "", fmt.Errorf("at most %d sign-up domains are allowed in a list; got %d", api.AuthSignupDomainsMax, len(out))
	}
	return strings.Join(out, ","), nil
}
