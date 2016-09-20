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

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
)

// SearchRequest is the entry point for the /api/search POST endpoint.
type SearchRequest struct {
	Query string

	// A list of OR'd restrictions.
	Restrict []SearchRequestRestriction
}

type SearchRequestRestriction struct {
	Repo     string
	Branches []string
}

// SearchResponse is the return type for /api/search endpoint
type SearchResponse struct {
	Files []*SearchResponseFile
}

type SearchResponseFile struct {
	Repo     string
	Branches []string
	FileName string
	Lines    []*SearchResponseLine
}

type SearchResponseLine struct {
	LineNumber int
	Line       string
	Matches    []*SearchResponseMatch
}

type SearchResponseMatch struct {
	Start int
	End   int
}

const jsonContentType = "application/json; charset=utf-8"

type httpError struct {
	msg    string
	status int
}

func (e *httpError) Error() string { return fmt.Sprintf("%d: %s", e.status, e.msg) }

func (s *Server) serveSearchAPI(w http.ResponseWriter, r *http.Request) {
	if err := s.serveSearchAPIErr(w, r); err != nil {
		if e, ok := err.(*httpError); ok {
			http.Error(w, e.msg, e.status)
		}
		http.Error(w, err.Error(), http.StatusTeapot)
	}
}

func (s *Server) serveSearchAPIErr(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return &httpError{"must use POST", http.StatusMethodNotAllowed}
	}

	if got := r.Header.Get("Content-Type"); got != jsonContentType {
		return &httpError{"must use " + jsonContentType, http.StatusNotAcceptable}

	}

	content, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return &httpError{err.Error(), http.StatusBadRequest}
	}

	var req SearchRequest
	if err := json.Unmarshal(content, &req); err != nil {
		return &httpError{err.Error(), http.StatusBadRequest}
	}

	rep, err := serveSearchAPIStructured(s.Searcher, &req)
	if err != nil {
		return err
	}
	content, err = json.Marshal(rep)
	if err != nil {
		return &httpError{err.Error(), http.StatusInternalServerError}

	}

	w.Header().Set("Content-Type", jsonContentType)
	if _, err := w.Write(content); err != nil {
		return &httpError{err.Error(), http.StatusInternalServerError}
	}
	return nil
}

func serveSearchAPIStructured(searcher zoekt.Searcher, req *SearchRequest) (*SearchResponse, error) {
	q, err := query.Parse(req.Query)
	if err != nil {
		return nil, &httpError{err.Error(), http.StatusBadRequest}
	}

	var restrictions []query.Q
	for _, r := range req.Restrict {
		var branchQs []query.Q
		for _, b := range r.Branches {
			branchQs = append(branchQs, &query.Branch{b})
		}

		restrictions = append(restrictions,
			query.NewAnd(&query.Repo{r.Repo}, query.NewOr(branchQs...)))
	}

	finalQ := query.NewAnd(q, query.NewOr(restrictions...))
	var options zoekt.SearchOptions
	options.SetDefaults()

	ctx := context.Background()
	result, err := searcher.Search(ctx, finalQ, &options)
	if err != nil {
		return nil, &httpError{err.Error(), http.StatusInternalServerError}

	}

	// TODO - make this tunable. Use a query param or a JSON struct?
	num := 50
	if len(result.Files) > num {
		result.Files = result.Files[:num]
	}
	var resp SearchResponse
	for _, f := range result.Files {
		srf := SearchResponseFile{
			Repo:     f.Repository,
			Branches: f.Branches,
			FileName: f.FileName,
			// TODO - set version
		}
		for _, m := range f.LineMatches {
			srl := &SearchResponseLine{
				LineNumber: m.LineNumber,
				Line:       string(m.Line),
			}
			// Convert to unicode indices.
			charOffsets := make([]int, len(m.Line), len(m.Line)+1)
			j := 0
			for i := range srl.Line {
				charOffsets[i] = j
				j++
			}
			charOffsets = append(charOffsets, j)

			for _, fr := range m.LineFragments {
				srfr := SearchResponseMatch{
					Start: charOffsets[fr.LineOffset],
					End:   charOffsets[fr.LineOffset+fr.MatchLength],
				}

				srl.Matches = append(srl.Matches, &srfr)
			}
			srf.Lines = append(srf.Lines, srl)
		}
		resp.Files = append(resp.Files, &srf)
	}

	return &resp, nil
}
