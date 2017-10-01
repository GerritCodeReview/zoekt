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

package shards

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/zoekt"
)

// ShardLoadEvent signals a newly loaded or deleted index shard.
type ShardLoadEvent struct {
	// The unique identifier of the index shard, typically a filename
	Name string

	// If nil, remove the shard with given name from the index
	Searcher zoekt.Searcher
}

// ShardWatcher watches a directory for file system events, and
// produces ShardLoadEvents in response.
type ShardWatcher struct {
	dir    string
	shards map[string]time.Time
	quit   chan struct{}

	sink chan<- ShardLoadEvent
}

// NewShardWatcher creates a watcher for the given
// directory. Resulting events are posted on the channel.
// The
func NewShardWatcher(dir string, sink chan<- ShardLoadEvent) (*ShardWatcher, error) {
	w := &ShardWatcher{
		dir:    dir,
		quit:   make(chan struct{}),
		shards: make(map[string]time.Time),
		sink:   sink,
	}

	_, err := w.scan()
	if err != nil {
		return nil, err
	}

	if err := w.watch(); err != nil {
		return nil, err
	}
	return w, nil
}

func (sw *ShardWatcher) Close() {
	if sw.quit != nil {
		close(sw.quit)
		sw.quit = nil
	}
}

func loadShard(fn string) (zoekt.Searcher, error) {
	f, err := os.Open(fn)
	if err != nil {
		return nil, err
	}

	iFile, err := zoekt.NewIndexFile(f)
	if err != nil {
		return nil, err
	}
	s, err := zoekt.NewSearcher(iFile)
	if err != nil {
		iFile.Close()
		return nil, fmt.Errorf("NewSearcher(%s): %v", fn, err)
	}

	return s, nil
}

// scan returns timestamps for the files in our directory.
func (s *ShardWatcher) scan() (map[string]time.Time, error) {
	fs, err := filepath.Glob(filepath.Join(s.dir, "*.zoekt"))
	if err != nil {
		return nil, err
	}

	if len(fs) == 0 {
		return nil, fmt.Errorf("directory %s is empty", s.dir)
	}

	diskTS := map[string]time.Time{}
	for _, fn := range fs {
		key := filepath.Base(fn)
		fi, err := os.Lstat(fn)
		if err != nil {
			continue
		}

		diskTS[key] = fi.ModTime()
	}
	return diskTS, nil
}

func (s *ShardWatcher) update() error {
	ts, err := s.scan()
	if err != nil {
		return err
	}

	s.postEvents(ts)
	return nil
}

func (s *ShardWatcher) postEvents(diskTS map[string]time.Time) error {
	// Unload deleted shards.
	for k := range s.shards {
		if _, ok := diskTS[k]; !ok {
			s.sink <- ShardLoadEvent{
				Name: k,
			}
			delete(s.shards, k)
		}
	}

	for k, mtime := range diskTS {
		loadedTS, ok := s.shards[k]
		if !ok || loadedTS != mtime {
			shard, err := loadShard(filepath.Join(s.dir, k))
			log.Printf("reloading: %s, err %v ", k, err)
			if err != nil {
				continue
			}

			s.shards[k] = mtime
			s.sink <- ShardLoadEvent{
				Name:     k,
				Searcher: shard,
			}
		}
	}

	return nil
}

func (s *ShardWatcher) watch() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := watcher.Add(s.dir); err != nil {
		return err
	}

	go func() {
		if err := s.update(); err != nil {
			log.Println("update:", err)
		}

		defer watcher.Close()
		defer close(s.sink)
		for {
			select {
			case <-watcher.Events:
				if err := s.update(); err != nil {
					log.Println("update:", err)
				}
			case err := <-watcher.Errors:
				if err != nil {
					log.Println("watcher error:", err)
				}
			case <-s.quit:
				return
			}
		}
	}()
	return nil
}
