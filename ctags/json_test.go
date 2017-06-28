// Copyright 2017 Google Inc. All rights reserved.
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

package ctags

import (
	"reflect"
	"testing"
)

func TestJSON(t *testing.T) {
	p, err := newProcess("universal-ctags")
	if err != nil {
		t.Fatal("newProcess", err)
	}

	defer p.Close()

	java := `
package io.zoekt;
import java.util.concurrent.Future;
class Back implements Future extends Frob {
  public static int BLA = 1;
  public int member;
  public Back() {
    member = 2;
  }
  public int method() {
    member++;
  }
}
`
	name := "io/zoekt/Back.java"
	got, err := p.Parse(name, []byte(java))
	if err != nil {
		t.Errorf("Process: %v", err)
	}

	want := []*Entry{
		{
			Sym:  "io.zoekt",
			Kind: "package",
			Path: "io/zoekt/Back.java",
			Line: 2,
		},
		{
			Sym:  "Back",
			Path: "io/zoekt/Back.java",
			Line: 4,
			Kind: "class",
		},

		{
			Sym:  "BLA",
			Path: "io/zoekt/Back.java",
			Line: 5,
			Kind: "field",
		},
		{
			Sym:  "member",
			Path: "io/zoekt/Back.java",
			Line: 6,
			Kind: "field",
		},
		{
			Sym:  "Back",
			Path: "io/zoekt/Back.java",
			Line: 7,
			Kind: "method",
		},
		{
			Sym:  "method",
			Path: "io/zoekt/Back.java",
			Line: 10, Kind: "method",
		},
	}

	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("got %#v, want %#v", got[i], want[i])
		}
	}
}
