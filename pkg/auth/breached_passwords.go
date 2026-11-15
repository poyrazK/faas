package auth

import (
	_ "embed"
	"errors"
	"strings"
)

// ErrPasswordBreached is returned by Validate for a password on the bundled
// list of common breached passwords, a single repeated character, or a plain
// run of consecutive digits or letters. Attackers try these first, so
// length alone does not make them safe (ADR-20261115160000).
var ErrPasswordBreached = errors.New("this password is too common: it appears in breached-password lists or is a simple pattern; choose another")

//go:embed breached_passwords.txt
var breachedPasswordList string

var breachedPasswords = parseBreachedPasswords(breachedPasswordList)

func parseBreachedPasswords(list string) map[string]struct{} {
	out := map[string]struct{}{}
	for line := range strings.SplitSeq(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		out[strings.ToLower(line)] = struct{}{}
	}
	return out
}

// commonPassword reports whether plaintext is on the breached list (ignoring
// case), one repeated character, or a run of consecutive characters such as
// "123456789012" or "abcdefghijklm".
func commonPassword(plaintext string) bool {
	lower := strings.ToLower(plaintext)
	if _, ok := breachedPasswords[lower]; ok {
		return true
	}
	return repeatedOrSequential(lower)
}

func repeatedOrSequential(s string) bool {
	if len(s) < 2 {
		return false
	}
	same, up, down := true, true, true
	for i := 1; i < len(s); i++ {
		d := int(s[i]) - int(s[i-1])
		same = same && d == 0
		up = up && (d == 1 || (s[i-1] == '9' && s[i] == '0'))
		down = down && (d == -1 || (s[i-1] == '0' && s[i] == '9'))
	}
	return same || up || down
}
