package tree

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/crucibledx/ember/internal/harness"
)

type GitMetadata interface {
	ReadFile(name string) ([]byte, error)
	IsDir(name string) bool
}
type BranchPattern struct {
	Match *regexp.Regexp
	ID    string
}
type WorkIDOptions struct {
	Explicit map[string]string
	Patterns []BranchPattern
	Git      GitMetadata
}

func ownWorkID(run harness.Run, options WorkIDOptions) string {
	if id := options.Explicit[run.SessionID]; id != "" {
		return id
	}
	branch, gitDir := run.Branch, ""
	if options.Git != nil && (branch == "" || slices.ContainsFunc(options.Patterns, repoPattern)) {
		gitDir = discoverGit(run.Cwd, options.Git)
		if branch == "" && gitDir != "" {
			branch = readBranch(gitDir, options.Git)
		}
	}
	for _, pattern := range options.Patterns {
		if pattern.Match == nil {
			continue
		}
		match := pattern.Match.FindStringSubmatchIndex(branch)
		if match == nil {
			continue
		}
		id := string(pattern.Match.ExpandString(nil, pattern.ID, branch, match))
		if strings.Contains(id, "{repo}") {
			if gitDir == "" {
				return ""
			}
			repo := readOrigin(gitDir, options.Git)
			if repo == "" {
				return ""
			}
			id = strings.ReplaceAll(id, "{repo}", repo)
		}
		return id
	}
	return ""
}
func repoPattern(pattern BranchPattern) bool { return strings.Contains(pattern.ID, "{repo}") }
func discoverGit(cwd string, git GitMetadata) string {
	for dir := filepath.Clean(cwd); dir != "." && dir != ""; dir = filepath.Dir(dir) {
		marker := filepath.Join(dir, ".git")
		if git.IsDir(marker) {
			return marker
		}
		if data, err := git.ReadFile(marker); err == nil {
			value, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
			if ok {
				target := strings.TrimSpace(value)
				if !filepath.IsAbs(target) {
					target = filepath.Join(dir, target)
				}
				return filepath.Clean(target)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return ""
}
func readBranch(gitDir string, git GitMetadata) string {
	head, err := git.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(head))
	if strings.HasPrefix(value, "ref: refs/heads/") {
		return strings.TrimPrefix(value, "ref: refs/heads/")
	}
	return ""
}
func readOrigin(gitDir string, git GitMetadata) string {
	data, err := git.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		common, commonErr := git.ReadFile(filepath.Join(gitDir, "commondir"))
		if commonErr != nil {
			return ""
		}
		commonDir := strings.TrimSpace(string(common))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
		data, err = git.ReadFile(filepath.Join(commonDir, "config"))
		if err != nil {
			return ""
		}
	}
	inOrigin := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inOrigin = line == `[remote "origin"]`
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if inOrigin && ok && strings.TrimSpace(key) == "url" {
			return originRepo(strings.TrimSpace(value))
		}
	}
	return ""
}
func originRepo(origin string) string {
	origin = strings.TrimSuffix(strings.TrimRight(origin, "/"), ".git")
	if !strings.Contains(origin, "://") {
		if _, path, ok := strings.Cut(origin, ":"); ok {
			return path
		}
	}
	parts := strings.Split(origin, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}
