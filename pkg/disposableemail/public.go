package disposableemail

import "strings"

// publicProviders are free mailbox providers anyone can sign up to. Two
// accounts on one of these domains say nothing about each other, so the
// held-account linking (ADR-20261114174600) never links on them. Curated, not
// exhaustive: a missing provider can only add flag-only links.
var publicProviders = map[string]struct{}{
	"gmail.com": {}, "googlemail.com": {},
	"outlook.com": {}, "hotmail.com": {}, "hotmail.co.uk": {}, "hotmail.fr": {}, "hotmail.de": {}, "live.com": {}, "msn.com": {},
	"yahoo.com": {}, "yahoo.co.uk": {}, "yahoo.fr": {}, "yahoo.de": {}, "yahoo.co.jp": {}, "ymail.com": {}, "rocketmail.com": {},
	"icloud.com": {}, "me.com": {}, "mac.com": {},
	"aol.com": {}, "aim.com": {},
	"proton.me": {}, "protonmail.com": {}, "pm.me": {},
	"gmx.com": {}, "gmx.de": {}, "gmx.net": {}, "web.de": {}, "mail.com": {},
	"zoho.com": {}, "zohomail.com": {}, "yandex.com": {}, "yandex.ru": {}, "mail.ru": {},
	"tutanota.com": {}, "tuta.io": {}, "fastmail.com": {}, "hey.com": {},
	"qq.com": {}, "163.com": {}, "126.com": {}, "naver.com": {},
	"orange.fr": {}, "free.fr": {}, "laposte.net": {}, "t-online.de": {}, "libero.it": {},
	"comcast.net": {}, "verizon.net": {}, "att.net": {}, "sbcglobal.net": {},
}

// PublicProvider reports whether domain is a free mailbox provider.
func PublicProvider(domain string) bool {
	_, ok := publicProviders[strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")]
	return ok
}

// Linkable reports whether two accounts sharing domain are worth linking: a
// private domain, neither a public provider nor a disposable one.
func Linkable(domain string) bool {
	return domain != "" && !PublicProvider(domain) && !Domain(domain)
}
