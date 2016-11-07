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

// package build implements a more convenient interface for building
// zoekt indices.
package build

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	"github.com/google/zoekt"
)

var DefaultDir = filepath.Join(os.Getenv("HOME"), ".zoekt")

// Branch describes a single branch version.
type Branch struct {
	Name    string
	Version string
}

// Options sets options for the index building.
type Options struct {
	// IndexDir is a directory that holds *.zoekt index files.
	IndexDir string

	// SizeMax is the maximum file size
	SizeMax int

	// Parallelism is the maximum number of shards to index in parallel
	Parallelism int

	// ShardMax sets the maximum corpus size for a single shard
	ShardMax int

	// RepositoryDescription holds names and URLs for the repository.
	RepositoryDescription zoekt.Repository

	// SubRepositories is a path => sub repository map.
	SubRepositories map[string]*zoekt.Repository

	// RepoDir is the path to the repository on the indexing machine.
	RepoDir string

	// Path to exuberant ctags binary to run
	CTags string

	// Path to namespace-sandbox from Bazel
	NamespaceSandbox string

	// Write memory profiles to this file.
	MemProfile string
}

// Builder manages (parallel) creation of uniformly sized shards.
type Builder struct {
	opts     Options
	throttle chan int

	nextShardNum int
	todo         []*zoekt.Document
	size         int

	building sync.WaitGroup

	errMu      sync.Mutex
	buildError error
}

// SetDefaults sets reasonable default options.
func (o *Options) SetDefaults() {
	if o.CTags == "" {
		ctags, err := exec.LookPath("ctags-universal")
		if err == nil {
			o.CTags = ctags
		}
	}

	if o.CTags == "" {
		ctags, err := exec.LookPath("ctags-exuberant")
		if err == nil {
			o.CTags = ctags
		}
	}
	if o.NamespaceSandbox == "" {
		ns, err := exec.LookPath("zoekt-sandbox")
		if err == nil {
			o.NamespaceSandbox = ns
		}
	}
	if o.Parallelism == 0 {
		o.Parallelism = 1
	}
	if o.SizeMax == 0 {
		o.SizeMax = 128 << 10
	}
	if o.ShardMax == 0 {
		o.ShardMax = 128 << 20
	}
}

// Timestamp returns the timestamp of the existing index file, or the
// zero time value if none is found.
func (o *Options) Timestamp() time.Time {
	var zero time.Time
	nm, err := shardName(o.IndexDir, o.RepoDir, 0)
	if err != nil {
		return zero
	}

	fi, err := os.Lstat(nm)
	if err != nil {
		return zero
	}

	return fi.ModTime()
}

// NewBuilder creates a new Builder instance.
func NewBuilder(opt Options) (*Builder, error) {
	if opt.RepoDir == "" {
		return nil, fmt.Errorf("must set options.RepoDir")
	}

	b := &Builder{
		opts:     opt,
		throttle: make(chan int, opt.Parallelism),
	}

	if _, err := b.newShardBuilder(); err != nil {
		return nil, err
	}

	return b, nil
}

func (b *Builder) AddFile(name string, content []byte) {
	b.Add(zoekt.Document{Name: name, Content: content})
}

func (b *Builder) Add(doc zoekt.Document) error {
	if len(doc.Content) > b.opts.SizeMax {
		return nil
	}

	if !zoekt.IsText(doc.Content) {
		return nil
	}

	b.todo = append(b.todo, &doc)
	b.size += len(doc.Name) + len(doc.Content)
	if b.size > b.opts.ShardMax {
		return b.flush()
	}

	return nil
}

func (b *Builder) Finish() error {
	b.flush()
	b.building.Wait()
	if b.nextShardNum > 0 {
		b.deleteRemainingShards()
	}
	return b.buildError
}

