// Command zoekt-archive-index indexes an archive.
//
// Example via github.com:
//
//   zoekt-archive-index -index $PWD/index -incremental -commit b57cb1605fd11ba2ecfa7f68992b4b9cc791934d -name github.com/gorilla/mux -strip_components 1 https://codeload.github.com/gorilla/mux/legacy.tar.gz/b57cb1605fd11ba2ecfa7f68992b4b9cc791934d
package main

import (
	"flag"
	"io"
	"io/ioutil"
	"log"
	"net/url"
	"reflect"
	"strings"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/gitindex"
)

// stripComponents removes the specified number of leading path
// elements. Pathnames with fewer elements will return the empty string.
func stripComponents(path string, count int) string {
	for i := 0; path != "" && i < count; i++ {
		i := strings.Index(path, "/")
		if i < 0 {
			return ""
		}
		path = path[i+1:]
	}
	return path
}

func main() {
	var (
		sizeMax     = flag.Int("file_limit", 128*1024, "maximum file size")
		shardLimit  = flag.Int("shard_limit", 100<<20, "maximum corpus size for a shard")
		parallelism = flag.Int("parallelism", 4, "maximum number of parallel indexing processes.")
		indexDir    = flag.String("index", build.DefaultDir, "index directory for *.zoekt files.")
		incremental = flag.Bool("incremental", true, "only index changed repositories")
		ctags       = flag.Bool("require_ctags", false, "If set, ctags calls must succeed.")

		name   = flag.String("name", "", "The repository name for the archive")
		urlRaw = flag.String("url", "", "The repository URL for the archive")
		branch = flag.String("branch", "HEAD", "The branch name for the archive")
		commit = flag.String("commit", "", "The commit sha for the archive. If incremental this will avoid updating shards already at commit")
		strip  = flag.Int("strip_components", 0, "Remove the specified number of leading path elements. Pathnames with fewer elements will be silently skipped.")
	)
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	opts := build.Options{
		Parallelism:      *parallelism,
		SizeMax:          *sizeMax,
		ShardMax:         *shardLimit,
		IndexDir:         *indexDir,
		CTagsMustSucceed: *ctags,
	}

	// For now just make all these args required. In the future we can read
	// extended attributes.
	if *name == "" && *urlRaw == "" {
		log.Fatal("-name or -url required")
	}
	if *branch == "" {
		log.Fatal("-branch required")
	}
	if len(*commit) != 40 {
		log.Fatal("-commit required to be absolute commit sha")
	}
	if len(flag.Args()) != 1 {
		log.Fatal("expected argument for archive location")
	}
	archive := flag.Args()[0]

	if *name != "" {
		opts.RepositoryDescription.Name = *name
	}
	if *urlRaw != "" {
		u, err := url.Parse(*urlRaw)
		if err != nil {
			log.Fatal(err)
		}
		if err := gitindex.SetTemplatesFromOrigin(&opts.RepositoryDescription, u); err != nil {
			log.Fatal(err)
		}
	}
	opts.SetDefaults()
	opts.RepositoryDescription.Branches = []zoekt.RepositoryBranch{{Name: *branch, Version: *commit}}
	brs := []string{*branch}

	if *incremental {
		versions := opts.IndexVersions()
		if reflect.DeepEqual(versions, opts.RepositoryDescription.Branches) {
			return
		}
	}

	a, err := openArchive(archive)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()

	builder, err := build.NewBuilder(opts)
	if err != nil {
		log.Fatal(err)
	}

	for {
		f, err := a.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}

		// We do not index large files
		if f.Size > int64(opts.SizeMax) {
			continue
		}

		contents, err := ioutil.ReadAll(f)
		if err != nil {
			log.Fatal(err)
		}

		name := stripComponents(f.Name, *strip)
		if name == "" {
			continue
		}

		err = builder.Add(zoekt.Document{
			Name:     name,
			Content:  contents,
			Branches: brs,
		})
		if err != nil {
			log.Fatal(err)
		}
	}

	err = builder.Finish()
	if err != nil {
		log.Fatal(err)
	}
}
