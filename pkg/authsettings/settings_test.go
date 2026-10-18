package authsettings

// adr: 1190

import (
	"reflect"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestChangeMapsToSecrets(t *testing.T) {
	set, unset, err := Change{
		SiteURL: ptr("https://shop.example.com"), RedirectURLs: ptr([]string{"https://shop.example.com/cb", "http://localhost:3000/cb"}),
		Signup: ptr(false), Autoconfirm: ptr(true), PasswordMinLength: ptr(12),
		PasswordRequire: ptr([]string{"symbol", "lower"}), LeakedPasswordCheck: ptr(true), MFA: ptr(false),
	}.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"AUTH_SITE_URL":                       "https://shop.example.com",
		"AUTH_REDIRECT_URLS":                  "https://shop.example.com/cb,http://localhost:3000/cb",
		"AUTH_DISABLE_SIGNUP":                 "1",
		"AUTH_AUTOCONFIRM":                    "1",
		"GOTRUE_PASSWORD_MIN_LENGTH":          "12",
		"GOTRUE_PASSWORD_REQUIRED_CHARACTERS": CharacterClasses["lower"] + ":" + CharacterClasses["symbol"],
		"GOTRUE_PASSWORD_HIBP_ENABLED":        "true",
		"GOTRUE_MFA_TOTP_ENROLL_ENABLED":      "false",
		"GOTRUE_MFA_TOTP_VERIFY_ENABLED":      "true",
	}
	if !reflect.DeepEqual(set, want) || len(unset) != 0 {
		t.Fatalf("set = %v unset = %v", set, unset)
	}
	set, unset, err = Change{PasswordRequire: ptr([]string{}), RedirectURLs: ptr([]string{})}.Secrets()
	if err != nil || !reflect.DeepEqual(unset, []string{"GOTRUE_PASSWORD_REQUIRED_CHARACTERS"}) || set["AUTH_REDIRECT_URLS"] != "" || len(set) != 1 {
		t.Fatalf("clearing: set = %v unset = %v err = %v", set, unset, err)
	}
	if !(Change{}).Empty() || (Change{MFA: ptr(true)}).Empty() {
		t.Fatal("Empty")
	}
}