func (b *Builder) deleteRemainingShards() {
	for {
		shard := b.nextShardNum
		b.nextShardNum++
		name, err := shardName(b.opts.IndexDir, b.opts.RepoDir, shard)
		if err != nil {
			break
		}

		if err := os.Remove(name); os.IsNotExist(err) {
			break
		}
	}
}

func (b *Builder) flush() error {
	todo := b.todo
	b.todo = nil
	b.size = 0
	b.errMu.Lock()
	defer b.errMu.Unlock()
	if b.buildError != nil {
		return b.buildError
	}

	if len(todo) == 0 {
		return nil
	}

	shard := b.nextShardNum
	b.nextShardNum++

	if b.opts.Parallelism > 1 {
		b.building.Add(1)
		go func() {
			b.throttle <- 1
			err := b.buildShard(todo, shard)
			<-b.throttle

			b.errMu.Lock()
			defer b.errMu.Unlock()
			if err != nil && b.buildError == nil {
				b.buildError = err
			}
			b.building.Done()
		}()
	} else {
		// No goroutines when we're not parallel. This
		// simplifies memory profiling.
		b.buildError = b.buildShard(todo, shard)

		if b.opts.MemProfile != "" {
			// drop memory, and profile.
			todo = nil
			b.writeMemProfile(b.opts.MemProfile)
		}

		return b.buildError
	}

	return nil
}

var profileNumber int

func (b *Builder) writeMemProfile(name string) {
	nm := fmt.Sprintf("%s.%d", name, profileNumber)
	profileNumber++
	f, err := os.Create(nm)
	if err != nil {
		log.Fatal("could not create memory profile: ", err)
	}
	runtime.GC() // get up-to-date statistics
	if err := pprof.WriteHeapProfile(f); err != nil {
		log.Fatal("could not write memory profile: ", err)
	}
	f.Close()
	log.Printf("wrote mem profile %q", nm)
}

func (b *Builder) buildShard(todo []*zoekt.Document, nextShardNum int) error {
	if b.opts.CTags != "" {
		if err := ctagsAddSymbols(todo, b.opts.CTags, b.opts.NamespaceSandbox); err != nil {
			log.Printf("ignoring %s error: %v", b.opts.CTags, err)
		}
	}

	name, err := shardName(b.opts.IndexDir, b.opts.RepoDir, nextShardNum)
	if err != nil {
		return err
	}

	shardBuilder, err := b.newShardBuilder()
	if err != nil {
		return err
	}

	for _, t := range todo {
		shardBuilder.Add(*t)
	}
	return writeShard(name, shardBuilder)
}

func (b *Builder) newShardBuilder() (*zoekt.IndexBuilder, error) {
	shardBuilder := zoekt.NewIndexBuilder()
	if err := shardBuilder.AddRepository(&b.opts.RepositoryDescription); err != nil {
		return nil, err
	}
	for _, b := range b.opts.RepositoryDescription.Branches {
		if err := shardBuilder.AddBranch(b.Name, b.Version); err != nil {
			return nil, err
		}
	}
	for k, v := range b.opts.SubRepositories {
		if err := shardBuilder.AddSubRepository(k, v); err != nil {
			return nil, fmt.Errorf("AddSubRepository(%s): %s", k, err)
		}
	}
	return shardBuilder, nil
}

func shardName(dir string, repoDir string, shardNum int) (string, error) {
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir,
		fmt.Sprintf("%s_v%d.%05d.zoekt", strings.Replace(abs, "/", "_", -1), zoekt.IndexFormatVersion, shardNum)), nil
}

func writeShard(fn string, b *zoekt.IndexBuilder) error {
	dir := filepath.Dir(fn)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	f, err := ioutil.TempFile(dir, filepath.Base(fn))
	if err != nil {
		return err
	}

	defer f.Close()
	if err := b.Write(f); err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), fn); err != nil {
		return err
	}

	log.Printf("wrote %s: %d index bytes (overhead %3.1f)", fn, fi.Size(),
		float64(fi.Size())/float64(b.ContentSize()+1))
	return nil
}
