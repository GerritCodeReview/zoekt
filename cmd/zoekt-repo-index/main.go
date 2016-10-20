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
      -manifest_repo_url https://android.googlesource.com/platform/manifests \
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

	"github.com/google/slothfs/manifest"
	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	gitindex "github.com/google/zoekt/git"
	git "github.com/libgit2/git2go"
)

var _ = log.Println

type branchFile struct {
	branch, file string
	mf           *manifest.Manifest
	manifestPath string
}

func parseBranches(manifestRepoURL, revPrefix string, cache *gitindex.RepoCache, args []string) ([]branchFile, error) {
	var branches []branchFile
	if manifestRepoURL != "" {
		u, err := url.Parse(manifestRepoURL)
		if err != nil {
			return nil, err
		}

		repo, err := cache.Open(u)
		if err != nil {
			return nil, err
		}
		for _, f := range args {
			fs := strings.SplitN(f, ":", 2)
			if len(fs) != 2 {
				return nil, fmt.Errorf("cannot parse %q as BRANCH:FILE", f)
			}
			mf, err := getManifest(repo, revPrefix+fs[0], fs[1])
			if err != nil {
				return nil, fmt.Errorf("manifest %s:%s: %v", fs[0], fs[1], err)
			}

			branches = append(branches, branchFile{
				branch:       fs[0],
				file:         fs[1],
				mf:           mf,
				manifestPath: repo.Path(),
			})
		}
	} else {
		if len(args) == 0 {
			return nil, fmt.Errorf("must give XML file argument")
		}
		for _, f := range args {
			mf, err := manifest.ParseFile(f)
			if err != nil {
				return nil, err
			}

			branches = append(branches, branchFile{
				branch:       "HEAD",
				file:         filepath.Base(f),
				mf:           mf,
				manifestPath: f,
			})
		}
	}
	return branches, nil
}

func main() {
	var sizeMax = flag.Int("file_limit", 128<<10, "maximum file size")
	var shardLimit = flag.Int("shard_limit", 100<<20, "maximum corpus size for a shard")
	var parallelism = flag.Int("parallelism", 1, "maximum number of parallel indexing processes")

	revPrefix := flag.String("rev_prefix", "refs/remotes/origin/", "prefix for references")
	baseURLStr := flag.String("base_url", "", "base url to interpret repository names")
	repoCacheDir := flag.String("repo_cache", "", "root for repository cache")
	indexDir := flag.String("index", build.DefaultDir, "index directory for *.zoekt files")
	manifestRepoURL := flag.String("manifest_repo_url", "", "set a URL for a git repository holding manifest XML file. Provide the BRANCH:XML-FILE as further command-line arguments")
	manifestRevPrefix := flag.String("manifest_rev_prefix", "refs/remotes/origin/", "prefixes for branches in manifest repository")
	repoName := flag.String("name", "", "set repository name")
	repoURL := flag.String("url", "", "set repository URL")
	flag.Parse()

	if *repoCacheDir == "" {
		log.Fatal("must set --repo_cache")
	}
	repoCache := gitindex.NewRepoCache(*repoCacheDir)

	opts := build.Options{
		Parallelism: *parallelism,
		SizeMax:     *sizeMax,
		ShardMax:    *shardLimit,
		IndexDir:    *indexDir,
		RepositoryDescription: zoekt.Repository{
			Name: *repoName,
			URL:  *repoURL,
		},
	}
	opts.SetDefaults()
	baseURL, err := url.Parse(*baseURLStr)
	if err != nil {
		log.Fatal("Parse baseURL %q: %v", baseURLStr, err)
	}

	branches, err := parseBranches(*manifestRepoURL, *manifestRevPrefix, repoCache, flag.Args())
	if err != nil {
		log.Fatalf("parseBranches: %v", err)
	}
	if len(branches) == 0 {
		log.Fatal("must specify at least one branch")
	}

	opts.RepoDir = branches[0].manifestPath
	perBranch := map[string]map[gitindex.FileKey]gitindex.BlobLocation{}
	opts.SubRepositories = map[string]*zoekt.Repository{}
	for _, br := range branches {
		br.mf.Filter()
		files, err := iterateManifest(br.mf, *baseURL, *revPrefix, repoCache)
		if err != nil {
			log.Fatalf("iterateManifest: %v", err)
		}

		perBranch[br.branch] = files
		for key, loc := range files {
			_, ok := opts.SubRepositories[key.SubRepoPath]
			if ok {
				// This can be incorrect: if the layout of manifests
				// changes across branches, then the same file could
				// be in different subRepos. We'll pretend this is not
				// a problem.
				continue
			}

			desc, err := gitindex.Templates(loc.URL)
			if err != nil {
				log.Fatalf("Templates: %v", err)
			}

			opts.SubRepositories[key.SubRepoPath] = desc
		}
	}

	// key => branch
	all := map[gitindex.FileKey][]string{}
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
		data, err := loc.Blob(&k.ID)
		if err != nil {
			log.Fatal(err)
		}

		doc := zoekt.Document{
			Name:              k.FullPath(),
			Content:           data,
			SubRepositoryPath: k.SubRepoPath,
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

// iterateManifest constructs a complete tree from the given Manifest.
func iterateManifest(mf *manifest.Manifest,
	baseURL url.URL, revPrefix string,
	cache *gitindex.RepoCache) (map[gitindex.FileKey]gitindex.BlobLocation, error) {
	allFiles := map[gitindex.FileKey]gitindex.BlobLocation{}

	for _, p := range mf.Project {
		rev := mf.ProjectRevision(&p)

		projURL := baseURL
		projURL.Path = path.Join(projURL.Path, p.Name)

		topRepo, err := cache.Open(&projURL)
		if err != nil {
			return nil, err
		}

		// provide ':' to get the tree.
		obj, err := topRepo.RevparseSingle(revPrefix + rev + ":")
		if err != nil {
			return nil, fmt.Errorf("RevparseSingle(%s, %s): %v", p.Name, rev, err)
		}

		// Since the number of projects is small, it's OK to
		// free this at the end of the function.
		defer obj.Free()
		tree, err := obj.AsTree()
		if err != nil {
			return nil, err
		}

		files, err := gitindex.TreeToFiles(topRepo, tree, projURL.String(), nil)
		if err != nil {
			return nil, err
		}

		for key, repo := range files {
			allFiles[gitindex.FileKey{
				SubRepoPath: filepath.Join(p.GetPath(), key.SubRepoPath),
				Path:        key.Path,
				ID:          key.ID,
			}] = repo
		}
	}

	return allFiles, nil
}
