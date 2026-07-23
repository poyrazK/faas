package main

// Spec §11 Control plane — "apid input validation is the trust boundary —
// fuzz it". Today the only validation coverage is the tarball-shape unit
// tests in deploy_inputs_test.go; there is no apid-targeted fuzzer. This
// file pins the three package-private validators in handlers.go so a
// future refactor cannot regress their shape (or start accepting control
// chars / multiline strings / unicode confusables) without a failing seed.
//
// Run as part of `make test`. The corpus seeds run on every go test; the
// `go test -fuzz=FuzzValidSlug -fuzztime=30s ./cmd/apid/...` invocation is
// deliberately deferred to a nightly CI job (out of scope here).

import (
	"regexp"
	"strings"
	"testing"
)

// recoverPanic converts an unexpected panic inside the fuzzer body into a
// failed seed. Without this a future refactor that introduces a panic on a
// pathological input would hang the test binary instead of reporting. The
// fuzz closure receives *testing.T, not *testing.F.
func recoverPanic(t *testing.T) {
	if r := recover(); r != nil {
		t.Errorf("fuzz target panicked: %v", r)
	}
}

// slugShape mirrors validSlug's regex (^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$)
// so the fuzzer can independently verify the return value's properties
// without re-using the regex under test.
var slugShape = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)

func FuzzValidSlug(f *testing.F) {
	// Seeds cover: typical accept, typical reject on space/upper/underscore,
	// control chars, empty, leading/trailing dash, exactly-length boundaries,
	// too long, embedded NUL, and unicode confusables.
	f.Add("abc")
	f.Add("a-b-c")
	f.Add("foo-bar-1")
	f.Add("Foo")                    // uppercase rejected
	f.Add("foo bar")                // space rejected
	f.Add("foo_bar")                // underscore rejected
	f.Add("")                       // empty rejected
	f.Add("-abc")                   // leading dash rejected
	f.Add("abc-")                   // trailing dash rejected
	f.Add(strings.Repeat("a", 39))  // within length
	f.Add(strings.Repeat("a", 40))  // length 40 rejected (> 40)
	f.Add(strings.Repeat("a", 100)) // way over rejected
	f.Add("a\nb")                   // newline rejected
	f.Add("a\x00b")                 // NUL rejected
	f.Add("../etc/passwd")          // path-traversal rejected
	f.Add("café")                   // unicode rejected
	f.Add("\t\rf")                  // tabs rejected
	f.Add("a.b")                    // dot rejected
	f.Add("ABC123")                 // all caps rejected

	f.Fuzz(func(t *testing.T, s string) {
		defer recoverPanic(t)
		ok := validSlug(s)
		// Independent re-check: if validSlug accepted it, the input must
		// satisfy the slug regex. If it didn't, that's the test passing.
		if ok && !slugShape.MatchString(s) {
			t.Errorf("validSlug(%q) = true, but slug regex disagrees", s)
		}
		// For any accepted slug, no control chars or whitespace must be
		// present — this is the property spec §11 cares about (the slug
		// becomes part of hostname / URL path; control chars would be a
		// log-injection or URL-parse hazard).
		if ok {
			for _, r := range s {
				if r < 0x20 || r == 0x7f {
					t.Errorf("validSlug(%q) accepted a control char (r=%#x)", s, r)
				}
			}
		}
	})
}

// digestShape matches the substring parseImageDigest returns on accept:
// "@sha256:" + 64 lowercase hex. The leading "@" comes from
// ref[strings.Index(ref, "@"):] in handlers.go — the parser returns the
// first-@ to end-of-string, callers strip the "@" via
// strings.TrimPrefix(digest, "@") before logging the OCI ref (see the
// CodeQL go/log-injection note at handlers.go:264).
var digestShape = regexp.MustCompile(`^@sha256:[0-9a-f]{64}$`)

func FuzzParseImageDigest(f *testing.F) {
	f.Add("foo/bar@sha256:" + strings.Repeat("a", 64))
	f.Add("foo/bar@sha256:" + strings.Repeat("A", 64))        // uppercase rejected
	f.Add("foo/bar@sha256:" + strings.Repeat("a", 63))        // too short rejected
	f.Add("foo/bar@sha256:" + strings.Repeat("a", 65))        // too long rejected
	f.Add("foo/bar")                                          // no @ rejected
	f.Add("foo/bar@sha512:" + strings.Repeat("a", 64))        // wrong algo rejected
	f.Add("@sha256:" + strings.Repeat("a", 64))               // empty repo rejected (host charset requires alnum first char)
	f.Add("foo/bar@sha256:" + strings.Repeat("z", 64))        // non-hex char rejected
	f.Add("foo/bar@sha256:" + strings.Repeat("0", 64) + "\n") // trailing newline rejected
	f.Add("foo\nbar@sha256:" + strings.Repeat("a", 64))       // control char in host rejected
	f.Add("foo/bar@bad")
	f.Add("foo/bar@@sha256:" + strings.Repeat("a", 64)) // double @ rejected

	f.Fuzz(func(t *testing.T, ref string) {
		defer recoverPanic(t)
		digest, ok := parseImageDigest(ref)
		// If accepted, the returned substring MUST be exactly "@sha256:"
		// + 64 lowercase hex. parseImageDigest returns "" on reject, so
		// this also pins that rejected calls cannot accidentally return
		// a partial / canonicalized digest.
		if ok {
			if !digestShape.MatchString(digest) {
				t.Errorf("parseImageDigest(%q).ok=true but digest=%q does not match canonical shape", ref, digest)
			}
		} else if digest != "" {
			t.Errorf("parseImageDigest(%q).ok=false but returned non-empty digest %q", ref, digest)
		}
		// Invariant: ok agrees with isDigestPinned (the input-validation
		// surface). If they ever diverge, deploy-input validation is in
		// a half-broken state.
		if got := isDigestPinned(ref); got != ok {
			t.Errorf("parseImageDigest(%q).ok=%v but isDigestPinned=%v", ref, ok, got)
		}
	})
}

func FuzzIsDigestPinned(f *testing.F) {
	// isDigestPinned is the input-validation surface (per handlers.go:284
	// doc) — fuzz it independently so a future refactor that adds a
	// cheaper pre-check (and accepts a wider set than the parser) gets
	// caught.
	f.Add("foo/bar@sha256:" + strings.Repeat("a", 64))
	f.Add("foo/bar")
	f.Add("")
	f.Add("foo bar@sha256:" + strings.Repeat("a", 64))
	f.Add("foo\nbar@sha256:" + strings.Repeat("a", 64))

	f.Fuzz(func(t *testing.T, ref string) {
		defer recoverPanic(t)
		// isDigestPinned must agree with parseImageDigest.ok — the parser
		// is the truth source (it's what the deploy-input handler calls).
		_, ok := parseImageDigest(ref)
		if got := isDigestPinned(ref); got != ok {
			t.Errorf("isDigestPinned(%q)=%v but parseImageDigest.ok=%v", ref, got, ok)
		}
	})
}
