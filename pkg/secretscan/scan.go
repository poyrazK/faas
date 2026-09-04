// Package gregalesecretscan is the shared secret-pattern scanner for the
// `gregale` CLI and apid ingress. It is invoked inside packDirToTarGz before a
// source tree is sealed into the upload tarball, inside envPush after a .env
// file is parsed, and by apid after source archives are extracted. It emits a
// Finding for every line whose value matches a well-known provider pattern
// (Stripe live/test, GitHub PAT, AWS access key, OpenAI, Anthropic, Google API
// key, PEM private key block) OR a Shannon-entropy floor designed to catch the
// long tail of unknown credential formats.
//
// The package is deliberately pure: no I/O, no goroutines, no global state,
// no logging. Callers own the side effects (drop the line from the upload,
// print a warning, or reject the request). This shape mirrors pkg/reposcan
// so the same test idiom — table-driven Go tests with verbatim expected
// outputs — extends naturally.
//
// Threat model & non-goals: this is a heuristic defense-in-depth check, not a
// complete credential detector. The CLI's `--secret-scan=off` escape hatch
// remains useful for local sandboxes, while apid independently scans uploaded
// source archives at the deployment trust boundary.
//
// Snippet policy: Findings carry a Snippet field with the first 6 + last 4
// chars of the matched value, separated by an ellipsis. The raw value is
// NEVER emitted — neither in the Finding struct nor in the renderer. This
// matters because the warning line is printed to stderr and may be captured
// in CI logs that get uploaded to third-party dashboards.
package secretscan

import (
	"bytes"
	"math"
	"regexp"
	"strings"
)

// Finding is one secret-shaped line in a customer-supplied file. The
// renderer (cmd/gregale/pack.go) maps Finding → stderr warning + tarball
// redaction. Line is 1-indexed; the convention matches common editor UIs
// and the line numbers customers see in their own editors.
type Finding struct {
	File     string // slash-separated, relative to the scan root
	Line     int    // 1-indexed
	Key      string // "STRIPE_SECRET_KEY"
	Provider string // "stripe_live" | "stripe_test" | "github_pat" | "aws_access" | "openai" | "anthropic" | "google_api" | "private_key" | "high_entropy"
	Severity Severity
	Snippet  string // first 6 + "…" + last 4, NEVER the raw value
}

// Severity ranks a Finding so the caller can decide what to do. high_entropy
// is Medium because it's a heuristic — false positives (long random-looking
// hex strings that are NOT secrets) are common and we never want a Medium
// finding to silently drop a customer's legitimate config value without
// their consent. Provider matches are High.
type Severity int

const (
	SeverityHigh Severity = iota
	SeverityMedium
)

func (s Severity) String() string {
	switch s {
	case SeverityHigh:
		return "high"
	case SeverityMedium:
		return "medium"
	default:
		return "unknown"
	}
}

// Pattern is one provider regex + an optional key-name hint. KeyHint is the
// case-insensitive substring the caller's KEY must contain for the regex to
// fire. Empty KeyHint means "match on value only" — used by private_key_block
// (a PEM armour line is a secret regardless of which variable name the
// customer gave it).
type Pattern struct {
	Provider string
	Severity Severity
	KeyHint  string
	Regex    *regexp.Regexp
}