func TestChangeRejectsInvalidValues(t *testing.T) {
	many := make([]string, 51)
	for i := range many {
		many[i] = "https://example.com"
	}
	for name, change := range map[string]Change{
		"http site":          {SiteURL: ptr("http://shop.example.com")},
		"script site":        {SiteURL: ptr("javascript:alert(1)")},
		"credentials":        {RedirectURLs: ptr([]string{"https://u:p@shop.example.com"})},
		"comma":              {RedirectURLs: ptr([]string{"https://a.example.com/x,y"})},
		"too many redirects": {RedirectURLs: &many},
		"short password":     {PasswordMinLength: ptr(5)},
		"long password":      {PasswordMinLength: ptr(73)},
		"unknown class":      {PasswordRequire: ptr([]string{"emoji"})},
	} {
		if _, _, err := change.Secrets(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestProviderSecretsAndValidation(t *testing.T) {
	set, err := Provider("google", "client.apps.example", "s3cret", "")
	if err != nil || !reflect.DeepEqual(set, map[string]string{"GOTRUE_EXTERNAL_GOOGLE_ENABLED": "true", "GOTRUE_EXTERNAL_GOOGLE_CLIENT_ID": "client.apps.example", "GOTRUE_EXTERNAL_GOOGLE_SECRET": "s3cret"}) {
		t.Fatalf("google = %v %v", set, err)
	}
	set, err = Provider("keycloak", "id", "secret", "https://sso.example.com/realms/shop")
	if err != nil || set["GOTRUE_EXTERNAL_KEYCLOAK_URL"] != "https://sso.example.com/realms/shop" {
		t.Fatalf("keycloak = %v %v", set, err)
	}
	if set, err := Provider("linkedin_oidc", "id", "secret", ""); err != nil || set["GOTRUE_EXTERNAL_LINKEDIN_OIDC_ENABLED"] != "true" {
		t.Fatalf("linkedin_oidc = %v %v", set, err)
	}
	for name, args := range map[string][4]string{
		"unknown provider":  {"myspace", "id", "secret", ""},
		"missing client ID": {"google", "", "secret", ""},
		"missing secret":    {"google", "id", "", ""},
		"space in secret":   {"google", "id", "a b", ""},
		"newline in ID":     {"google", "id\n", "secret", ""},
		"long secret":       {"apple", "id", strings.Repeat("s", 4097), ""},
		"keycloak no URL":   {"keycloak", "id", "secret", ""},
		"URL not accepted":  {"google", "id", "secret", "https://accounts.example.com"},
		"http URL":          {"gitlab", "id", "secret", "http://gitlab.example.com"},
	} {
		if _, err := Provider(args[0], args[1], args[2], args[3]); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestCharacterClassesNeedNoEscaping(t *testing.T) {
	if len(ClassOrder) != len(CharacterClasses) {
		t.Fatal("ClassOrder and CharacterClasses differ")
	}
	for _, class := range ClassOrder {
		if chars := CharacterClasses[class]; chars == "" || strings.ContainsAny(chars, ":\\") {
			t.Errorf("class %s = %q", class, chars)
		}
	}
}

func TestPasskeySettings(t *testing.T) {
	set, unset, err := Change{Passkeys: ptr(true), PasskeyRPID: ptr("example.com")}.Secrets()
	if err != nil || !reflect.DeepEqual(set, map[string]string{"GOTRUE_PASSKEY_ENABLED": "true", "AUTH_PASSKEY_RP_ID": "example.com"}) || len(unset) != 0 {
		t.Fatalf("set = %v unset = %v err = %v", set, unset, err)
	}
	set, unset, err = Change{Passkeys: ptr(false), PasskeyRPID: ptr("")}.Secrets()
	if err != nil || set["GOTRUE_PASSKEY_ENABLED"] != "false" || !reflect.DeepEqual(unset, []string{"AUTH_PASSKEY_RP_ID"}) {
		t.Fatalf("clearing: set = %v unset = %v err = %v", set, unset, err)
	}
	for _, bad := range []string{"localhost", "Example.com", "example..com", "-example.com", "https://example.com", "example.com/", "*.example.com"} {
		if _, _, err := (Change{PasskeyRPID: ptr(bad)}).Secrets(); err == nil {
			t.Errorf("passkey domain %q accepted", bad)
		}
	}
}

func TestBrandingCaptchaAndRetention(t *testing.T) {
	set, unset, err := Change{
		UIName: ptr("Shop"), UIColor: ptr("#4F46E5"), UILogoURL: ptr("https://cdn.example.com/logo.svg"),
		Captcha:               &Captcha{Provider: "turnstile", SiteKey: "1x00000000000000000000AA", Secret: "1x0000000000000000000000000000000AA"},
		ActivityRetentionDays: ptr(90),
	}.Secrets()
	want := map[string]string{
		"AUTH_UI_NAME": "Shop", "AUTH_UI_COLOR": "#4f46e5", "AUTH_UI_LOGO_URL": "https://cdn.example.com/logo.svg",
		"GOTRUE_SECURITY_CAPTCHA_ENABLED": "true", "GOTRUE_SECURITY_CAPTCHA_PROVIDER": "turnstile",
		"GOTRUE_SECURITY_CAPTCHA_SECRET": "1x0000000000000000000000000000000AA", "AUTH_CAPTCHA_SITE_KEY": "1x00000000000000000000AA",
		"AUTH_AUDIT_RETENTION_DAYS": "90",
	}
	if err != nil || !reflect.DeepEqual(set, want) || len(unset) != 0 {
		t.Fatalf("set = %v unset = %v err = %v", set, unset, err)
	}
	set, unset, err = Change{UIName: ptr(""), UIColor: ptr(""), UILogoURL: ptr(""), Captcha: &Captcha{}, ActivityRetentionDays: ptr(0)}.Secrets()
	wantUnset := []string{"AUTH_UI_NAME", "AUTH_UI_COLOR", "AUTH_UI_LOGO_URL", "GOTRUE_SECURITY_CAPTCHA_PROVIDER", "GOTRUE_SECURITY_CAPTCHA_SECRET", "AUTH_CAPTCHA_SITE_KEY", "AUTH_AUDIT_RETENTION_DAYS"}
	if err != nil || !reflect.DeepEqual(set, map[string]string{"GOTRUE_SECURITY_CAPTCHA_ENABLED": "false"}) || !reflect.DeepEqual(unset, wantUnset) {
		t.Fatalf("clearing: set = %v unset = %v err = %v", set, unset, err)
	}
	for name, change := range map[string]Change{
		"long name":        {UIName: ptr(strings.Repeat("x", 61))},
		"markup name":      {UIName: ptr("<b>Shop</b>")},
		"named colour":     {UIColor: ptr("red")},
		"short colour":     {UIColor: ptr("#fff")},
		"http logo":        {UILogoURL: ptr("http://cdn.example.com/logo.svg")},
		"logo credentials": {UILogoURL: ptr("https://u:p@cdn.example.com/logo.svg")},
		"recaptcha":        {Captcha: &Captcha{Provider: "recaptcha", SiteKey: "abcdefgh", Secret: "s"}},
		"no site key":      {Captcha: &Captcha{Provider: "hcaptcha", Secret: "s"}},
		"no secret":        {Captcha: &Captcha{Provider: "hcaptcha", SiteKey: "10000000-ffff-ffff-ffff-000000000001"}},
		"negative days":    {ActivityRetentionDays: ptr(-1)},
		"too many days":    {ActivityRetentionDays: ptr(3651)},
	} {
		if _, _, err := change.Secrets(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestMoreMFASettings(t *testing.T) {
	on, off := true, false
	set, _, err := Change{MFAPhone: &on, RecoveryCodes: &off}.Secrets()
	want := map[string]string{"GOTRUE_MFA_PHONE_ENROLL_ENABLED": "true", "GOTRUE_MFA_PHONE_VERIFY_ENABLED": "true",
		"GOTRUE_MFA_RECOVERY_CODES_ENROLL_ENABLED": "false", "GOTRUE_MFA_RECOVERY_CODES_VERIFY_ENABLED": "true"}
	if err != nil || !reflect.DeepEqual(set, want) {
		t.Fatalf("set %v err %v", set, err)
	}
}

func TestOAuthServerSwitches(t *testing.T) {
	set, unset, err := Change{OAuthServer: ptr(true), OAuthRegistration: ptr(false)}.Secrets()
	if err != nil || !reflect.DeepEqual(set, map[string]string{"AUTH_OAUTH_SERVER": "1", "AUTH_OAUTH_REGISTRATION": "0"}) || len(unset) != 0 {
		t.Fatalf("set = %v unset = %v err = %v", set, unset, err)
	}
	if (Change{OAuthRegistration: ptr(true)}).Empty() {
		t.Fatal("a registration change is not empty")
	}
}

func TestAnonymousUsersAndIdentityLinking(t *testing.T) {
	set, unset, err := Change{Anonymous: ptr(true), AnonymousRetentionDays: ptr(30), IdentityLinking: ptr(true)}.Secrets()
	want := map[string]string{"GOTRUE_EXTERNAL_ANONYMOUS_USERS_ENABLED": "true", "AUTH_ANONYMOUS_RETENTION_DAYS": "30", "AUTH_IDENTITY_LINKING": "1"}
	if err != nil || !reflect.DeepEqual(set, want) || len(unset) != 0 {
		t.Fatalf("set = %v unset = %v err = %v", set, unset, err)
	}
	set, unset, err = Change{Anonymous: ptr(false), AnonymousRetentionDays: ptr(0)}.Secrets()
	if err != nil || set["GOTRUE_EXTERNAL_ANONYMOUS_USERS_ENABLED"] != "false" || !reflect.DeepEqual(unset, []string{"AUTH_ANONYMOUS_RETENTION_DAYS"}) {
		t.Fatalf("off: set = %v unset = %v err = %v", set, unset, err)
	}
	for _, days := range []int{-1, 3651} {
		if _, _, err := (Change{AnonymousRetentionDays: ptr(days)}).Secrets(); err == nil {
			t.Fatalf("accepted %d days", days)
		}
	}
}

func TestSelfDeletion(t *testing.T) {
	for value, want := range map[bool]string{true: "1", false: "0"} {
		set, unset, err := Change{SelfDeletion: ptr(value)}.Secrets()
		if err != nil || len(unset) != 0 || !reflect.DeepEqual(set, map[string]string{"AUTH_SELF_DELETION": want}) {
			t.Fatalf("SelfDeletion %v: set %v unset %v err %v", value, set, unset, err)
		}
	}
}

func TestOrgClaims(t *testing.T) {
	for value, want := range map[bool]string{true: "1", false: "0"} {
		set, unset, err := Change{OrgClaims: ptr(value)}.Secrets()
		if err != nil || len(unset) != 0 || !reflect.DeepEqual(set, map[string]string{"AUTH_ORG_CLAIMS": want}) {
			t.Fatalf("OrgClaims %v: set %v unset %v err %v", value, set, unset, err)
		}
	}
}
