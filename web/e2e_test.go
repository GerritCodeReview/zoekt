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

package web

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"golang.org/x/net/context"
)

// TODO(hanwen): cut & paste from ../ . Should create internal test
// util package.
type memSeeker struct {
	data []byte
}

func (s *memSeeker) Close() {}
func (s *memSeeker) Read(off, sz uint32) ([]byte, error) {
	return s.data[off : off+sz], nil
}

func (s *memSeeker) Size() (uint32, error) {
	return uint32(len(s.data)), nil
}
func (s *memSeeker) Name() string {
	return "memSeeker"
}

func searcherForTest(t *testing.T, b *zoekt.IndexBuilder) zoekt.Searcher {
	var buf bytes.Buffer
	b.Write(&buf)
	f := &memSeeker{buf.Bytes()}

	searcher, err := zoekt.NewSearcher(f)
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}

	return searcher
}

func TestBasic(t *testing.T) {
	b := zoekt.NewIndexBuilder()
	b.SetName("name")
	b.SetRepoURL("url", "fragment")
	b.AddFile("f2", []byte("to carry water in the no later bla"))
	// -------------------- 0123456789012345678901234567890123456789

	s := searcherForTest(t, b)

	srv := Server{
		Searcher: s,
		Top:      Top,
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/search?q=water")
	if err != nil {
		t.Fatal(err)
	}
	resultBytes, err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	result := string(resultBytes)
	for _, want := range []string{
		"href=\"url",
		"#fragment\"",
		"carry <b>water</b>",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("result did not have %q: %s", want, result)
		}
	}
	for _, notWant := range []string{
		"crashed",
	} {
		if strings.Contains(result, notWant) {
			t.Errorf("result has %q: %s", notWant, result)
		}
	}
}

type crashSearcher struct {
	zoekt.Searcher
}

func (s *crashSearcher) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	res := zoekt.SearchResult{}
	res.Stats.Crashes = 1
	return &res, nil
}

func TestCrash(t *testing.T) {
	srv := Server{
		Searcher: &crashSearcher{},
		Top:      Top,
	}

	mux, err := NewMux(&srv)
	if err != nil {
		t.Fatalf("NewMux: %v", err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/search?q=water")
	if err != nil {
		t.Fatal(err)
	}
	resultBytes, err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	result := string(resultBytes)
	for _, want := range []string{
		"1 shards crashed",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("result did not have %q: %s", want, result)
		}
	}
}
