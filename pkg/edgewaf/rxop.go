package edgewaf

// ADR-831 amendment 10: @grx, the gateway's own @rx. It matches exactly as
// Coraza's @rx does (RE2 with (?sm), up to ten capture fields), and adds two
// things that never change a verdict:
//
//   - a pre-filter: literals any match must contain, each taken whole from
//     one literal node of the pattern, so no literal is ever made up by
//     joining two nodes (the cause of corazawaf/coraza#1790), and a minimum
//     input length;
//   - a bounded cache of results by (app, pattern, value), so a value an
//     app's traffic repeats, such as its User-Agent or Accept headers, is
//     matched once. The app is part of the key, so one app's requests can
//     never be timed to learn what another app's requests contained.
//
// CRS is loaded with @rx renamed to @grx (rxfs.go). The stock operator stays
// registered, so tests compare the two engines on the same rules.

import (
	"hash/maphash"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/corazawaf/coraza/v3/experimental/plugins"
	"github.com/corazawaf/coraza/v3/experimental/plugins/plugintypes"
	"rsc.io/binaryregexp"
)

const (
	// rxOperatorName is the operator CRS's @rx is renamed to.
	rxOperatorName = "grx"
	// rxMaxClauseLiterals bounds one pre-filter clause: an alternation of
	// more literals than this is not worth scanning for.
	rxMaxClauseLiterals = 32
	// rxMaxClauses bounds the clauses a pre-filter checks, most selective
	// first.
	rxMaxClauses = 3
	// rxMemoMaxValue is the longest value the cache keeps: headers, paths
	// and short arguments, not bodies.
	rxMemoMaxValue = 256
	// rxMemoShards and rxMemoShardEntries bound the cache to about 32,000
	// results; a full shard is emptied and refills.
	rxMemoShards       = 64
	rxMemoShardEntries = 512
	// rxScopeSep separates the app from the rest of a transaction ID; a
	// transaction whose ID has no app is never cached.
	rxScopeSep = "|"
)

func init() {
	plugins.RegisterOperator(rxOperatorName, newGregaleRx)
}

// grx is one compiled pattern. Patterns are shared by every rule that uses
// them, so a cache entry serves them all.
type grx struct {
	id     uint32
	re     *regexp.Regexp
	minLen int
	req    rxRequirement
}

var rxPatterns struct {
	mu   sync.Mutex
	byRx map[string]*grx
}

func newGregaleRx(options plugintypes.OperatorOptions) (plugintypes.Operator, error) {
	data := "(?sm)" + options.Arguments
	if matchesNonUTF8(data) {
		// As in Coraza, a pattern matching arbitrary bytes uses the binary
		// matcher; it is not pre-filtered or cached.
		re, err := binaryregexp.Compile(options.Arguments)
		if err != nil {
			return nil, err
		}
		return &binaryGrx{re: re}, nil
	}
	rxPatterns.mu.Lock()
	defer rxPatterns.mu.Unlock()
	if p := rxPatterns.byRx[data]; p != nil {
		return p, nil
	}
	re, err := regexp.Compile(data)
	if err != nil {
		return nil, err
	}
	parsed, err := syntax.Parse(data, syntax.Perl)
	if err != nil {
		return nil, err
	}
	parsed = parsed.Simplify()
	if rxPatterns.byRx == nil {
		rxPatterns.byRx = map[string]*grx{}
	}
	p := &grx{id: uint32(len(rxPatterns.byRx) + 1), re: re, minLen: rxMinLen(parsed), req: rxRequired(parsed).best()}
	rxPatterns.byRx[data] = p
	return p, nil
}

func (p *grx) Evaluate(tx plugintypes.TransactionState, value string) bool {
	if len(value) < p.minLen || !p.req.mayMatch(value) {
		return false
	}
	capturing := tx.Capturing()
	var m []int
	if scope := rxScope(tx.ID()); scope != "" && len(value) <= rxMemoMaxValue {
		matched, idx := rxResults.lookup(scope, p, value, capturing)
		if !matched {
			return false
		}
		m = idx
	} else if !capturing {
		return p.re.MatchString(value)
	} else if m = p.re.FindStringSubmatchIndex(value); m == nil {
		return false
	}
	if capturing {
		// As Coraza's @rx: capture fields 0 to 8.
		for i := 0; i < len(m)/2 && i < 9; i++ {
			if m[2*i] >= 0 {
				tx.CaptureField(i, value[m[2*i]:m[2*i+1]])
			} else {
				tx.CaptureField(i, "")
			}
		}
	}
	return true
}

