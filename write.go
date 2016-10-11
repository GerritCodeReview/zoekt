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

package zoekt

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"sort"
	"time"
)

// FormatVersion is a version number. It is increased every time the
// on-disk index format is changed.
const IndexFormatVersion = 5

var _ = log.Println

type indexTOC struct {
	fileContents compoundSection
	fileNames    compoundSection
	fileSections compoundSection
	postings     compoundSection
	newlines     compoundSection
	ngramText    simpleSection

	branchMasks simpleSection
	subRepos    simpleSection

	nameNgramText simpleSection
	namePostings  compoundSection
	unaryData     simpleSection
}

func (t *indexTOC) sections() []section {
	return []section{
		// This must be first, so it can be reliably read across
		// file format versions.
		&t.unaryData,
		&t.fileContents,
		&t.fileNames,
		&t.fileSections,
		&t.newlines,
		&t.ngramText,
		&t.postings,
		&t.nameNgramText,
		&t.namePostings,
		&t.branchMasks,
		&t.subRepos,
	}
}

func (w *writer) writeTOC(toc *indexTOC) {
	secs := toc.sections()
	w.U32(uint32(len(secs)))
	for _, s := range secs {
		s.write(w)
	}
}

func (s *compoundSection) writeStrings(w *writer, strs []*searchableString) {
	s.start(w)
	for _, f := range strs {
		s.addItem(w, f.data)
	}
	s.end(w)
}

func (b *IndexBuilder) Write(out io.Writer) error {
	buffered := bufio.NewWriterSize(out, 1<<20)
	defer buffered.Flush()

	w := &writer{w: buffered}
	toc := indexTOC{}

	toc.fileContents.writeStrings(w, b.files)

	toc.newlines.start(w)
	for _, f := range b.files {
		toc.newlines.addItem(w, toDeltas(newLinesIndices(f.data)))
	}
	toc.newlines.end(w)

	toc.branchMasks.start(w)
	for _, m := range b.branchMasks {
		w.U32(m)
	}
	toc.branchMasks.end(w)

	toc.fileSections.start(w)
	for _, s := range b.docSections {
		toc.fileSections.addItem(w, marshalDocSections(s))
	}
	toc.fileSections.end(w)

	var keys []string
	for k := range b.contentPostings {
		keys = append(keys, k.String())
	}
	sort.Strings(keys)

	toc.ngramText.start(w)
	for _, k := range keys {
		w.Write([]byte(k))
	}
	toc.ngramText.end(w)

	toc.postings.start(w)
	for _, k := range keys {
		toc.postings.addItem(w, toDeltas(b.contentPostings[stringToNGram(k)]))
	}
	toc.postings.end(w)

	// names.
	toc.fileNames.writeStrings(w, b.fileNames)

	keys = keys[:0]
	for k := range b.namePostings {
		keys = append(keys, k.String())
	}
	sort.Strings(keys)

	toc.nameNgramText.start(w)
	for _, k := range keys {
		w.Write([]byte(k))
	}
	toc.nameNgramText.end(w)

	toc.namePostings.start(w)
	for _, k := range keys {
		toc.namePostings.addItem(w, toDeltas(b.namePostings[stringToNGram(k)]))
	}
	toc.namePostings.end(w)

	toc.subRepos.start(w)
	w.Write(toDeltas(b.subRepos))
	toc.subRepos.end(w)

	unaryData := indexUnaryData{
		Repository:         b.repo,
		IndexFormatVersion: IndexFormatVersion,
		IndexTime:          time.Now(),
		SubRepoMap:         b.subRepoMap,
	}

	blob, err := json.Marshal(&unaryData)
	if err != nil {
		return err
	}
	toc.unaryData.start(w)
	w.Write(blob)
	toc.unaryData.end(w)

	var tocSection simpleSection

	tocSection.start(w)
	w.writeTOC(&toc)
	tocSection.end(w)
	tocSection.write(w)
	return w.err
}

func newLinesIndices(in []byte) []uint32 {
	out := make([]uint32, 0, len(in)/30)
	for i, c := range in {
		if c == '\n' {
			out = append(out, uint32(i))
		}
	}
	return out
}
