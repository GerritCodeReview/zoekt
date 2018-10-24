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

// This binary fetches all repos of a user or organization and clones
// them.  It is strongly recommended to get a personal API token from
// https://github.com/settings/tokens, save the token in a file, and
// point the --token option to it.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"

	"github.com/google/zoekt/gitindex"
)

func main() {
	dest := flag.String("dest", "", "destination directory")
	org := flag.String("org", "", "organization to mirror")
	user := flag.String("user", "", "user to mirror")
	token := flag.String("token",
		filepath.Join(os.Getenv("HOME"), ".github-token"),
		"file holding API token.")
	forks := flag.Bool("forks", false, "also mirror forks.")
	deleteRepos := flag.Bool("delete", false, "delete missing repos")
	namePattern := flag.String("name", "", "only clone repos whose name matches the given regexp.")
	excludePattern := flag.String("exclude", "", "don't mirror repos whose names match this regexp.")
	flag.Parse()

	if *dest == "" {
		log.Fatal("must set --dest")
	}
	if (*org == "") == (*user == "") {
		log.Fatal("must set either --org or --user")
	}

	destDir := filepath.Join(*dest, "github.com")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		log.Fatal(err)
	}

	client := github.NewClient(nil)
	if *token != "" {
		content, err := ioutil.ReadFile(*token)
		if err != nil {
			log.Fatal(err)
		}

		ts := oauth2.StaticTokenSource(
			&oauth2.Token{
				AccessToken: strings.TrimSpace(string(content)),
			})
		tc := oauth2.NewClient(oauth2.NoContext, ts)
		client = github.NewClient(tc)
	}

	var repos []*github.Repository
	var err error
	if *org != "" {
		repos, err = getOrgRepos(client, *org)
	} else if *user != "" {
		repos, err = getUserRepos(client, *user)
	}

	if err != nil {
		log.Fatal(err)
	}

	if !*forks {
		trimmed := repos[:0]
		for _, r := range repos {
			if r.Fork == nil || !*r.Fork {
				trimmed = append(trimmed, r)
			}
		}
		repos = trimmed
	}

	filter, err := gitindex.NewFilter(*namePattern, *excludePattern)
	if err != nil {
		log.Fatal(err)
	}

	{
		trimmed := repos[:0]
		for _, r := range repos {
			if filter.Include(*r.Name) {
				trimmed = append(trimmed, r)
			}
		}
		repos = trimmed
	}

	if err := cloneRepos(destDir, repos); err != nil {
		log.Fatalf("cloneRepos: %v", err)
	}

	if *deleteRepos {
		if err := deleteStaleRepos(*dest, filter, repos, *org+*user); err != nil {
			log.Fatalf("deleteStaleRepos: %v", err)
		}
	}
}

func deleteStaleRepos(destDir string, filter *gitindex.Filter, repos []*github.Repository, user string) error {
	u, err := url.Parse("https://github.com/" + user)
	if err != nil {
		return err
	}

	paths, err := gitindex.ListRepos(destDir, u)
	if err != nil {
		return err
	}

	names := map[string]bool{}
	for _, r := range repos {
		u, err := url.Parse(*r.HTMLURL)
		if err != nil {
			return err
		}

		names[filepath.Join(u.Host, u.Path+".git")] = true
	}

	var toDelete []string
	for _, p := range paths {
		if filter.Include(p) && !names[p] {
			toDelete = append(toDelete, p)
		}
	}

	if len(toDelete) > 0 {
		log.Printf("deleting repos %v", toDelete)
	}

	var errs []string
	for _, d := range toDelete {
		if err := os.RemoveAll(filepath.Join(destDir, d)); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors: %v", errs)
	}
	return nil
}

func getOrgRepos(client *github.Client, org string) ([]*github.Repository, error) {
	var allRepos []*github.Repository
	opt := &github.RepositoryListByOrgOptions{}
	for {
		repos, resp, err := client.Repositories.ListByOrg(context.Background(), org, opt)
		if err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			break
		}
		var names []string
		for _, r := range repos {
			names = append(names, *r.Name)
		}

		opt.Page = resp.NextPage
		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
	}
	return allRepos, nil
}

func getUserRepos(client *github.Client, user string) ([]*github.Repository, error) {
	var allRepos []*github.Repository
	opt := &github.RepositoryListOptions{}
	for {
		repos, resp, err := client.Repositories.List(context.Background(), user, opt)
		if err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			break
		}

		var names []string
		for _, r := range repos {
			names = append(names, *r.Name)
		}

		opt.Page = resp.NextPage
		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
	}
	return allRepos, nil
}

func itoa(p *int) string {
	if p != nil {
		return strconv.Itoa(*p)
	}
	return ""
}

func cloneRepos(destDir string, repos []*github.Repository) error {
	for _, r := range repos {
		config := map[string]string{
			"zoekt.web-url-type": "github",
			"zoekt.web-url":      *r.HTMLURL,
			"zoekt.name":         filepath.Join("github.com", *r.FullName),

			"zoekt.github-stars":       itoa(r.StargazersCount),
			"zoekt.github-watchers":    itoa(r.WatchersCount),
			"zoekt.github-subscribers": itoa(r.SubscribersCount),
			"zoekt.github-forks":       itoa(r.ForksCount),
		}
		dest, err := gitindex.CloneRepo(destDir, *r.FullName, *r.CloneURL, config)
		if err != nil {
			return err
		}
		if dest != "" {
			fmt.Println(dest)
		}

	}

	return nil
}