type binaryGrx struct{ re *binaryregexp.Regexp }

func (o *binaryGrx) Evaluate(tx plugintypes.TransactionState, value string) bool {
	if !tx.Capturing() {
		return o.re.MatchString(value)
	}
	m := o.re.FindStringSubmatch(value)
	if len(m) == 0 {
		return false
	}
	for i, c := range m {
		if i == 9 {
			break
		}
		tx.CaptureField(i, c)
	}
	return true
}

// matchesNonUTF8 reports whether a pattern's \xNN and \x{NN} escapes spell
// bytes that are not valid UTF-8: the case Coraza's @rx hands to its binary
// matcher, decided by the same reading of the escapes.
func matchesNonUTF8(expr string) bool {
	b := make([]byte, 0, len(expr))
	for i := 0; i < len(expr); i++ {
		if expr[i] != '\\' {
			b = append(b, expr[i])
			continue
		}
		if i+3 >= len(expr) {
			b = append(b, expr[i:]...)
			break
		}
		if expr[i+1] != 'x' {
			b = append(b, expr[i])
			continue
		}
		esc, skip := expr[i:], 3
		if len(esc) >= 6 && esc[2] == '{' {
			if end := strings.IndexByte(esc, '}'); end >= 0 {
				esc, skip = `\x`+esc[3:end], end
			}
		}
		v, multibyte, _, err := strconv.UnquoteChar(esc, 0)
		if err != nil || multibyte {
			b = append(b, expr[i])
			continue
		}
		b = append(b, byte(v))
		i += skip
	}
	return !utf8.Valid(b)
}

// rxScope is the app a transaction ID names, or "".
func rxScope(id string) string {
	scope, _, ok := strings.Cut(id, rxScopeSep)
	if !ok {
		return ""
	}
	return scope
}

// rxTransactionID names a transaction for app; seq keeps IDs unique.
func rxTransactionID(appID string, seq uint64) string {
	if appID == "" || strings.Contains(appID, rxScopeSep) {
		return ""
	}
	return appID + rxScopeSep + strconv.FormatUint(seq, 36)
}

// --- pre-filter ----------------------------------------------------------

// rxLiteral is a string a match must contain; fold literals are lower-case
// ASCII and compared without case.
type rxLiteral struct {
	s    string
	fold bool
}

// rxClause holds when the input contains at least one of its literals.
type rxClause []rxLiteral

// rxRequirement holds when every clause holds. Empty means no requirement.
type rxRequirement []rxClause

// rxRequired is what any match of re must contain. Every literal comes
// whole from one OpLiteral node: a node's literal occurs contiguously in any
// match that uses the node, so the requirement can only be weaker than the
// truth, never stronger.
func rxRequired(re *syntax.Regexp) rxRequirement {
	switch re.Op {
	case syntax.OpLiteral:
		s := string(re.Rune)
		fold := re.Flags&syntax.FoldCase != 0
		// U+FFFD matches any invalid byte, and folding outside ASCII is
		// not a plain substring test; neither is pre-filtered.
		if len(s) < 2 || strings.ContainsRune(s, utf8.RuneError) || fold && !isASCII(s) {
			return nil
		}
		if fold {
			s = strings.ToLower(s)
		}
		return rxRequirement{{{s: s, fold: fold}}}
	case syntax.OpCapture, syntax.OpPlus:
		return rxRequired(re.Sub[0])
	case syntax.OpRepeat:
		if re.Min >= 1 {
			return rxRequired(re.Sub[0])
		}
		return nil
	case syntax.OpConcat:
		var all rxRequirement
		for _, sub := range re.Sub {
			all = append(all, rxRequired(sub)...)
		}
		return all
	case syntax.OpAlternate:
		// Some branch matches, so its requirement holds: the union of one
		// clause from each branch holds.
		var union rxClause
		for _, sub := range re.Sub {
			r := rxRequired(sub).best()
			if len(r) == 0 {
				return nil
			}
			union = append(union, r[0]...)
		}
		if len(union) > rxMaxClauseLiterals {
			return nil
		}
		return rxRequirement{union}
	}
	return nil
}

