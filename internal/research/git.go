package research

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type GitConfig struct {
	Enabled     bool
	StorageDir  string
	AuthorName  string
	AuthorEmail string
}

type GitCommitter struct {
	cfg GitConfig
}

func NewGitCommitter(cfg GitConfig) *GitCommitter {
	return &GitCommitter{cfg: cfg}
}

func (g *GitCommitter) Commit(message string) (string, error) {
	if g == nil || !g.cfg.Enabled {
		return "", nil
	}
	if strings.TrimSpace(g.cfg.StorageDir) == "" {
		return "", fmt.Errorf("storage dir is required for research git commits")
	}

	repo, err := gogit.PlainOpen(g.cfg.StorageDir)
	if err != nil {
		repo, err = gogit.PlainInit(g.cfg.StorageDir, false)
		if err != nil {
			return "", fmt.Errorf("open or init git repo: %w", err)
		}
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("get git worktree: %w", err)
	}

	rootRel, err := filepath.Rel(g.cfg.StorageDir, filepath.Join(g.cfg.StorageDir, "root"))
	if err != nil {
		return "", fmt.Errorf("resolve wiki root path: %w", err)
	}
	if _, err := wt.Add(filepath.ToSlash(rootRel)); err != nil {
		return "", fmt.Errorf("stage wiki root: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return "", fmt.Errorf("git status: %w", err)
	}
	if !hasStagedChanges(status) {
		return "", nil
	}

	authorName := strings.TrimSpace(g.cfg.AuthorName)
	if authorName == "" {
		authorName = "LeafWiki Research Agent"
	}
	authorEmail := strings.TrimSpace(g.cfg.AuthorEmail)
	if authorEmail == "" {
		authorEmail = "research-agent@leafwiki.local"
	}
	commitMsg := strings.TrimSpace(message)
	if commitMsg == "" {
		commitMsg = "research: update records"
	}

	hash, err := wt.Commit(commitMsg, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  authorName,
			Email: authorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "empty commit") {
			return "", nil
		}
		return "", fmt.Errorf("git commit: %w", err)
	}
	return hash.String(), nil
}

func hasStagedChanges(status gogit.Status) bool {
	for _, fileStatus := range status {
		switch fileStatus.Staging {
		case gogit.Added, gogit.Modified, gogit.Deleted, gogit.Renamed:
			return true
		}
	}
	return false
}
