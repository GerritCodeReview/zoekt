// Copyright 2017 Google Inc. All rights reserved.
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

package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
)

type Project struct {
	Name     string
	CloneURL string `json:"clone_url"`
}

func getGitilesRepos(URL *url.URL, filter func(string) bool) (map[string]string, error) {
	URL.RawQuery = "format=JSON"
	resp, err := http.Get(URL.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	const xssTag = ")]}'\n"
	content = bytes.TrimPrefix(content, []byte(xssTag))

	m := map[string]*Project{}
	if err := json.Unmarshal(content, &m); err != nil {
		return nil, err
	}

	result := map[string]string{}
	for k, v := range m {
		if k == "All-Users" || k == "All-Projects" {
			continue
		}
		if !filter(k) {
			continue
		}
		result[k] = v.CloneURL
	}
	return result, nil
}
