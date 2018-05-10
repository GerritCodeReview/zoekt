// Copyright 2016 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package query

import (
	"log"
	"regexp/syntax"
	"unicode/utf8"
)

var _ = log.Println

// LowerRegexp lowers rune literals and adjusts character classes to include
// lowercase versions of [A-Z] and [^A-Z].
//
// Note: We can't just use strings.ToLower since it will change the meaning of
// regex shorthands like \S or \B.
func LowerRegexp(r *syntax.Regexp) *syntax.Regexp {
	newRE := *r
	switch r.Op {
	case syntax.OpLiteral:
		// For literal strings we can simply lower each character.
		newRE.Rune = make([]rune, len(r.Rune))
		for i, c := range r.Rune {
			if c >= 'A' && c <= 'Z' {
				newRE.Rune[i] = c + 'a' - 'A'
			} else {
				newRE.Rune[i] = c
			}
		}
	case syntax.OpCharClass:
		// An exclusion class is something like [^A-Z]. We need to specially
		// handle it since the user intention of [^A-Z] should map to
		// [^a-z]. If we use the normal mapping logic, we will do nothing
		// since [a-z] is in [^A-Z]. We assume we have an exclusion class if
		// our inclusive range starts at 0 and ends at the end of the unicode
		// range. Note this means we don't support unusual ranges like
		// [^\x00-B] or [^B-\x{10ffff}].
		isExclusion := len(r.Rune) >= 4 && r.Rune[0] == 0 && r.Rune[len(r.Rune)-1] == utf8.MaxRune
		if isExclusion {
			newRE.Rune = lowerCharClassExcludedAZ(r.Rune)
		} else {
			newRE.Rune = lowerCharClassAZ(r.Rune)
		}
	default:
		newRE.Sub = make([]*syntax.Regexp, len(newRE.Sub))
		for i, s := range r.Sub {
			newRE.Sub[i] = LowerRegexp(s)
		}
	}

	return &newRE
}

// lowerCharClassExcludedAZ returns the character class defined by r, but with
// lowered counterparts in [A-Z] excluded in r, excluded in the result.
//
// eg [^B-H] will return [^B-Hb-h]
func lowerCharClassExcludedAZ(r []rune) []rune {
	// Algorithm:
	// Assume r is sorted (it is!)
	// 1. Build a list of inclusive ranges in a-z that are excluded in A-Z (excluded)
	// 2. Copy across classes, ensuring all ranges are outside of ranges in excluded.
	//
	// In our comments we use the mathematical notation [x, y] and (a,
	// b). [ and ] are range inclusive, ( and ) are range
	// exclusive. So x is in [x, y], but not in (x, y).

	// excluded is a list of _exclusive_ ranges in ['a', 'z'] that need
	// to be removed.
	excluded := []rune{}

	// Note i starts at 1, so we are inspecting the gaps between ranges. So
	// [r[0], r[1]] and [r[2], r[3]] implies we have an excluded range of
	// (r[1], r[2]).
	for i := 1; i < len(r)-1; i += 2 {
		// (a, b) is a range that is excluded
		a, b := r[i], r[i+1]
		// This range doesn't exclude [A-Z], so skip (does not
		// intersect with ['A', 'Z']).
		if a > 'Z' || b < 'A' {
			continue
		}
		// We know (a, b) intersects with ['A', 'Z']. So clamp such
		// that we have the intersection (a, b) ^ [A, Z]
		if a < 'A' {
			a = 'A' - 1
		}
		if b > 'Z' {
			b = 'Z' + 1
		}
		// (a, b) is now a range contained in ['A', 'Z'] that needs to
		// be excluded. So we map it to the lower case version and add
		// it to the excluded list.
		excluded = append(excluded, a+'a'-'A', b+'b'-'B')
	}

	// Copy from re.Rune to newRE.Rune, but exclude excluded.
	dst := make([]rune, 0, len(r))
	for i := 0; i < len(r); i += 2 {
		// [a, b] is a range that is included
		a, b := r[i], r[i+1]

		// Remove exclusions ranges that occur before a. They would of
		// been previously processed.
		for len(excluded) > 0 && a >= excluded[1] {
			excluded = excluded[2:]
		}

		// If our exclusion range happens after b, that means we
		// should only consider it later.
		if len(excluded) == 0 || b <= excluded[0] {
			dst = append(dst, a, b)
			continue
		}

		// We now know that the current exclusion range intersects
		// with [a, b]. Break it into two parts, the range before a
		// and the range after b.
		if a <= excluded[0] {
			dst = append(dst, a, excluded[0])
		}
		if b >= excluded[1] {
			dst = append(dst, excluded[1], b)
		}
	}

	return dst
}

// lowerCharClassAZ returns the character class defined by r, but with
// lowered counterparts of [A-Z] in r also in the result range.
//
// eg [B-H] will return [b-h]
func lowerCharClassAZ(r []rune) []rune {
	l := len(r)
	tmp := make([]rune, len(r))
	copy(tmp, r)
	r = tmp

	// First check if we already include [a-z]. If so we have can use the character class as is.
	for i := 0; i < l; i += 2 {
		if r[i] <= 'a' && r[i+1] >= 'z' {
			return r
		}
	}
	for i := 0; i < l; i += 2 {
		a, b := r[i], r[i+1]
		// This range doesn't include A-Z, so skip
		if a > 'Z' || b < 'A' {
			continue
		}
		simple := true
		if a < 'A' {
			simple = false
			a = 'A'
		}
		if b > 'Z' {
			simple = false
			b = 'Z'
		}
		a, b = a+'a'-'A', b+'a'-'A'
		if simple {
			// The char range is within A-Z, so we can
			// just modify it to be the equivalent in a-z.
			r[i], r[i+1] = a, b
		} else {
			// The char range includes characters outside
			// of A-Z. To be safe we just append a new
			// lowered range which is the intersection
			// with A-Z.
			r = append(r, a, b)
		}
	}

	return r
}

// RegexpToQuery tries to distill a substring search query that
// matches a superset of the regexp.
func RegexpToQuery(r *syntax.Regexp, minTextSize int) Q {
	q := regexpToQueryRecursive(r, minTextSize)
	q = Simplify(q)
	return q
}

func regexpToQueryRecursive(r *syntax.Regexp, minTextSize int) Q {
	// TODO - we could perhaps transform Begin/EndText in '\n'?
	// TODO - we could perhaps transform CharClass in (OrQuery )
	// if there are just a few runes, and part of a OpConcat?
	switch r.Op {
	case syntax.OpLiteral:
		s := string(r.Rune)
		if len(s) >= minTextSize {
			return &Substring{Pattern: s}
		}
	case syntax.OpCapture:
		return regexpToQueryRecursive(r.Sub[0], minTextSize)

	case syntax.OpPlus:
		return regexpToQueryRecursive(r.Sub[0], minTextSize)

	case syntax.OpRepeat:
		if r.Min >= 1 {
			return regexpToQueryRecursive(r.Sub[0], minTextSize)
		}

	case syntax.OpConcat, syntax.OpAlternate:
		var qs []Q
		for _, sr := range r.Sub {
			if sq := regexpToQueryRecursive(sr, minTextSize); sq != nil {
				qs = append(qs, sq)
			}
		}
		if r.Op == syntax.OpConcat {
			return &And{qs}
		}
		return &Or{qs}
	}
	return &Const{true}
}
