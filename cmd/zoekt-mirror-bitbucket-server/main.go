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

// This binary fetches all repos of a project, and of a specific type, in case
// these are specified, and clones them. By default it fetches and clones all
// existing repos.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	git "gopkg.in/src-d/go-git.v4"

	"github.com/gfleury/go-bitbucket-v1"

	"github.com/google/zoekt/gitindex"
)

func main() {
	dest := flag.String("dest", "", "destination directory")
	bitBucketServerUrl := flag.String("url", "", "BitBucket Server url")
	userName := flag.String("username", "", "BitBucket Server username")
	passwordFile := flag.String("password", ".bitbucket-password", "file holding BitBucket Server password")
	project := flag.String("project", "", "project to mirror")
	namePattern := flag.String("name", "", "only clone repos whose name matches the given regexp.")
	excludePattern := flag.String("exclude", "", "don't mirror repos whose names match this regexp.")
	projectType := flag.String("type", "", "only clone repos whose type matches the given string.")
	flag.Parse()

	if *bitBucketServerUrl == "" {
		log.Fatal("must set --url")
	}

	rootURL, err := url.Parse(*bitBucketServerUrl)
	if err != nil {
		log.Fatalf("url.Parse(): %v", err)
	}

	if *dest == "" {
		log.Fatal("must set --dest")
	}

	destDir := filepath.Join(*dest, rootURL.Host)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		log.Fatal(err)
	}

	password := ""
	if *passwordFile == "" {
		log.Fatal("must set --password")
	} else {
		content, err := ioutil.ReadFile(*passwordFile)
		if err != nil {
			log.Fatal(err)
		}
		password = strings.TrimSpace(string(content))
	}

	basicAuth := bitbucketv1.BasicAuth{UserName: *userName, Password: password}
	ctx, cancel := context.WithTimeout(context.Background(), 120000*time.Millisecond)
	ctx = context.WithValue(ctx, bitbucketv1.ContextBasicAuth, basicAuth)
	defer cancel()

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	httpClient := &http.Client{
		Transport: tr,
	}
	httpClientConfig := func(configs *bitbucketv1.Configuration) {
		configs.HTTPClient = httpClient
	}

	apiPath, err := url.Parse("/rest")
	if err != nil {
		log.Fatal(err)
	}

	apiBaseURL := rootURL.ResolveReference(apiPath).String()

	client := bitbucketv1.NewAPIClient(
		ctx,
		bitbucketv1.NewConfiguration(apiBaseURL, httpClientConfig),
	)

	var repos []bitbucketv1.Repository

	if *project!= "" {
		repos, err = getProjectRepos(*client, *project)
	} else {
		repos, err = getAllRepos(*client)
	}

	if err != nil {
		log.Fatal(err)
	}

	filter, err := gitindex.NewFilter(*namePattern, *excludePattern)
	if err != nil {
		log.Fatal(err)
	}

	{
		trimmed := repos[:0]
		for _, r := range repos {
			if filter.Include(r.Slug) && r.Project.Type == *projectType {
				trimmed = append(trimmed, r)
			}
		}
		repos = trimmed
	}

	if err := cloneRepos(destDir, rootURL.Host, repos, password); err != nil {
		log.Fatalf("cloneRepos: %v", err)
	}
}


func getAllRepos(client bitbucketv1.APIClient) ([]bitbucketv1.Repository, error) {
	var allRepos []bitbucketv1.Repository
	var localVarOptionals map[string]interface{}
	localVarOptionals = make(map[string]interface{})
	localVarOptionals["limit"] = 1000
	localVarOptionals["start"] = 0
	for {
		resp, err := client.DefaultApi.GetRepositories_19(localVarOptionals)

		if err != nil {
			return nil, err
		}

		repos, err := bitbucketv1.GetRepositoriesResponse(resp)

		if err != nil {
			return nil, err
		}

		if len(repos) == 0 {
			break
		}
		var names []string
		for _, r := range repos {
			names = append(names, r.Slug)
		}

		localVarOptionals["start"] = localVarOptionals["start"].(int) + localVarOptionals["limit"].(int)

		allRepos = append(allRepos, repos...)
	}
	return allRepos, nil
}

func getProjectRepos(client bitbucketv1.APIClient, projectName string) ([]bitbucketv1.Repository, error) {
	var allRepos []bitbucketv1.Repository
	optionals := map[string]interface{}{}
	optionals["limit"] = 1000
	optionals["start"] = 0
	for {
		resp, err := client.DefaultApi.GetRepositoriesWithOptions(projectName, optionals)
		if err != nil {
			return nil, err
		}

		repos, err := bitbucketv1.GetRepositoriesResponse(resp)
		if err != nil {
			return nil, err
		}

		if len(repos) == 0 {
			break
		}
		var names []string
		for _, r := range repos {
			names = append(names, r.Slug)
		}

		optionals["start"] = optionals["start"].(int) + optionals["limit"].(int)

		allRepos = append(allRepos, repos...)
	}
	return allRepos, nil
}

func cloneRepos(destDir string, host string, repos []bitbucketv1.Repository, password string) error {
	for _, r := range repos {
		fullName := filepath.Join(r.Project.Key, r.Slug)
		config := map[string]string{
			"zoekt.web-url-type": "bitbucket-server",
			"zoekt.web-url":      r.Links.Self[0].Href,
			"zoekt.name":         filepath.Join(host, fullName),
		}

		httpsCloneUrl := ""
		for _, cloneUrl := range r.Links.Clone {
			if cloneUrl.Name == "http" {
				s := strings.Split(cloneUrl.Href, "@")
				httpsCloneUrl = s[0] + ":" + password + "@" + s[1]
			}
		}

		if httpsCloneUrl != "" {
			if err := gitindex.CloneRepo(destDir, fullName, httpsCloneUrl, config); err != nil {
				return fmt.Errorf("cloneRepo: %v", err)
			}
		}


		if err := updateConfig(destDir, r); err != nil {
			return fmt.Errorf("updateConfig: %v", err)
		}
	}

	return nil
}

func updateConfig(destDir string, r bitbucketv1.Repository) error {
	fullName := filepath.Join(r.Project.Key, r.Slug)
	p := filepath.Join(destDir, fullName+".git")
	repo, err := git.PlainOpen(p)
	if err != nil {
		return fmt.Errorf("PlainOpen(%s): %v", p, err)
	}

	cfg, err := repo.Config()
	if err != nil {
		return err
	}

	f, err := ioutil.TempFile(p, "")
	if err != nil {
		return err
	}
	defer f.Close()

	out, err := cfg.Marshal()
	if err != nil {
		return err
	}

	if _, err := f.Write(out); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Rename(f.Name(), filepath.Join(p, "config")); err != nil {
		return err
	}

	return nil
}
