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
	"bytes"
	"log"
	"sort"
)

var _ = log.Println

// contentProvider is an abstraction to treat matches for names and
// content with the same code.
type contentProvider struct {
	reader   *reader
	id       *indexData
	idx      uint32
	stats    *Stats
	_cb      []byte
	_data    []byte
	_nl      []uint32
	fileSize uint32
}

func (p *contentProvider) newlines() []uint32 {
	if p._nl == nil {
		p._nl = p.reader.readNewlines(p.id, p.idx)
	}
	return p._nl
}

func (p *contentProvider) data(fileName bool) []byte {
	if fileName {
		return p.id.fileNameContent[p.id.fileNameIndex[p.idx]:p.id.fileNameIndex[p.idx+1]]
	}

	if p._data == nil {
		p._data = p.reader.readContents(p.id, p.idx)
		p.stats.FilesLoaded++
		p.stats.BytesLoaded += int64(len(p._data))
	}
	return p._data
}

func (p *contentProvider) caseBits(fileName bool) []byte {
	if fileName {
		return p.id.fileNameCaseBits[p.id.fileNameCaseBitsIndex[p.idx]:p.id.fileNameCaseBitsIndex[p.idx+1]]
	}

	if p._cb == nil {
		p._cb = p.reader.readCaseBits(p.id, p.idx)
	}
	return p._cb
}

func (p *contentProvider) caseMatches(m *candidateMatch) bool {
	return m.caseMatches(p.caseBits(m.fileName))
}

func (p *contentProvider) matchContent(m *candidateMatch) bool {
	return m.matchContent(p.data(m.fileName))
}

func (p *contentProvider) fillMatch(m *candidateMatch) Match {
	var finalMatch Match
	if m.fileName {
		finalMatch = Match{
			Offset:      m.offset,
			Line:        p.id.fileName(p.idx),
			LineOff:     int(m.offset),
			MatchLength: int(m.matchSz),
			FileName:    true,
		}
	} else {
		data := p.data(false)
		endMatch := m.offset + m.matchSz

		num, start, end := m.line(p.newlines(), p.fileSize)
		for end < len(data) && endMatch > uint32(end) {
			end = bytes.IndexByte(data[end+1:], '\n')
			if end == -1 {
				end = len(data)
			}
		}

		finalMatch = Match{
			Offset:      m.offset,
			LineStart:   start,
			LineEnd:     end,
			LineNum:     num,
			LineOff:     int(m.offset) - start,
			MatchLength: int(m.matchSz),
		}
		finalMatch.Line = toOriginal(p.data(false), p.caseBits(false), start, end)
	}

	finalMatch.Score = matchScore(&finalMatch)

	return finalMatch
}

const (
	// TODO - how to scale this relative to rank?
	scorePartialWordMatch = 5000.0
	scoreWordMatch        = 50000.0
)

func matchScore(m *Match) float64 {
	startBoundary := m.LineOff < len(m.Line) && (m.LineOff == 0 || byteClass(m.Line[m.LineOff-1]) != byteClass(m.Line[m.LineOff]))

	end := int(m.LineOff) + m.MatchLength
	if end > len(m.Line) {
		// I see an occasional out-of-bounds panic when
		// computing endBoundary. Since the problem is not
		// reproducible, try to gather some more data.
		log.Panicf("end %d out of bounds (l %d), match: %#v", end, len(m.Line), m)
	}

	endBoundary := end > 0 && (end == len(m.Line) || byteClass(m.Line[end-1]) != byteClass(m.Line[end]))

	if startBoundary && endBoundary {
		return scoreWordMatch
	} else if startBoundary || endBoundary {
		return scorePartialWordMatch
	}

	return 0.0
}

type matchScoreSlice []Match

func (m matchScoreSlice) Len() int           { return len(m) }
func (m matchScoreSlice) Swap(i, j int)      { m[i], m[j] = m[j], m[i] }
func (m matchScoreSlice) Less(i, j int) bool { return m[i].Score > m[j].Score }

type fileMatchSlice []FileMatch

func (m fileMatchSlice) Len() int           { return len(m) }
func (m fileMatchSlice) Swap(i, j int)      { m[i], m[j] = m[j], m[i] }
func (m fileMatchSlice) Less(i, j int) bool { return m[i].Score > m[j].Score }

func sortMatchesByScore(ms []Match) {
	sort.Sort(matchScoreSlice(ms))
}

func sortFilesByScore(ms []FileMatch) {
	sort.Sort(fileMatchSlice(ms))
}