// best keeps the most selective clauses: fewest literals, then longest
// shortest literal.
func (r rxRequirement) best() rxRequirement {
	out := make(rxRequirement, 0, len(r))
	for _, c := range r {
		if len(c) > 0 && len(c) <= rxMaxClauseLiterals {
			out = append(out, c)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].better(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) > rxMaxClauses {
		out = out[:rxMaxClauses]
	}
	return out
}

func (c rxClause) better(o rxClause) bool {
	if len(c) != len(o) {
		return len(c) < len(o)
	}
	return c.shortest() > o.shortest()
}

func (c rxClause) shortest() int {
	n := -1
	for _, l := range c {
		if n < 0 || len(l.s) < n {
			n = len(l.s)
		}
	}
	return n
}

// mayMatch reports whether value meets every clause. With a fold literal
// and non-ASCII input it gives up and says yes: Unicode folding matches
// some ASCII letters with other characters (k with U+212A KELVIN SIGN).
func (r rxRequirement) mayMatch(value string) bool {
	ascii := -1
	for _, c := range r {
		ok := false
		for _, l := range c {
			if !l.fold {
				ok = strings.Contains(value, l.s)
			} else {
				if ascii < 0 {
					ascii = 0
					if isASCII(value) {
						ascii = 1
					}
				}
				ok = ascii == 0 || containsFoldASCII(value, l.s)
			}
			if ok {
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// containsFoldASCII reports whether s contains lit (lower-case ASCII)
// ignoring ASCII case.
func containsFoldASCII(s, lit string) bool {
	n := len(lit)
	for i := 0; i+n <= len(s); i++ {
		j := 0
		for ; j < n; j++ {
			c := s[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != lit[j] {
				break
			}
		}
		if j == n {
			return true
		}
	}
	return false
}

// rxMinLen is the fewest bytes a match of re can span: one per character,
// since a folded or class character may match a shorter encoding and an
// invalid byte matches as one.
func rxMinLen(re *syntax.Regexp) int {
	switch re.Op {
	case syntax.OpLiteral:
		if re.Flags&syntax.FoldCase != 0 {
			return len(re.Rune)
		}
		n := 0
		for _, r := range re.Rune {
			if r == utf8.RuneError {
				n++
			} else {
				n += utf8.RuneLen(r)
			}
		}
		return n
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL, syntax.OpCharClass:
		return 1
	case syntax.OpCapture, syntax.OpPlus:
		return rxMinLen(re.Sub[0])
	case syntax.OpRepeat:
		return re.Min * rxMinLen(re.Sub[0])
	case syntax.OpConcat:
		n := 0
		for _, sub := range re.Sub {
			n += rxMinLen(sub)
		}
		return n
	case syntax.OpAlternate:
		n := -1
		for _, sub := range re.Sub {
			if m := rxMinLen(sub); n < 0 || m < n {
				n = m
			}
		}
		return max(n, 0)
	}
	return 0
}

// --- result cache --------------------------------------------------------

type rxMemoKey struct {
	scope string
	rx    uint32
	value string
}

// rxMemoEntry is a cached result; m is the submatch indexes of a match,
// nil until a capturing rule has asked for them.
type rxMemoEntry struct {
	matched bool
	m       []int
}

type rxMemoShard struct {
	mu sync.Mutex
	m  map[rxMemoKey]rxMemoEntry
}

type rxMemo struct {
	seed   maphash.Seed
	shards [rxMemoShards]rxMemoShard
}

var rxResults = &rxMemo{seed: maphash.MakeSeed()}

// lookup reports whether p matches value, and with capturing the submatch
// indexes, from the cache of scope's results; a miss matches and caches.
func (c *rxMemo) lookup(scope string, p *grx, value string, capturing bool) (bool, []int) {
	var h maphash.Hash
	h.SetSeed(c.seed)
	_, _ = h.WriteString(scope)
	_, _ = h.WriteString(value)
	s := &c.shards[(h.Sum64()^uint64(p.id))%rxMemoShards]
	key := rxMemoKey{scope: scope, rx: p.id, value: value}
	s.mu.Lock()
	e, ok := s.m[key]
	s.mu.Unlock()
	if ok && (!e.matched || !capturing || e.m != nil) {
		return e.matched, e.m
	}
	if capturing {
		e.m = p.re.FindStringSubmatchIndex(value)
		e.matched = e.m != nil
	} else {
		e.matched = p.re.MatchString(value)
	}
	s.mu.Lock()
	if s.m == nil || len(s.m) >= rxMemoShardEntries {
		s.m = make(map[rxMemoKey]rxMemoEntry, rxMemoShardEntries)
	}
	// The key keeps its own copies of the strings it was given.
	key.scope, key.value = strings.Clone(scope), strings.Clone(value)
	s.m[key] = e
	s.mu.Unlock()
	return e.matched, e.m
}
