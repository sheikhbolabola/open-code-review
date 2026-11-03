// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package diff

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/gitcmd"
)

func TestWorkspaceTrackedGitignoreChangeMatrix(t *testing.T) {
	patterns := []struct {
		name    string
		pattern string
		tracked string
		ignored string
	}{
		{"directory", "bin/\n", "bin/release.py", "bin/untracked.py"},
		{"glob", "*.py\n", "src/release.py", "src/untracked.py"},
		{"rooted", "/release*.py\n", "release.py", "release-new.py"},
		{"quoted_path", "bin/\n", "bin/release\tname.py", "bin/untracked.py"},
	}
	for _, pattern := range patterns {
		for _, state := range []string{"unstaged", "staged", "mixed", "deleted"} {
			t.Run(pattern.name+"/"+state, func(t *testing.T) {
				if runtime.GOOS == "windows" && strings.ContainsRune(pattern.tracked, '\t') {
					t.Skip("Windows does not allow tab characters in filenames")
				}
				repo := t.TempDir()
				runGitTest(t, repo, "init", "-q")
				runGitTest(t, repo, "config", "user.email", "test@example.com")
				runGitTest(t, repo, "config", "user.name", "Test User")
				runGitTest(t, repo, "config", "commit.gpgsign", "false")
				runGitTest(t, repo, "config", "core.quotepath", "true")
				write := func(path, content string) {
					t.Helper()
					full := filepath.Join(repo, filepath.FromSlash(path))
					if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				write(".gitignore", pattern.pattern)
				write("app.go", "package app\nconst Value = 1\n")
				write(pattern.tracked, "print('original')\n")
				write("node_modules/excluded.go", "package dep\nconst Value = 1\n")
				runGitTest(t, repo, "add", ".gitignore", "app.go")
				runGitTest(t, repo, "add", "-f", "--", pattern.tracked, "node_modules/excluded.go")
				runGitTest(t, repo, "commit", "-q", "-m", "seed tracked files")

				write("app.go", "package app\nconst Value = 2\n")
				write("new.go", "package app\nconst New = true\n")
				write(pattern.ignored, "print('untracked ignored')\n")
				write("node_modules/excluded.go", "package dep\nconst Value = 2\n")
				if state == "deleted" {
					if err := os.Remove(filepath.Join(repo, filepath.FromSlash(pattern.tracked))); err != nil {
						t.Fatal(err)
					}
				} else {
					write(pattern.tracked, "print('changed')\n")
					if state == "staged" || state == "mixed" {
						runGitTest(t, repo, "add", "-u", "--", pattern.tracked)
					}
					if state == "mixed" {
						write(pattern.tracked, "print('final')\n")
					}
				}

				set, err := NewWorkspaceProvider(repo, gitcmd.New(0)).GetDiffSet(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				var paths []string
				var trackedDiff string
				for _, d := range set.Included {
					path := d.NewPath
					if path == "/dev/null" {
						path = d.OldPath
					}
					paths = append(paths, path)
					if path == pattern.tracked {
						trackedDiff = d.Diff
					}
				}
				slices.Sort(paths)
				want := []string{"app.go", "new.go", pattern.tracked}
				slices.Sort(want)
				if !slices.Equal(paths, want) {
					t.Fatalf("included paths = %q, want %q", paths, want)
				}
				if len(set.Excluded) != 1 || set.Excluded[0].NewPath != "node_modules/excluded.go" {
					t.Fatalf("built-in exclusions = %+v, want node_modules/excluded.go only", set.Excluded)
				}
				marker := "+print('changed')"
				if state == "mixed" {
					marker = "+print('final')"
				} else if state == "deleted" {
					marker = "-print('original')"
				}
				if !strings.Contains(trackedDiff, marker) {
					t.Fatalf("tracked diff = %q, want %q", trackedDiff, marker)
				}
			})
		}
	}
}
