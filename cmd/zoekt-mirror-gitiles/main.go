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

// This binary fetches all repos of a Gitiles host.  It does double
// duty for other "simple" web hosts
package main

import (
	"flag"
	"log"
	"net/url"
	"os"
	"path/filepath"

	"github.com/google/zoekt/gitindex"
)

type crawlTarget struct {
	cloneURL   string
	webURL     string
	webURLType string
}

type hostCrawler func(*url.URL, func(string) bool) (map[string]*crawlTarget, error)

func main() {
	dest := flag.String("dest", "", "destination directory")
	namePattern := flag.String("name", "", "only clone repos whose name matches the regexp.")
	excludePattern := flag.String("exclude", "", "don't mirror repos whose names match this regexp.")
	hostType := flag.String("type", "gitiles", "which webserver to crawl. Choices: gitiles, cgit")
	flag.Parse()

	if len(flag.Args()) < 1 {
		log.Fatal("must provide URL argument.")
	}

	var crawler hostCrawler
	switch *hostType {
	case "gitiles":
		crawler = getGitilesRepos
	case "cgit":
		crawler = getCGitRepos
	default:
		log.Fatal("unknown host type %q", hostType)
	}

	rootURL, err := url.Parse(flag.Arg(0))
	if err != nil {
		log.Fatal("url.Parse(): %v", err)
	}

	if *dest == "" {
		log.Fatal("must set --dest")
	}

	destDir := filepath.Join(*dest, rootURL.Host)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		log.Fatal(err)
	}

	filter, err := gitindex.NewFilter(*namePattern, *excludePattern)
	if err != nil {
		log.Fatal(err)
	}

	repos, err := crawler(rootURL, filter.include)
	if err != nil {
		log.Fatal(err)
	}

	for nm, target := range repos {
		config := map[string]string{
			"zoekt.web-url":      target.webURL,
			"zoekt.web-url-type": target.webURLType,
			"zoekt.name", nm,
		}

		if err := gitindex.CloneRepo(destDir, nm, target.cloneURL, config); err != nil {
			log.Fatal(err)
		}
	}
}
