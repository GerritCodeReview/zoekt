package gerrit

import (
	"log"
	"sync"

	"github.com/google/zoekt"
	"github.com/google/zoekt/shards"
)

type gerritFilterLoader struct {
	checker *gerritChecker
	mu      sync.Mutex

	shardedSearcher     zoekt.Searcher
	shardedSearcherChan chan shards.ShardLoadEvent

	// key => searcher. TODO - this doesn't work well for
	// multi-shard repos.
	filtersByShard map[string]*gerritPermissionWrapper
}

func NewGerritFilterLoader(url, user, passwd string, evs chan shards.ShardLoadEvent) {
	checker := &gerritChecker{
		adminUser:     user,
		adminPassword: passwd,
		gerritURL:     url,
	}

	next := make(chan shards.ShardLoadEvent, 1)
	sharded := shards.NewShardedSearcher(next)
	loader := &gerritFilterLoader{
		checker:             checker,
		shardedSearcher:     sharded,
		shardedSearcherChan: next,
		filtersByShard:      make(map[string]*gerritPermissionWrapper),
	}

	go loader.loop(evs)
	return

}

func (l *gerritFilterLoader) loop(evs chan shards.ShardLoadEvent) {
	for ev := range evs {
		l.replace(ev.Name, ev.Searcher)
	}

}

func (l *gerritFilterLoader) replace(key string, s zoekt.Searcher) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if s == nil {
		delete(l.filtersByShard, key)
	} else {
		prev, ok := l.filtersByShard[key]

		var wrapper *gerritPermissionWrapper
		var err error
		if ok {
			wrapper, err = prev.withNewShard(s)
		} else {
			wrapper, err = newWrapperFromShard(l.checker, s)
		}

		if err != nil {
			log.Println("ignoring update for %s: %v", key, err)
			s.Close()
			return
		}

		l.filtersByShard[key] = wrapper
		l.shardedSearcherChan <- shards.ShardLoadEvent{
			Name:     key,
			Searcher: wrapper,
		}
	}
}