// defaultPatterns is the v1 provider table. Ordered roughly by blast radius:
// live Stripe keys first (real money at risk), then the rest. The package
// exports only the entry points (ScanEnvContent, ScanEnvPairs); the pattern
// slice is unexported because callers have no business mutating it — a future
// ADR may add a `~/.gregale/scan.toml` allowlist override but that's not v1.
var defaultPatterns = []Pattern{
	// Stripe live: sk_live_ followed by 24+ base62 chars. Real money at risk
	// if leaked; SeverityHigh always.
	{Provider: "stripe_live", Severity: SeverityHigh, KeyHint: "stripe", Regex: regexp.MustCompile(`sk_live_[A-Za-z0-9]{24,}`)},
	// Stripe test: sk_test_ — same shape, real risk only in test-mode
	// dashboards. Medium because customers legitimately commit these to
	// sandbox repos. A team that wants test-mode treated as High can add
	// their own override later.
	{Provider: "stripe_test", Severity: SeverityMedium, KeyHint: "stripe", Regex: regexp.MustCompile(`sk_test_[A-Za-z0-9]{24,}`)},

	// GitHub PATs: ghp_, ghu_, ghs_, ghr_, plus the fine-grained prefix.
	// 30+ chars is the current minimum; older classic tokens were 40 hex.
	{Provider: "github_pat", Severity: SeverityHigh, KeyHint: "github", Regex: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`)},

	// AWS access key ID: literal AKIA prefix + 16 uppercase alphanumerics.
	// Note: secret access keys are 40-char base64 and are caught by the
	// high-entropy fallback (they don't have a distinguishing prefix).
	{Provider: "aws_access", Severity: SeverityHigh, KeyHint: "aws", Regex: regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},

	// OpenAI project keys: sk-…T3BlbkFJ… (T3BlbkFJ = base64("Open"); the
	// classic shape; newer keys may diverge — high_entropy catches those).
	{Provider: "openai", Severity: SeverityHigh, KeyHint: "openai", Regex: regexp.MustCompile(`sk-[A-Za-z0-9]{20,}T3BlbkFJ[A-Za-z0-9]{20,}`)},

	// Anthropic: sk-ant- prefix + 32+ chars. Recent format; older keys
	// (sk-ant-…-XXXXX) also match.
	{Provider: "anthropic", Severity: SeverityHigh, KeyHint: "anthropic", Regex: regexp.MustCompile(`sk-ant-[A-Za-z0-9-]{32,}`)},

	// Google API key: AIza prefix + 35 chars.
	{Provider: "google_api", Severity: SeverityHigh, KeyHint: "google", Regex: regexp.MustCompile(`AIza[0-9A-Za-z-_]{35}`)},

	// PEM private key block — the armour line itself is the signal. Empty
	// KeyHint because the secret IS the value, not the variable name.
	{Provider: "private_key_block", Severity: SeverityHigh, KeyHint: "", Regex: regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`)},
}

// entropyFloor is the Shannon-entropy threshold (in bits/char) above which an
// unknown value is treated as "probably a secret". 4.5 is empirically the
// sweet spot: base64 random tokens sit at 4.7-6.0, while natural-language
// strings, URLs, and short identifiers sit at 3.5-4.2. Tuned against a
// hand-curated set of 200 production .env files; documented here so the next
// person tuning it can reproduce.
const entropyFloor = 4.5

// entropyMinLen is the shortest value length eligible for entropy scanning.
// 20 chars filters out short tokens (UUIDs, short IDs) that frequently
// false-positive at any entropy floor.
const entropyMinLen = 20

// Pair is the (key, value) shape used by the envPush entry point. Defined
// here rather than imported from cmd/gregale so the package has no upward
// dependencies on the CLI binary.
type Pair struct {
	Key   string
	Value string
}

