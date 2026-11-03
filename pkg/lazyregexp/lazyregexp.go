// Package lazyregexp compiles a package-level regular expression the first
// time it is used, not while its package initializes.
//
// pkg/api declared 91 package-level patterns with regexp.MustCompile.
// Compiling them at init cost every process that imports pkg/api about 4 MB
// of allocations and 7 ms, including the stream bridge vmmd runs for each
// live instance (ADR-20261101215228 measurements). Most processes use few of
// those patterns, or none.
//
// Under `go test` a pattern compiles when it is declared, as
// regexp.MustCompile would, so a bad pattern still fails the tests of every
// package that imports it.
package lazyregexp

import (
	"os"
	"regexp"
	"strings"
	"sync"
)

// Regexp is a regular expression compiled on first use. It is safe for
// concurrent use, like *regexp.Regexp.
type Regexp struct {
	str  string
	once sync.Once
	rx   *regexp.Regexp
}

// New returns a Regexp for str. Compiling panics on an invalid pattern, as
// regexp.MustCompile does; outside tests that happens on first use.
func New(str string) *Regexp {
	r := &Regexp{str: str}
	if inTest {
		r.Regexp()
	}
	return r
}

// Regexp returns the compiled expression.
func (r *Regexp) Regexp() *regexp.Regexp {
	r.once.Do(func() { r.rx = regexp.MustCompile(r.str) })
	return r.rx
}

func (r *Regexp) MatchString(s string) bool { return r.Regexp().MatchString(s) }

func (r *Regexp) String() string { return r.str }

func (r *Regexp) FindString(s string) string { return r.Regexp().FindString(s) }

func (r *Regexp) FindStringIndex(s string) []int { return r.Regexp().FindStringIndex(s) }

func (r *Regexp) FindStringSubmatch(s string) []string { return r.Regexp().FindStringSubmatch(s) }

func (r *Regexp) FindAllString(s string, n int) []string { return r.Regexp().FindAllString(s, n) }

func (r *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	return r.Regexp().FindAllStringSubmatch(s, n)
}

func (r *Regexp) ReplaceAllString(src, repl string) string {
	return r.Regexp().ReplaceAllString(src, repl)
}

func (r *Regexp) ReplaceAllStringFunc(src string, repl func(string) string) string {
	return r.Regexp().ReplaceAllStringFunc(src, repl)
}

func (r *Regexp) SubexpIndex(name string) int { return r.Regexp().SubexpIndex(name) }

// inTest reports whether this process is a test binary, which go test names
// <package>.test.
var inTest = len(os.Args) > 0 && strings.HasSuffix(strings.TrimSuffix(os.Args[0], ".exe"), ".test")
