package productcontrol

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func ValidOnboardingText(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}
