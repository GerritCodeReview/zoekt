// This binary fetches all repos from gitlab
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/zoekt/gitindex"
	gitlab "github.com/xanzy/go-gitlab"
)

func main() {
	dest := flag.String("dest", "", "destination directory")
	gitlabURL := flag.String("url", "https://gitlab.com/api/v4/", "Gitlab URL. If not set https://gitlab.com/api/v4/ will be used")
	token := flag.String("token",
		filepath.Join(os.Getenv("HOME"), ".gitlab-token"),
		"file holding API token.")
	isMember := flag.Bool("membership", false, "only mirror repos this user is a member of ")
	deleteRepos := flag.Bool("delete", false, "delete missing repos")
	namePattern := flag.String("name", "", "only clone repos whose name matches the given regexp.")
	excludePattern := flag.String("exclude", "", "don't mirror repos whose names match this regexp.")
	flag.Parse()

	if *dest == "" {
		log.Fatal("must set --dest")
	}

	var host string
	rootURL, err := url.Parse(*gitlabURL)
	if err != nil {
		log.Fatal(err)
	}
	host = rootURL.Host

	destDir := filepath.Join(*dest, host)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		log.Fatal(err)
	}

	content, err := ioutil.ReadFile(*token)
	if err != nil {
		log.Fatal(err)
	}
	apiToken := strings.TrimSpace(string(content))

	client := gitlab.NewClient(nil, apiToken)
	client.SetBaseURL(*gitlabURL)

	opt := &gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: 10,
			Page:    1,
		},
		Membership: isMember,
	}

	var gitlabProjects []*gitlab.Project
	for {
		projects, resp, err := client.Projects.ListProjects(opt)

		if err != nil {
			log.Fatal(err)
		}

		for _, project := range projects {

			// Skip projects without a default branch - these should be projects
			// where the repository isn't enabled
			if project.DefaultBranch == "" {
				continue
			}

			gitlabProjects = append(gitlabProjects, project)
		}

		if resp.CurrentPage >= resp.TotalPages {
			break
		}

		opt.Page = resp.NextPage
	}

	filter, err := gitindex.NewFilter(*namePattern, *excludePattern)
	if err != nil {
		log.Fatal(err)
	}

	{
		trimmed := gitlabProjects[:0]
		for _, p := range gitlabProjects {
			if filter.Include(p.NameWithNamespace) {
				trimmed = append(trimmed, p)
			}
		}
		gitlabProjects = trimmed
	}

	fetchProjects(destDir, apiToken, gitlabProjects)

	if *deleteRepos {
		if err := deleteStaleProjects(*dest, filter, gitlabProjects); err != nil {
			log.Fatalf("deleteStaleProjects: %v", err)
		}
	}
}

func deleteStaleProjects(destDir string, filter *gitindex.Filter, projects []*gitlab.Project) error {

	u, err := url.Parse(projects[0].HTTPURLToRepo)
	u.Path = ""
	if err != nil {
		return err
	}

	paths, err := gitindex.ListRepos(destDir, u)
	if err != nil {
		return err
	}

	names := map[string]bool{}
	for _, p := range projects {
		u, err := url.Parse(p.HTTPURLToRepo)
		if err != nil {
			return err
		}

		names[filepath.Join(u.Host, u.Path)] = true
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

func fetchProjects(destDir, token string, projects []*gitlab.Project) {

	for _, p := range projects {
		u, err := url.Parse(p.HTTPURLToRepo)
		if err != nil {
			log.Printf("Unable to parse project URL: %v", err)
			continue
		}
		config := map[string]string{
			"zoekt.web-url-type": "gitlab",
			"zoekt.web-url":      p.WebURL,
			"zoekt.name":         filepath.Join(u.Hostname(), p.PathWithNamespace),

			"zoekt.gitlab-stars": strconv.Itoa(p.StarCount),
			"zoekt.gitlab-forks": strconv.Itoa(p.ForksCount),
		}

		cloneURL := fmt.Sprintf("%s://oauth2:%s@%s%s", u.Scheme, token, u.Host,
			u.Path)

		dest, err := gitindex.CloneRepo(destDir, p.PathWithNamespace, cloneURL, config)
		if err != nil {
			log.Printf("cloneRepos: %v", err)
			continue
		}
		if dest != "" {
			fmt.Println(dest)
		}
	}
}
