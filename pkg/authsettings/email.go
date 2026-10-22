package authsettings

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

// EmailProviders are the HTTPS email services the Auth app sends through;
// tenant egress blocks SMTP.
var EmailProviders = []string{"resend", "postmark"}

// EmailSecrets are the email delivery settings, set and removed together.
var EmailSecrets = []string{"AUTH_EMAIL_PROVIDER", "AUTH_EMAIL_FROM", "AUTH_EMAIL_API_KEY"}

// EmailFromPattern is the template's AUTH_EMAIL_FROM rule (emailConfig in
// config.mjs): an address, or a display name with the address in angle
// brackets. TestAuthTemplateSettingsContract keeps the two in step, because a
// sender the template refuses stops the Auth app from starting.
const EmailFromPattern = `^([^<>\r\n]{1,100} <[^@\s<>]+@[^@\s<>]+\.[^@\s<>]+>|[^@\s<>]+@[^@\s<>]+\.[^@\s<>]+)$`

var emailFrom = regexp.MustCompile(EmailFromPattern)

// Email is the email delivery setting. An empty Provider removes it; any
// other change takes all three values, because the API key is write-only.
type Email struct{ Provider, From, APIKey string }

func (e Email) secrets() (map[string]string, error) {
	if !slices.Contains(EmailProviders, e.Provider) {
		return nil, errors.New("the email provider must be resend or postmark")
	}
	if !EmailFrom(e.From) {
		return nil, errors.New(`the sender must be an address or "Name <address>"`)
	}
	if len(e.APIKey) < 8 || !credential(e.APIKey, api.AuthEmailAPIKeyMaxBytes) {
		return nil, errors.New("the email provider API key is required and looks malformed")
	}
	return map[string]string{EmailSecrets[0]: e.Provider, EmailSecrets[1]: e.From, EmailSecrets[2]: e.APIKey}, nil
}

// EmailFrom reports whether the template accepts from as the sender.
// JavaScript's \s also matches Unicode spaces, so they are refused here too.
func EmailFrom(from string) bool {
	if !emailFrom.MatchString(from) {
		return false
	}
	address := from
	if open := strings.LastIndexByte(from, '<'); open >= 0 {
		address = from[open:]
	}
	return !strings.ContainsFunc(address, unicode.IsSpace)
}
