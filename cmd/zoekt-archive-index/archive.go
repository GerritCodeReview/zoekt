package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type Archive interface {
	Next() (*File, error)
	Close() error
}

type File struct {
	io.Reader
	Name string
	Size int64
}

type tarArchive struct {
	tr     *tar.Reader
	closer io.Closer
}

func (a *tarArchive) Next() (*File, error) {
	for {
		hdr, err := a.tr.Next()
		if err != nil {
			return nil, err
		}

		// We only care about files
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}

		return &File{
			Reader: a.tr,
			Name:   hdr.Name,
			Size:   hdr.Size,
		}, nil
	}
}

func (a *tarArchive) Close() error {
	return a.closer.Close()
}

type zipArchive struct {
	files []*zip.File

	last   io.Closer
	closer io.Closer
}

func (a *zipArchive) Next() (*File, error) {
	if err := a.closeLast(); err != nil {
		return nil, err
	}

	if len(a.files) == 0 {
		return nil, io.EOF
	}

	f := a.files[0]
	a.files = a.files[1:]

	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	a.last = r

	return &File{
		Reader: r,
		Name:   f.Name,
		Size:   int64(f.UncompressedSize64),
	}, nil
}

func (a *zipArchive) Close() error {
	err := a.closeLast()
	if err2 := a.closer.Close(); err2 != nil {
		err = err2
	}
	return err
}

func (a *zipArchive) closeLast() error {
	if a.last == nil {
		return nil
	}

	err := a.last.Close()
	a.last = nil
	return err
}

func newZipArchive(r io.Reader, closer io.Closer) (*zipArchive, error) {
	f, ok := r.(interface {
		io.ReaderAt
		Stat() (os.FileInfo, error)
	})
	if !ok {
		return nil, fmt.Errorf("streaming zip files not supported")
	}

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		return nil, err
	}

	// Filter out non files
	files := zr.File[:0]
	for _, f := range zr.File {
		if f.Mode().IsRegular() {
			files = append(files, f)
		}
	}

	return &zipArchive{
		files:  files,
		closer: closer,
	}, nil
}

func detectContentType(r io.Reader) (string, io.Reader, error) {
	var buf [512]byte
	n, err := io.ReadFull(r, buf[:])
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", nil, err
	}

	ct := http.DetectContentType(buf[:n])

	// If we are a seeker, we can just undo our read
	if s, ok := r.(io.Seeker); ok {
		_, err := s.Seek(int64(-n), io.SeekCurrent)
		return ct, r, err
	}

	// Otherwise return a new reader which merges in the read bytes
	return ct, io.MultiReader(bytes.NewReader(buf[:n]), r), nil
}

func openReader(u string) (io.ReadCloser, error) {
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		resp, err := http.Get(u)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			b, err := ioutil.ReadAll(io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
			if err != nil {
				return nil, err
			}
			return nil, &url.Error{
				Op:  "Get",
				URL: u,
				Err: fmt.Errorf("%s: %s", resp.Status, string(b)),
			}
		}
		return resp.Body, nil
	} else if u == "-" {
		return ioutil.NopCloser(os.Stdin), nil
	}

	return os.Open(u)
}

// openArchive opens the tar at the URL or filepath u. Also supported is tgz
// files over http.
func openArchive(u string) (ar Archive, err error) {
	readCloser, err := openReader(u)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = readCloser.Close()
		}
	}()

	ct, r, err := detectContentType(readCloser)
	if err != nil {
		return nil, err
	}
	switch ct {
	case "application/x-gzip":
		r, err = gzip.NewReader(r)
		if err != nil {
			return nil, err
		}

	case "application/zip":
		return newZipArchive(r, readCloser)
	}

	return &tarArchive{
		tr:     tar.NewReader(r),
		closer: readCloser,
	}, nil
}
