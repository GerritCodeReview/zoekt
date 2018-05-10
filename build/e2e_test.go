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

package build

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/context"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/shards"
)

func TestBasic(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}

	opts := Options{
		IndexDir: dir,
		ShardMax: 1024,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
		Parallelism: 2,
		SizeMax:     1 << 20,
	}

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	for i := 0; i < 4; i++ {
		s := fmt.Sprintf("%d", i)
		b.AddFile("F"+s, []byte(strings.Repeat(s, 1000)))
	}

	b.Finish()

	fs, _ := filepath.Glob(dir + "/*")
	if len(fs) <= 1 {
		t.Fatalf("want multiple shards, got %v", fs)
	}

	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}
	defer ss.Close()

	q, err := query.Parse("111")
	if err != nil {
		t.Fatalf("Parse(111): %v", err)
	}

	var sOpts zoekt.SearchOptions
	ctx := context.Background()
	result, err := ss.Search(ctx, q, &sOpts)
	if err != nil {
		t.Fatalf("Search(%v): %v", q, err)
	}

	if len(result.Files) != 1 || result.Files[0].FileName != "F1" {
		t.Errorf("got %v, want 1 file.", result.Files)
	}
}

func TestUpdate(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}

	opts := Options{
		IndexDir: dir,
		ShardMax: 1024,
		RepositoryDescription: zoekt.Repository{
			Name:            "repo",
			FileURLTemplate: "url",
		},
		Parallelism: 2,
		SizeMax:     1 << 20,
	}

	if b, err := NewBuilder(opts); err != nil {
		t.Fatalf("NewBuilder: %v", err)
	} else {
		b.AddFile("F", []byte("hoi"))
		b.Finish()
	}
	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}

	ctx := context.Background()
	repos, err := ss.List(ctx, &query.Repo{Pattern: "repo"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(repos.Repos) != 1 {
		t.Errorf("List(repo): got %v, want 1 repo", repos.Repos)
	}

	fs, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	opts.RepositoryDescription = zoekt.Repository{
		Name:            "repo2",
		FileURLTemplate: "url2",
	}

	if b, err := NewBuilder(opts); err != nil {
		t.Fatalf("NewBuilder: %v", err)
	} else {
		b.AddFile("F", []byte("hoi"))
		b.Finish()
	}

	// This is ugly, and potentially flaky, but there is no
	// observable synchronization for the Sharded searcher, so
	// this is the best we can do.
	time.Sleep(100 * time.Millisecond)

	ctx = context.Background()
	if repos, err = ss.List(ctx, &query.Repo{Pattern: "repo"}); err != nil {
		t.Fatalf("List: %v", err)
	} else if len(repos.Repos) != 2 {
		t.Errorf("List(repo): got %v, want 2 repos", repos.Repos)
	}

	for _, fn := range fs {
		log.Printf("removing %s", fn)
		if err := os.Remove(fn); err != nil {
			t.Fatalf("Remove(%s): %v", fn, err)
		}
		break
	}

	time.Sleep(100 * time.Millisecond)

	ctx = context.Background()
	if repos, err = ss.List(ctx, &query.Repo{Pattern: "repo"}); err != nil {
		t.Fatalf("List: %v", err)
	} else if len(repos.Repos) != 1 {
		var ss []string
		for _, r := range repos.Repos {
			ss = append(ss, r.Repository.Name)
		}
		t.Errorf("List(repo): got %v, want 1 repo", ss)
	}
}

func TestDeleteOldShards(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}

	opts := Options{
		IndexDir: dir,
		ShardMax: 1024,
		RepositoryDescription: zoekt.Repository{
			Name:            "repo",
			FileURLTemplate: "url",
		},
		SizeMax: 1 << 20,
	}
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	for i := 0; i < 4; i++ {
		s := fmt.Sprintf("%d\n", i)
		b.AddFile("F"+s, []byte(strings.Repeat(s, 1024/2)))
	}
	b.Finish()

	glob := filepath.Join(dir, "*")
	fs, err := filepath.Glob(glob)
	if err != nil {
		t.Fatalf("Glob(%s): %v", glob, err)
	} else if len(fs) != 4 {
		t.Fatalf("Glob(%s): got %v, want 4 shards", glob, fs)
	}

	if fi, err := os.Lstat(fs[0]); err != nil {
		t.Fatalf("Lstat: %v", err)
	} else if fi.Mode()&0666 == 0600 {
		// This fails spuriously if your umask is very restrictive.
		t.Errorf("got mode %o, should respect umask.", fi.Mode())
	}

	// Do again, without sharding.
	opts.ShardMax = 1 << 20
	b, err = NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	for i := 0; i < 4; i++ {
		s := fmt.Sprintf("%d\n", i)
		b.AddFile("F"+s, []byte(strings.Repeat(s, 1024/2)))
	}
	b.Finish()

	fs, err = filepath.Glob(glob)
	if err != nil {
		t.Fatalf("Glob(%s): %v", glob, err)
	} else if len(fs) != 1 {
		t.Fatalf("Glob(%s): got %v, want 1 shard", glob, fs)
	}

	// Again, but don't index anything; should leave old shards intact.
	b, err = NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	b.Finish()

	fs, err = filepath.Glob(glob)
	if err != nil {
		t.Fatalf("Glob(%s): %v", glob, err)
	} else if len(fs) != 1 {
		t.Fatalf("Glob(%s): got %v, want 1 shard", glob, fs)
	}
}

