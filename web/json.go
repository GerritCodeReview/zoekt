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
	"io/ioutil"
	"net/http"

	"github.com/google/zoekt"
	"github.com/google/zoekt/query"
)

// For JSON Get,
type SearchRequest struct {
	Query    string
	Restrict []SearchRequestRestriction
}

type SearchRequestRestriction struct {
	Repo     string
	Branches []string
}

// For JSON return
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
	Fragments  []*SearchResponseFragment
}

type SearchResponseFragment struct {
	Start int
	End   int
}

const jsonType = "application/json; charset=utf-8"

func (s *Server) serveSearchAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "must use POST", http.StatusMethodNotAllowed)
		return
	}

	if got := r.Header.Get("Content-Type"); got != jsonType {
		http.Error(w, "must use "+jsonType, http.StatusNotAcceptable)
		return
	}

	content, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req SearchRequest
	if err := json.Unmarshal(content, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	q, err := query.Parse(req.Query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
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
	result, err := s.Searcher.Search(ctx, finalQ, &options)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// TODO - make this tunable. Use a query param or a JSON struct?
	num := 50
	if len(result.Files) > num {
		result.Files = result.Files[:num]
	}
	var resp SearchResponse
	for _, f := range result.Files {
		srf := SearchResponseFile{
			Repo:     f.Repo,
			Branches: f.Branches,
			FileName: f.Name,
			// TODO - set version
		}
		for _, m := range f.Matches {
			srl := &SearchResponseLine{
				LineNumber: m.LineNum,
				Line:       string(m.Line),
			}
			for _, fr := range m.Fragments {
				srfr := SearchResponseFragment{
					Start: fr.LineOff,
					End:   fr.LineOff + fr.MatchLength,
				}
				srl.Fragments = append(srl.Fragments, &srfr)
			}
			srf.Lines = append(srf.Lines, srl)
		}
		resp.Files = append(resp.Files, &srf)
	}

	content, err = json.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", jsonType)
	if _, err := w.Write(content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
