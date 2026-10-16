package authsettings

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// SMSProviders are the SMS services GoTrue v2.197.0 sends through over
// HTTPS with the customer's own account. Gregale sells no messages.
var SMSProviders = []string{"twilio", "twilio_verify", "messagebird", "textlocal", "vonage"}

// smsFields names each provider's GOTRUE_SMS_<PROVIDER>_* settings for the
// three values the CLI and dashboard take: the account (empty when the
// provider has none), the sender and the secret.
var smsFields = map[string]struct{ account, sender, secret string }{
	"twilio":        {"ACCOUNT_SID", "MESSAGE_SERVICE_SID", "AUTH_TOKEN"},
	"twilio_verify": {"ACCOUNT_SID", "MESSAGE_SERVICE_SID", "AUTH_TOKEN"},
	"messagebird":   {"", "ORIGINATOR", "ACCESS_KEY"},
	"textlocal":     {"", "SENDER", "API_KEY"},
	"vonage":        {"API_KEY", "FROM", "API_SECRET"},
}

const (
	secretPhoneEnabled = "GOTRUE_EXTERNAL_PHONE_ENABLED"
	secretSMSProvider  = "GOTRUE_SMS_PROVIDER"
	smsValueMaxBytes   = 256
)

// SMS is phone sign-in by text message. An empty Provider turns it off;
// otherwise Sender is the provider's sender (a Twilio messaging or Verify
// service SID or phone number, a MessageBird originator, a Textlocal sender
// or a Vonage from number or name), Account the Twilio account SID or Vonage
// API key, and Secret the Twilio auth token, MessageBird access key,
// Textlocal API key or Vonage API secret.
type SMS struct{ Provider, Account, Sender, Secret string }

// SMSSecretPrefix is the secret-name prefix of one provider's settings.
func SMSSecretPrefix(provider string) string {
	return "GOTRUE_SMS_" + strings.ToUpper(provider) + "_"
}

// SMSAccountRequired reports whether provider takes an account.
func SMSAccountRequired(provider string) bool { return smsFields[provider].account != "" }

// secrets returns what to set and remove. Every other provider's settings
// are removed, so switching providers leaves no stale credential.
func (s SMS) secrets() (map[string]string, []string, error) {
	unset := []string{}
	for _, provider := range SMSProviders {
		if provider == s.Provider {
			continue
		}
		f := smsFields[provider]
		for _, name := range []string{f.account, f.sender, f.secret} {
			if name != "" {
				unset = append(unset, SMSSecretPrefix(provider)+name)
			}
		}
	}
	if s.Provider == "" {
		return map[string]string{secretPhoneEnabled: "false"}, append(unset, secretSMSProvider), nil
	}
	if !slices.Contains(SMSProviders, s.Provider) {
		return nil, nil, fmt.Errorf("unknown SMS provider %q; use one of %s", s.Provider, strings.Join(SMSProviders, ", "))
	}
	f, prefix := smsFields[s.Provider], SMSSecretPrefix(s.Provider)
	set := map[string]string{secretPhoneEnabled: "true", secretSMSProvider: s.Provider}
	switch {
	case f.account != "" && !credential(s.Account, smsValueMaxBytes):
		return nil, nil, fmt.Errorf("%s requires its account (the Twilio account SID or Vonage API key) without spaces", s.Provider)
	case f.account == "" && s.Account != "":
		return nil, nil, fmt.Errorf("%s takes no account", s.Provider)
	case !credential(s.Sender, smsValueMaxBytes):
		return nil, nil, errors.New("the SMS sender is required and must not contain spaces or control characters")
	case !credential(s.Secret, api.AuthProviderSecretMaxBytes):
		return nil, nil, errors.New("the SMS provider secret is required and must not contain spaces or control characters")
	}
	if f.account != "" {
		set[prefix+f.account] = s.Account
	}
	set[prefix+f.sender], set[prefix+f.secret] = s.Sender, s.Secret
	return set, unset, nil
}