func TestPartialSuccess(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}

	opts := Options{
		IndexDir: dir,
		ShardMax: 1024,
		SizeMax:  1 << 20,
	}
	opts.RepositoryDescription.Name = "repo"
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	for i := 0; i < 4; i++ {
		nm := fmt.Sprintf("F%d", i)

		// no error checking: the 2nd call will fail
		b.AddFile(nm, []byte(strings.Repeat("01234567\n", 128)))
		if i == 1 {
			// force writes to fail.
			if err := os.Chmod(dir, 0555); err != nil {
				t.Fatalf("chmod(%s): %s", dir, err)
			}
		}
	}

	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatalf("chmod(%s, writable): %s", dir, err)
	}

	b.Finish()

	if fs, err := filepath.Glob(dir + "/*"); err != nil {
		t.Errorf("glob(%s): %v", dir, err)
	} else if len(fs) != 0 {
		t.Errorf("got shards %v, want []", fs)
	}
}

type filerankCase struct {
	name string
	docs []*zoekt.Document
	want []int
}

func testFileRankAspect(t *testing.T, c filerankCase) {
	var want []*zoekt.Document
	for _, j := range c.want {
		want = append(want, c.docs[j])
	}

	got := make([]*zoekt.Document, len(c.docs))
	copy(got, c.docs)
	sortDocuments(got)

	print := func(ds []*zoekt.Document) string {
		r := ""
		for _, d := range ds {
			r += fmt.Sprintf("%v, ", d)
		}
		return r
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got docs [%v], want [%v]", print(got), print(want))
	}
}

func TestFileRank(t *testing.T) {
	for _, c := range []filerankCase{{
		name: "filename",
		docs: []*zoekt.Document{
			{
				Name:    "longlonglong",
				Content: []byte("bla"),
			},
			{
				Name:    "short",
				Content: []byte("bla"),
			}},
		want: []int{1, 0},
	}, {
		name: "test",
		docs: []*zoekt.Document{
			{
				Name:    "test",
				Content: []byte("bla"),
			},
			{
				Name:    "longlonglong",
				Content: []byte("bla"),
			},
		},
		want: []int{1, 0},
	}, {
		name: "content",
		docs: []*zoekt.Document{
			{
				Content: []byte("bla"),
			},
			{
				Content: []byte("blablablabla"),
			},
			{
				Content: []byte("blabla"),
			},
		},
		want: []int{0, 2, 1},
	}} {
		t.Run(c.name, func(t *testing.T) {
			testFileRankAspect(t, c)
		})
	}
}

func TestEmptyContent(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}

	opts := Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
	}
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	b.Finish()

	fs, _ := filepath.Glob(dir + "/*")
	if len(fs) != 1 {
		t.Fatalf("want a shard, got %v", fs)
	}

	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}
	defer ss.Close()

	ctx := context.Background()
	result, err := ss.List(ctx, &query.Const{Value: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(result.Repos) != 1 || result.Repos[0].Repository.Name != "repo" {
		t.Errorf("got %+v, want 1 repo.", result.Repos)
	}
}

func TestSizeLimit(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}

	opts := Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
		SizeMax: 1,
	}
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	b.AddFile("F1", []byte("foobar"))
	b.Finish()

	fs, _ := filepath.Glob(dir + "/*")
	if len(fs) != 1 {
		t.Fatalf("want a shard, got %v", fs)
	}

	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}
	defer ss.Close()

	q, err := query.Parse("NOT-INDEXED")
	if err != nil {
		t.Fatalf("Parse(NOT-INDEXED): %v", err)
	}

	var sOpts zoekt.SearchOptions
	ctx := context.Background()
	result, err := ss.Search(ctx, q, &sOpts)
	if err != nil {
		t.Fatalf("Search(%v): %v", q, err)
	} else if len(result.Files) != 1 {
		t.Fatalf("got %v, want 1 file", result.Files)
	}
}

func TestBinaryFile(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}

	opts := Options{
		IndexDir: dir,
		RepositoryDescription: zoekt.Repository{
			Name: "repo",
		},
	}
	opts.SetDefaults()

	b, err := NewBuilder(opts)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	b.AddFile("F1", []byte("zero\x00byte"))
	b.Finish()

	fs, _ := filepath.Glob(dir + "/*")
	if len(fs) != 1 {
		t.Fatalf("want a shard, got %v", fs)
	}

	ss, err := shards.NewDirectorySearcher(dir)
	if err != nil {
		t.Fatalf("NewDirectorySearcher(%s): %v", dir, err)
	}
	defer ss.Close()

	q, err := query.Parse("NOT-INDEXED")
	if err != nil {
		t.Fatalf("Parse(NOT-INDEXED): %v", err)
	}

	var sOpts zoekt.SearchOptions
	ctx := context.Background()
	result, err := ss.Search(ctx, q, &sOpts)
	if err != nil {
		t.Fatalf("Search(%v): %v", q, err)
	} else if len(result.Files) != 1 {
		t.Fatalf("got %v, want 1 file", result.Files)
	}

	if result.Files[0].Language != "binary" {
		t.Errorf("got %v, want binary", result.Files[0].Language)
	}
}
