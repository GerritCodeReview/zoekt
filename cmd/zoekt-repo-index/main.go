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

/* zoekt-repo-index indexes a repo-based repository.  The constituent
git repositories should already have been downloaded to the
--repo_cache directory, eg.

go install github.com/google/zoekt/cmd/zoekt-repo-index &&

zoekt-repo-index -base_url https://android.googlesource.com/ \
  -name Android \
  -manifest_repo ~/android-orig/.repo/manifests.git/ \
  -manifest_rev_prefix=refs/remotes/origin/ \
  -rev_prefix="refs/remotes/aosp/" \
  --repo_cache ~/android-repo-cache/ \
  -shard_limit 50000000
   master:default.xml

*/
package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/slothfs/manifest"
	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/git"
	"github.com/libgit2/git2go"
)

var _ = log.Println

func main() {
	var sizeMax = flag.Int("file_limit", 128*1024, "maximum file size")
	var shardLimit = flag.Int("shard_limit", 100<<20, "maximum corpus size for a shard")
	var parallelism = flag.Int("parallelism", 1, "maximum number of parallel indexing processes.")

	revPrefix := flag.String("rev_prefix", "refs/remotes/origin/", "prefix for references")
	baseURLStr := flag.String("base_url", "", "base url to interpret repository names")
	repoCacheDir := flag.String("repo_cache", "", "root for repository cache")
	indexDir := flag.String("index", build.DefaultDir, "index directory for *.zoekt files.")
	manifestRepo := flag.String("manifest_repo", "", "get manifest repo from here; args are BRANCH:FILES, otherwise args are XML files")
	manifestRevPrefix := flag.String("manifest_rev_prefix", "refs/remotes/origin/", "prefixes for branches in manifest repository")
	repoName := flag.String("name", "", "set repository name")
	repoURL := flag.String("url", "", "set repository URL")
	flag.Parse()

	if *repoCacheDir == "" {
		log.Fatal("must set --repo_cache")
	}
	repoCache := newRepoCache(*repoCacheDir)

	opts := build.Options{
		Parallelism: *parallelism,
		SizeMax:     *sizeMax,
		ShardMax:    *shardLimit,
		IndexDir:    *indexDir,
		RepoName:    *repoName,
		RepoURL:     *repoURL,
	}
	opts.SetDefaults()
	baseURL, err := url.Parse(*baseURLStr)
	if err != nil {
		log.Fatal("Parse baseURL %q: %v", baseURLStr, err)
	}

	type branchFile struct {
		branch, file string
		mf           *manifest.Manifest
	}
	var branches []branchFile

	if *manifestRepo != "" {
		repo, err := git.OpenRepository(*manifestRepo)
		if err != nil {
			log.Fatalf("OpenRepository(%s): %v", flag.Arg(0), err)
		}
		opts.RepoDir = flag.Arg(0)

		for _, f := range flag.Args() {
			fs := strings.SplitN(f, ":", 2)
			if len(fs) != 2 {
				log.Fatalf("cannot parse %q as BRANCH:FILE")
			}

			mf, err := getManifest(repo, *manifestRevPrefix+fs[0], fs[1])
			if err != nil {
				log.Fatalf("manifest %s:%s: %v", fs[0], fs[1], err)
			}

			branches = append(branches, branchFile{
				branch: fs[0],
				file:   fs[1],
				mf:     mf,
			})
		}
		repo.Free()
	} else {
		if len(flag.Args()) == 0 {
			log.Fatal("must give XML file argument")
		}
		for _, f := range flag.Args() {
			mf, err := manifest.ParseFile(f)
			if err != nil {
				log.Fatalf("manifest %s: %v", f, err)
			}

			branches = append(branches, branchFile{
				file: filepath.Base(f),
				mf:   mf,
			})
		}
		opts.RepoDir = flag.Arg(0)
	}

	perBranch := map[string]map[locationKey]locator{}

	for _, br := range branches {
		br.mf.Filter()
		files, err := iterateManifest(br.mf, *baseURL, *revPrefix, repoCache)
		if err != nil {
			log.Fatal("iterateManifest", err)
		}

		key := br.branch + ":" + br.file
		perBranch[key] = files
	}

	// key => branch
	all := map[locationKey][]string{}
	for br, files := range perBranch {
		for k := range files {
			all[k] = append(all[k], br)
		}
	}

	builder, err := build.NewBuilder(opts)
	if err != nil {
		log.Fatal(err)
	}

	for k, branches := range all {
		loc := perBranch[branches[0]][k]
		data, err := loc.Blob(&k.id)
		if err != nil {
			log.Fatal(err)
		}
		doc := zoekt.Document{
			Name:    k.path,
			Content: data,
		}

		for _, br := range branches {
			doc.Branches = append(doc.Branches, br)
		}

		builder.Add(doc)
	}
	builder.Finish()

}