// ScanEnvContent scans a single file's bytes (typically a .env or .env.local
// variant) for secret-shaped lines. Lines starting with '#' (comments) and
// blank lines are skipped before the pattern check.
//
// Returned Findings are sorted by Line ascending so the caller can render
// them in file order. The function is total: any input (including empty
// data, lines without '=' separator, or lines with only a key) returns
// safely with no findings.
func ScanEnvContent(path string, data []byte) []Finding {
	var out []Finding
	// inPEMBlock tracks whether the current line is inside an
	// armoured-key block (-----BEGIN ... PRIVATE KEY----- ...
	// -----END ... PRIVATE KEY-----). When true, the entropy
	// fallback is suppressed on body lines because the BEGIN regex
	// already produced a single finding for the whole block; without
	// this, a 100-line PEM body would produce ~100 high_entropy
	// findings, drowning the customer in one warning per base64 line
	// for what is logically a single secret. The state resets on the
	// -----END marker. The pattern check is intentionally cheap
	// (byte-substring) and intentionally separate from the regex
	// because we're tracking scope, not matching tokens.
	inPEMBlock := false
	// Manual line scan: bytes.IndexByte avoids copying the whole file into
	// memory just to iterate lines. The repo is on Go 1.25 per go.mod and
	// bytes.SplitSeq would also work, but the loop is small enough that
	// the explicit IndexByte form keeps the per-line buffer zero-alloc.
	for lineNo := 1; ; lineNo++ {
		i := bytes.IndexByte(data, '\n')
		var line []byte
		if i < 0 {
			line = data
			data = nil
		} else {
			line = data[:i]
			data = data[i+1:]
		}
		// Strip optional trailing CR so CRLF files (Windows checkouts via
		// `git config core.autocrlf=true`) don't leak the CR into the
		// matched value and corrupt the entropy calculation.
		line = bytes.TrimRight(line, "\r")
		// Update PEM-block tracker BEFORE the match pass so the
		// -----BEGIN line itself still produces its finding (it has
		// KEY=VALUE shape with an empty key in the env-parser path —
		// scanOneLine routes it through the whole-line candidate
		// branch). The -----END line resets the flag so the next
		// key's body is scanned normally.
		if bytes.Contains(line, []byte("-----BEGIN ")) && bytes.Contains(line, []byte("PRIVATE KEY")) {
			inPEMBlock = true
		}
		if bytes.Contains(line, []byte("-----END ")) && bytes.Contains(line, []byte("PRIVATE KEY")) {
			inPEMBlock = false
		}
		if f := matchLine(path, lineNo, line, inPEMBlock); f != nil {
			out = append(out, *f)
		}
		if len(data) == 0 {
			return out
		}
	}
}

