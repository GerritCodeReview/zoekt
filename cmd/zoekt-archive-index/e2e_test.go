package main

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"github.com/google/zoekt"
	"github.com/google/zoekt/build"
	"github.com/google/zoekt/query"
	"github.com/google/zoekt/shards"
)

type file struct {
	name, body string
}

func makeArchive(w io.Writer, files []file) error {
	tw := tar.NewWriter(w)

	for _, file := range files {
		hdr := &tar.Header{
			Name: file.name,
			Mode: 0600,
			Size: int64(len(file.body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write([]byte(file.body)); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}

	return nil
}

func TestIndexArg(t *testing.T) {
	indexdir, err := ioutil.TempDir("", "TestIndexArg-index")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer os.RemoveAll(indexdir)
	archive, err := ioutil.TempFile("", "TestIndexArg-archive")
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	defer os.Remove(archive.Name())

	fileSize := 1000

	files := make([]file, 4)
	for i := 0; i < 4; i++ {
		s := fmt.Sprintf("%d", i)
		files[i] = file{"F" + s, strings.Repeat("a", fileSize)}
	}

	err = makeArchive(archive, files)
	if err != nil {
		t.Fatalf("unable to create archive %v", err)
	}

	tests := []struct {
		sizeMax      int
		wantNumFiles int
	}{
		{
			sizeMax:      fileSize - 1,
			wantNumFiles: 0,
		},
		{
			sizeMax:      fileSize + 1,
			wantNumFiles: 4,
		},
	}

	for _, test := range tests {
		bopts := build.Options{
			SizeMax:  test.sizeMax,
			IndexDir: indexdir,
		}
		opts := Options{
			Incremental: true,
			Archive:     archive.Name(),
			Name:        "repo",
			Branch:      "master",
			Commit:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Strip:       0,
		}

		if err := do(opts, bopts); err != nil {
			t.Fatalf("error creating index: %v", err)
		}

		ss, err := shards.NewDirectorySearcher(indexdir)
		if err != nil {
			t.Fatalf("NewDirectorySearcher(%s): %v", indexdir, err)
		}

		q, err := query.Parse("aaa")
		if err != nil {
			t.Fatalf("Parse(aaa): %v", err)
		}

		var sOpts zoekt.SearchOptions
		ctx := context.Background()
		result, err := ss.Search(ctx, q, &sOpts)
		if err != nil {
			t.Fatalf("Search(%v): %v", q, err)
		}

		if len(result.Files) != test.wantNumFiles {
			t.Errorf("got %v, want %d files.", result.Files, test.wantNumFiles)
		}
		defer ss.Close()
	}
}
