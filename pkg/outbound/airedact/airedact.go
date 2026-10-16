// Package airedact finds and redacts personal data and secrets in AI
// request and response text (ADR-997). Detectors favor precision: card
// numbers must pass the Luhn check and IBANs the ISO 7064 check, and secrets
// are matched by their issuers' published prefixes.
package airedact

import (
	"bytes"
	"regexp"
	"sort"
	"strconv"
)

// Kind names one detector.
type Kind string

const (
	Email       Kind = "email"
	PaymentCard Kind = "payment_card"
	IBAN        Kind = "iban"
	Secret      Kind = "secret"
)

// Kinds lists every detector in a stable order.
var Kinds = []Kind{Email, PaymentCard, IBAN, Secret}

// Finding is one match, as a byte range of the scanned text.
type Finding struct {
	Kind       Kind
	Start, End int
}

var (
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?)*\.[A-Za-z]{2,}`)
	// Candidates: digits in groups separated by single spaces or hyphens,
	// and IBAN-shaped runs. Boundaries and checksums are checked in code,
	// since RE2 has no lookaround.
	cardPattern = regexp.MustCompile(`[0-9](?:[ \-]?[0-9]){12,18}`)
	ibanPattern = regexp.MustCompile(`[A-Z]{2}[0-9]{2}(?: ?[A-Z0-9]){11,30}`)
	// Published key formats of common issuers, JWTs, bearer tokens and PEM
	// private keys.
	secretPattern = regexp.MustCompile(anyOf(
		`sk-(?:proj-|ant-(?:api|admin)[0-9]{2}-|svcacct-)?[A-Za-z0-9_\-]{20,}`,
		`(?:AKIA|ASIA)[0-9A-Z]{16}`,
		`gh[pousr]_[A-Za-z0-9]{36,}`,
		`github_pat_[A-Za-z0-9_]{22,}`,
		`xox[abprs]-[A-Za-z0-9\-]{10,}`,
		`AIza[0-9A-Za-z_\-]{35}`,
		`(?:sk|rk)_live_[0-9A-Za-z]{24,}`,
		`eyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`,
		`[Bb]earer [A-Za-z0-9._~+/\-]{20,}=*`,
		`-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----(?:[\s\S]*?-----END (?:[A-Z]+ )?PRIVATE KEY-----|[\s\S]*)`,
	))
)

func anyOf(alternatives ...string) string {
	var b bytes.Buffer
	for index, alternative := range alternatives {
		if index > 0 {
			b.WriteByte('|')
		}
		b.WriteString("(?:" + alternative + ")")
	}
	return b.String()
}

// Find returns the non-overlapping findings of the given kinds in text,
// ordered by position. A secret wins over another kind on the same bytes.
func Find(text []byte, kinds ...Kind) []Finding {
	var found []Finding
	for _, kind := range kinds {
		switch kind {
		case Email:
			for _, match := range emailPattern.FindAllIndex(text, -1) {
				found = append(found, Finding{Email, match[0], match[1]})
			}
		case PaymentCard:
			found = appendChecked(found, text, cardPattern, PaymentCard, 13, luhnValid)
		case IBAN:
			found = appendChecked(found, text, ibanPattern, IBAN, 15, ibanValid)
		case Secret:
			for _, match := range secretPattern.FindAllIndex(text, -1) {
				found = append(found, Finding{Secret, match[0], match[1]})
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].Start != found[j].Start {
			return found[i].Start < found[j].Start
		}
		return found[i].Kind == Secret && found[j].Kind != Secret
	})
	kept := found[:0]
	end := -1
	for _, finding := range found {
		if finding.Start >= end {
			kept = append(kept, finding)
			end = finding.End
		}
	}
	return kept
}

// Redact replaces each finding of the given kinds with "[REDACTED:kind]"
// and reports how many of each it replaced. Text without findings is
// returned unchanged.
func Redact(text []byte, kinds ...Kind) ([]byte, map[Kind]int) {
	found := Find(text, kinds...)
	if len(found) == 0 {
		return text, nil
	}
	counts := make(map[Kind]int, len(kinds))
	out := make([]byte, 0, len(text))
	last := 0
	for _, finding := range found {
		out = append(out, text[last:finding.Start]...)
		out = append(out, "[REDACTED:"+string(finding.Kind)+"]"...)
		last = finding.End
		counts[finding.Kind]++
	}
	return append(out, text[last:]...), counts
}

// appendChecked adds the candidates of pattern that stand alone and pass
// valid. A candidate that runs into neighboring characters is shortened
// from the end, so "4111 1111 1111 1111 22" still finds the card.
func appendChecked(found []Finding, text []byte, pattern *regexp.Regexp, kind Kind, minimum int,
	valid func([]byte) bool) []Finding {
	for _, match := range pattern.FindAllIndex(text, -1) {
		start := match[0]
		if start > 0 && alphanumeric(text[start-1]) {
			continue
		}
		var symbols []int
		for index := start; index < match[1]; index++ {
			if text[index] != ' ' && text[index] != '-' {
				symbols = append(symbols, index)
			}
		}
		for count := len(symbols); count >= minimum; count-- {
			end := symbols[count-1] + 1
			if end < len(text) && alphanumeric(text[end]) {
				continue
			}
			compact := make([]byte, 0, count)
			for _, index := range symbols[:count] {
				compact = append(compact, text[index])
			}
			if valid(compact) {
				found = append(found, Finding{kind, start, end})
				break
			}
		}
	}
	return found
}

func alphanumeric(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// luhnValid checks a 13 to 19 digit card number.
func luhnValid(digits []byte) bool {
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	for index := range digits {
		digit := int(digits[len(digits)-1-index] - '0')
		if index%2 == 1 {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
	}
	return sum%10 == 0
}

// ibanValid checks length and the ISO 7064 mod-97 remainder of 1.
func ibanValid(iban []byte) bool {
	if len(iban) < 15 || len(iban) > 34 {
		return false
	}
	rearranged := append(append([]byte{}, iban[4:]...), iban[:4]...)
	remainder := 0
	for _, c := range rearranged {
		var value int
		switch {
		case c >= '0' && c <= '9':
			value = int(c - '0')
		case c >= 'A' && c <= 'Z':
			value = int(c-'A') + 10
		default:
			return false
		}
		for _, digit := range strconv.Itoa(value) {
			remainder = (remainder*10 + int(digit-'0')) % 97
		}
	}
	return remainder == 1
}
