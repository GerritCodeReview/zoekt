package gitindex

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
)

// DeleteRepos deletes stale repos under a specific path in disk. The `names` argument
// stores info on whether a repo is stale or not and is used along with the `filter` argument to decide
// on repo deletion.
func DeleteRepos(baseDir string, urlPrefix *url.URL, names map[string]bool, filter *Filter) error {
	paths, err := ListRepos(baseDir, urlPrefix)
	if err != nil {
		return err
	}
	var toDelete []string
	for _, p := range paths {
		if filter.Include(filepath.Base(p)) && !names[p] {
			toDelete = append(toDelete, p)
		}
	}

	if len(toDelete) > 0 {
		log.Printf("deleting repos %v", toDelete)
	}

	var errs []string
	for _, d := range toDelete {
		if err := os.RemoveAll(filepath.Join(baseDir, d)); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors: %v", errs)
	}
	return nil
}
