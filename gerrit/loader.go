package gerrit

import (
	"net/url"
	"sync"

	"github.com/google/zoekt"
	"github.com/google/zoekt/shards"
)

type closer interface {
	Close()
}

type gerritFilterLoader struct {
	zoekt.Searcher

	checker *gerritChecker
	mu      sync.Mutex
	watcher closer

	shardedSearcherChan chan shards.ShardLoadEvent

	// key => searcher. TODO - this doesn't work well for
	// multi-shard repos.
	filtersByShard map[string]*gerritPermissionWrapper
}

func (l *gerritFilterLoader) Close() {
	l.watcher.Close()
	l.Searcher.Close()
}

func NewGerritSearcher(dir, gerritURL, user, passwd string) (zoekt.Searcher, error) {
	evs := make(chan shards.ShardLoadEvent, 1)
	w, err := shards.NewShardWatcher(dir, evs)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(gerritURL)
	if err != nil {
		return nil, err
	}

	checker := &gerritChecker{
		adminUser:     user,
		adminPassword: passwd,
		gerritURL:     u,
	}

	next := make(chan shards.ShardLoadEvent, 1)
	sharded := shards.NewShardedSearcher(next)
	loader := &gerritFilterLoader{
		checker:             checker,
		watcher:             w,
		Searcher:            sharded,
		shardedSearcherChan: next,
		filtersByShard:      make(map[string]*gerritPermissionWrapper),
	}
	go loader.loop(evs)
	return loader, nil
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
