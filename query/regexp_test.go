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
	"reflect"
	"regexp/syntax"
	"strings"
	"testing"
)

var opnames = map[syntax.Op]string{
	syntax.OpNoMatch:        "OpNoMatch",
	syntax.OpEmptyMatch:     "OpEmptyMatch",
	syntax.OpLiteral:        "OpLiteral",
	syntax.OpCharClass:      "OpCharClass",
	syntax.OpAnyCharNotNL:   "OpAnyCharNotNL",
	syntax.OpAnyChar:        "OpAnyChar",
	syntax.OpBeginLine:      "OpBeginLine",
	syntax.OpEndLine:        "OpEndLine",
	syntax.OpBeginText:      "OpBeginText",
	syntax.OpEndText:        "OpEndText",
	syntax.OpWordBoundary:   "OpWordBoundary",
	syntax.OpNoWordBoundary: "OpNoWordBoundary",
	syntax.OpCapture:        "OpCapture",
	syntax.OpStar:           "OpStar",
	syntax.OpPlus:           "OpPlus",
	syntax.OpQuest:          "OpQuest",
	syntax.OpRepeat:         "OpRepeat",
	syntax.OpConcat:         "OpConcat",
	syntax.OpAlternate:      "OpAlternate",
}

func printRegexp(r *syntax.Regexp, lvl int) {
	log.Printf("%s%s ch: %d", strings.Repeat(" ", lvl), opnames[r.Op], len(r.Sub))
	for _, s := range r.Sub {
		printRegexp(s, lvl+1)
	}
}

func TestRegexpParse(t *testing.T) {
	type testcase struct {
		in   string
		want Q
	}

	cases := []testcase{
		{"(foo|)bar", &Substring{Pattern: "bar"}},
		{"(foo|)", &Const{true}},
		{"(foo|bar)baz.*bla", &And{[]Q{
			&Or{[]Q{
				&Substring{Pattern: "foo"},
				&Substring{Pattern: "bar"},
			}},
			&Substring{Pattern: "baz"},
			&Substring{Pattern: "bla"},
		}}},
		{"^[a-z](People)+barrabas$",
			&And{[]Q{
				&Substring{Pattern: "People"},
				&Substring{Pattern: "barrabas"},
			}}},
	}

	for _, c := range cases {
		r, err := syntax.Parse(c.in, syntax.Perl)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}

		got := RegexpToQuery(r, 3)
		if !reflect.DeepEqual(c.want, got) {
			t.Errorf("regexpToQuery(%q): got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestLowerRegexp(t *testing.T) {
	// The expected values are a bit volatile, since they come from
	// syntex.Regexp.String. So they may change between go versions. Just
	// ensure they make sense.
	cases := map[string]string{
		"[a-zA-Z]fooBAR": "[A-Za-z]foobar",

		"foo":       "foo",
		"FoO":       "foo",
		"(?m:^foo)": "(?m:^)foo", // regex parse simplifies to this
		"(?m:^FoO)": "(?m:^)foo",

		// Ranges for the characters can be tricky. So we include many cases
		"[A-Z]":  "[a-z]",
		"[^A-Z]": "[^A-Za-z]",
		"[A-M]":  "[a-m]",
		"[^A-M]": "[^A-Ma-m]",
		"[A]":    "a",
		"[^A]":   "[^Aa]",
		"[M]":    "m",
		"[^M]":   "[^Mm]",
		"[Z]":    "z",
		"[^Z]":   "[^Zz]",
		"[a-z]":  "[a-z]",
		"[^a-z]": "[^a-z]",
		"[a-m]":  "[a-m]",
		"[^a-m]": "[^a-m]",
		"[a]":    "a",
		"[^a]":   "[^a]",
		"[m]":    "m",
		"[^m]":   "[^m]",
		"[z]":    "z",
		"[^z]":   "[^z]",

		// @ is tricky since it is 1 value less than A
		"[^A-Z@]": "[^@-Za-z]",

		// full unicode range should just be a .
		"[\\x00-\\x{10ffff}]": "(?s:.)",

		"[abB-Z]":       "[b-za-b]",
		"([abB-Z]|FoO)": "([b-za-b]|foo)",
		`[@-\[]`:        `[@-\[a-z]`,      // original range includes A-Z but excludes a-z
		`\S`:            `[^\t-\n\f-\r ]`, // \S is shorthand for the expected
	}

	for expr, want := range cases {
		re := mustParseRE(expr)
		in := re.String()
		got := LowerRegexp(re)
		if got.String() != want {
			t.Errorf("LowerRegexp(%q) == %q != %q", expr, got.String(), want)
		}

		if re.String() != in {
			t.Errorf("got mutated original %s want %s", re.String(), in)
		}
	}
}
