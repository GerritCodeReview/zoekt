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

// This binary fetches all repos of a user or organization and clones them.
package main

import (
	"flag"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

func main() {
	dest := flag.String("dest", "", "destination directory")
	org := flag.String("org", "", "organization to mirror")
	user := flag.String("user", "", "organization to mirror")
	token := flag.String("token", "", "auth token")
	flag.Parse()

	if *dest == "" {
		log.Fatal("must set --dest")
	}
	if *org == "" && *user == "" {
		log.Fatal("must set --org or --user")
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

	var repos []github.Repository
	var err error
	if *org != "" {
		repos, err = getOrgRepos(client, *org)
	} else if *user != "" {
		repos, err = getUserRepos(client, *user)
	}
	if err != nil {
		log.Fatal(err)
	}

	if err := cloneRepos(destDir, repos); err != nil {
		log.Fatal(err)
	}
}

func getOrgRepos(client *github.Client, org string) ([]github.Repository, error) {
	var allRepos []github.Repository
	opt := &github.RepositoryListByOrgOptions{}
	for {
		repos, resp, err := client.Repositories.ListByOrg(org, opt)
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
		log.Println(strings.Join(names))

		opt.Page = resp.NextPage
		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
	}
	return allRepos, nil
}

func getUserRepos(client *github.Client, user string) ([]github.Repository, error) {
	var allRepos []github.Repository
	opt := &github.RepositoryListOptions{}
	for {
		repos, resp, err := client.Repositories.List(user, opt)
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
		log.Println(strings.Join(names))

		opt.Page = resp.NextPage
		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
	}
	return allRepos, nil
}

func cloneRepos(destDir string, repos []github.Repository) error {
	for _, r := range repos {
		parent := filepath.Join(destDir, filepath.Dir(*r.FullName))
		if err := os.MkdirAll(parent, 0755); err != nil {
			return err
		}

		base := *r.Name + ".git"
		if _, err := os.Lstat(filepath.Join(parent, base)); err == nil {
			continue
		}

		cmd := exec.Command("git", "clone", "--bare", *r.CloneURL, base)
		cmd.Dir = parent
		log.Println("running:", cmd.Args)
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	return nil
}