// getManifest parses the manifest XML at the given branch/path inside a Git repository.
func getManifest(repo *git.Repository, branch, path string) (*manifest.Manifest, error) {
	obj, err := repo.RevparseSingle(branch + ":" + path)
	if err != nil {
		return nil, err
	}
	defer obj.Free()
	blob, err := obj.AsBlob()
	if err != nil {
		return nil, err
	}
	return manifest.Parse(blob.Contents())
}

// locator holds data where a file can be found. It's a struct so we
// can insert additional data into the index (eg. subrepository URLs).
type locator struct {
	repo *git.Repository
}

func (l *locator) Blob(id *git.Oid) ([]byte, error) {
	blob, err := l.repo.LookupBlob(id)
	if err != nil {
		return nil, err
	}
	defer blob.Free()
	return blob.Contents(), nil
}

type repoCache interface {
	open(url *url.URL) (*git.Repository, error)
}

type repoCacheImpl struct {
	baseDir string

	reposMu sync.Mutex
	repos   map[string]*git.Repository
}

func newRepoCache(dir string) *repoCacheImpl {
	return &repoCacheImpl{
		baseDir: dir,
		repos:   make(map[string]*git.Repository),
	}
}

func (rc *repoCacheImpl) open(u *url.URL) (*git.Repository, error) {
	key := filepath.Join(u.Host, u.Path)
	if !strings.HasSuffix(key, ".git") {
		key += ".git"
	}

	rc.reposMu.Lock()
	defer rc.reposMu.Unlock()

	r := rc.repos[key]
	if r != nil {
		return r, nil
	}

	d := filepath.Join(rc.baseDir, key)
	repo, err := git.OpenRepository(d)
	if err == nil {
		rc.repos[key] = repo
	}
	return repo, err
}

// locationKey is a single file version (possibly from multiple
// branches).
type locationKey struct {
	path string
	id   git.Oid
}

// iterateManifest constructs a complete tree from the given Manifest.
func iterateManifest(mf *manifest.Manifest,
	baseURL url.URL, revPrefix string,
	cache repoCache) (map[locationKey]locator, error) {
	allFiles := map[locationKey]locator{}
	for _, p := range mf.Project {
		rev := mf.ProjectRevision(&p)

		projURL := baseURL
		projURL.Path = path.Join(projURL.Path, p.Name)

		repo, err := cache.open(&projURL)
		if err != nil {
			return nil, err
		}

		obj, err := repo.RevparseSingle(revPrefix + rev + ":")
		if err != nil {
			return nil, fmt.Errorf("RevparseSingle(%s, %s): %v", p.Name, rev, err)
		}
		defer obj.Free()
		tree, err := obj.AsTree()
		if err != nil {
			obj.Free()
			return nil, err
		}

		submodules := false
		files, _, err := gitindex.TreeToFiles(repo, tree, submodules)
		if err != nil {
			obj.Free()
			return nil, err
		}

		for path, sha := range files {
			fullPath := filepath.Join(p.Path, path)
			allFiles[locationKey{fullPath, sha}] = locator{
				repo: repo,
				url:  projURL,
			}
		}
	}

	return allFiles, nil
}
