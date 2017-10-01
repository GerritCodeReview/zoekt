package gerrit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
)

type gerritChecker struct {
	adminUser     string
	adminPassword string
	gerritURL     *url.URL
}

type checkAccessInput struct {
	Ref     string `json:"ref"`
	Account string `json:"account"`
}

type checkAccessInfo struct {
	Message string `json:"message"`
	Status  int    `json:"status"`
}

var gerritJSONHeader = []byte(`)]}'`)

// check issues a REST call.
func (c *gerritChecker) check(repo string, refs []string, uid int) ([]string, error) {
	var visible []string
	for _, r := range refs {
		body, err := json.Marshal(&checkAccessInput{
			Ref:     r,
			Account: strconv.Itoa(uid),
		})
		if err != nil {
			return nil, err
		}

		u := *c.gerritURL
		u.Path = fmt.Sprintf("/a/projects/%s/check.access", url.PathEscape(repo))

		req, err := http.NewRequest("POST", u.String(), bytes.NewBuffer(body))
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(c.adminUser, c.adminPassword)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("http status %d", resp.StatusCode)
		}
		cont, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		cont = bytes.TrimPrefix(cont, gerritJSONHeader)
		result := &checkAccessInfo{}
		if err := json.Unmarshal(cont, result); err != nil {
			return nil, err
		}
		if result.Status == 200 {
			visible = append(visible, r)
		}
	}
	return visible, nil
}

// permission for a single user and a single repo.
type repoUserPermission struct {
	expiry  time.Time
	visible bool
	// TODO: refs.
}

type gerritPermissionWrapper struct {
	zoekt.Searcher

	repoBranches []string
	repoName     string

	checker *gerritChecker

	mu          sync.Mutex
	permissions map[int]*repoUserPermission
}

func newWrapperFromShard(checker *gerritChecker, s zoekt.Searcher) (*gerritPermissionWrapper, error) {
	res, err := s.List(context.Background(), &query.Repo{})
	if err != nil {
		return nil, err
	}
	if len(res.Repos) != 1 {
		return nil, fmt.Errorf("should have only one repo")
	}

	r := res.Repos[0].Repository

	var branches []string
	for _, b := range r.Branches {
		branches = append(branches, b.Name)
	}
	proj := r.RawConfig["gerrit-project"]
	host := r.RawConfig["gerrit-host"]

	if host != checker.gerritURL.String() {
		return nil, fmt.Errorf("got host %q, want %q", host, checker.gerritURL)
	}

	return &gerritPermissionWrapper{
		checker:      checker,
		Searcher:     s,
		repoName:     proj,
		repoBranches: branches,
		permissions:  make(map[int]*repoUserPermission),
	}, nil
}

func (w *gerritPermissionWrapper) withNewShard(s zoekt.Searcher) (*gerritPermissionWrapper, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	newW := &gerritPermissionWrapper{
		permissions:  make(map[int]*repoUserPermission, len(w.permissions)),
		checker:      w.checker,
		repoBranches: w.repoBranches,
		repoName:     w.repoName,
	}

	// TODO - can only copy if the branches haven't changed.
	for k, v := range w.permissions {
		newW.permissions[k] = v
	}
	return newW, nil
}

func (w *gerritPermissionWrapper) check(ctx context.Context) (bool, error) {
	u, ok := fromContext(ctx)
	if !ok {
		return false, nil
	}

	w.mu.Lock()
	perm, ok := w.permissions[u.UID]
	if ok && time.Now().After(perm.expiry) {
		perm = nil
		ok = false
		delete(w.permissions, u.UID)
	}
	if !ok {
		w.mu.Unlock()
		refs, err := w.checker.check(w.repoName, w.repoBranches, u.UID)
		if err != nil {
			return false, err
		}
		w.mu.Lock()
		perm = &repoUserPermission{
			expiry:  time.Now().Add(24 * time.Hour),
			visible: len(refs) > 0,
		}
		w.permissions[u.UID] = perm
		ok = true
	}

	w.mu.Unlock()
	return perm.visible, nil
}

func (w *gerritPermissionWrapper) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	ok, err := w.check(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &zoekt.SearchResult{}, nil
	}

	return w.Searcher.Search(ctx, q, opts)

}

func (w *gerritPermissionWrapper) List(ctx context.Context, q query.Q) (*zoekt.RepoList, error) {
	ok, err := w.check(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &zoekt.RepoList{}, nil
	}

	return w.Searcher.List(ctx, q)
}

func (w *gerritPermissionWrapper) Close() {
	w.Searcher.Close()
}

func (w *gerritPermissionWrapper) String() string {
	return fmt.Sprintf("gerritPerms(%s,%s)", w.checker.gerritURL, w.Searcher.String())
}
