// Package pyre provides regular-expression building blocks that reproduce the
// Unicode semantics of the original Python implementation (\w, \s) on top of
// Go's RE2 engine, so catalog analysis and SQL classification stay identical.
package pyre

import "regexp"

// Word is the body of a character class matching Python's Unicode \w.
const Word = `\p{L}\p{N}_`

// Space is the body of a character class matching Python's Unicode \s.
const Space = `\t\n\x0B\f\r\x1C-\x1F \x85\p{Z}`

// W matches one Python \w character.
const W = `[` + Word + `]`

// S matches one Python \s character.
const S = `[` + Space + `]`

// NS matches one Python \S character.
const NS = `[^` + Space + `]`

// MustCompile compiles a pattern and panics on error (patterns are constants).
func MustCompile(pattern string) *regexp.Regexp {
	return regexp.MustCompile(pattern)
}