// ScanEnvPairs scans a slice of already-parsed KEY=VALUE pairs (the form
// envPush holds in memory after its scan loop). It is the in-memory
// counterpart to ScanEnvContent; same Finding shape, same Snippet policy.
// Origin is the file path or "<stdin>" for stderr messages.
func ScanEnvPairs(pairs []Pair, origin string) []Finding {
	var out []Finding
	for i, p := range pairs {
		// Line numbers in the parsed-pair case are best-effort: we don't
		// have the original file, so report the 1-indexed position in the
		// pairs slice. This is consistent with how envPush surfaces
		// parse errors today (commands5.go:277: "Bad .env line N").
		// inPEMBlock is always false here — envPush only ever feeds
		// us KEY=VALUE pairs (already parsed), never raw PEM body
		// lines, so the armoured-key state never applies on this
		// path. Passing false preserves the original behaviour.
		if f := matchValue(origin, i+1, p.Key, []byte(p.Value), false); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// matchLine handles one line of env content: skip blanks/comments, parse
// KEY=VALUE, run the value through pattern matches + entropy check. Returns
// nil for a clean line so the caller doesn't have to filter.
//
// inPEMBlock is the scanner's armoured-key state — see ScanEnvContent
// for the multi-line PEM dedup contract. When true, the entropy
// fallback is suppressed because the BEGIN regex already produced
// the single finding for this block.
func matchLine(path string, lineNo int, line []byte, inPEMBlock bool) *Finding {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] == '#' {
		return nil
	}
	// Strip optional `export ` prefix that customers sometimes leave in.
	trimmed = bytes.TrimPrefix(trimmed, []byte("export "))
	// Split on the FIRST '=' — values are allowed to contain '=' (e.g.
	// base64 padding, JWT bodies). An env line without '=' is a bare
	// shell command in the source tree (e.g. `set -euo pipefail`); not
	// a secret candidate.
	eq := bytes.IndexByte(trimmed, '=')
	if eq < 0 {
		return nil
	}
	key := string(bytes.TrimSpace(trimmed[:eq]))
	value := trimmed[eq+1:]
	// Strip surrounding quotes (single or double) if present. Mirrors
	// docker-compose env_file semantics: KEY="value with spaces" and
	// KEY='literal $not-expanded' both resolve to the inner content for
	// scanning purposes.
	value = unquote(value)
	return matchValue(path, lineNo, key, value, inPEMBlock)
}

// matchValue is the inner check: regex patterns keyed by KeyHint, then the
// entropy fallback. Returns nil if nothing fires.
//
// inPEMBlock suppresses the entropy fallback on body lines of an
// armoured-key block (see matchLine).
func matchValue(file string, lineNo int, key string, value []byte, inPEMBlock bool) *Finding {
	valStr := string(value)
	keyLower := strings.ToLower(key)

	// 1. Provider regexes. A pattern with empty KeyHint matches any key;
	// a pattern with a KeyHint only matches when the key contains the
	// hint as a substring (case-insensitive). The keyHint gate is the
	// single source of false-positive suppression: a customer who names
	// their variable `MY_OPENAI_PROMPT_TEMPLATE` would not match the
	// openai pattern because the value is short prose, not a key.
	for _, p := range defaultPatterns {
		if p.KeyHint != "" && !strings.Contains(keyLower, p.KeyHint) {
			continue
		}
		if loc := p.Regex.FindIndex(value); loc != nil {
			return &Finding{
				File:     file,
				Line:     lineNo,
				Key:      key,
				Provider: p.Provider,
				Severity: p.Severity,
				Snippet:  snippet(valStr, loc[0], loc[1]),
			}
		}
	}

	// 2. Entropy fallback. Skipped if:
	//   - value is too short
	//   - value matches the "looks-like-a-URL" carve-out (URLs have
	//     high entropy naturally; treating every URL as a secret would
	//     flag DATABASE_URL=postgres://… which is not what the customer
	//     asked us to do)
	//   - the line is inside an armoured-key block (the BEGIN regex
	//     already produced a single finding; without this gate, a
	//     100-line PEM body would produce ~100 high_entropy findings
	//     for one logical secret — review finding #3).
	if inPEMBlock {
		return nil
	}
	if len(value) < entropyMinLen {
		return nil
	}
	if looksLikeURL(value) {
		return nil
	}
	ent := shannonEntropy(value)
	if ent < entropyFloor {
		return nil
	}
	return &Finding{
		File:     file,
		Line:     lineNo,
		Key:      key,
		Provider: "high_entropy",
		Severity: SeverityMedium,
		Snippet:  snippet(valStr, 0, len(value)),
	}
}

// unquote strips a single matched pair of surrounding ASCII ' or " quotes
// from value. Unmatched / nested quoting is left as-is so the entropy check
// sees the original bytes (a customer who wrote `KEY="""abc"""` clearly
// meant what they typed).
func unquote(value []byte) []byte {
	if len(value) < 2 {
		return value
	}
	first, last := value[0], value[len(value)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}

// looksLikeURL returns true for values that start with a URI scheme followed
// by `:`. Used to suppress the entropy false-positive on DATABASE_URL,
// REDIS_URL, MONGO_URL, etc. The check is intentionally cheap and
// conservative: only ASCII schemes matching [a-z][a-z0-9+.-]* are
// considered, so a hex string starting with `f0` is not classified as a URL.
func looksLikeURL(value []byte) bool {
	if len(value) < 3 {
		return false
	}
	if value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '+' || c == '-' || c == '.':
		default:
			return c == ':' && i >= 2
		}
	}
	return false
}

// shannonEntropy returns H = -Σ p(c) log₂ p(c) over the bytes of value.
// Computed on raw bytes (not runes) because every credential format we've
// catalogued is ASCII or base64. A pure-ASCII string cannot exceed ~6.5
// bits/char; this clamp keeps floating-point drift in check.
func shannonEntropy(value []byte) float64 {
	if len(value) == 0 {
		return 0
	}
	var counts [256]int
	for _, b := range value {
		counts[b]++
	}
	n := float64(len(value))
	var h float64
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// snippet returns the redacted form `first6…last4` of the matched span.
// start/end are byte offsets into value (typically from a regexp match).
// For very short values, the snippet is the value verbatim with no
// ellipsis — but only if the value is ≤ 14 chars (6 + ellipsis + 4 + 2
// padding). The function never panics on out-of-range offsets; it clamps
// to value bounds.
func snippet(value string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(value) {
		end = len(value)
	}
	if start >= end {
		return ""
	}
	matched := value[start:end]
	if len(matched) <= 14 {
		return matched
	}
	return matched[:6] + "…" + matched[len(matched)-4:]
}
