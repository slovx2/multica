package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLocalDirectorySync(t *testing.T) {
	for _, tc := range []struct {
		name, mode, want                                                  string
		dirty, untracked, ahead, behind, noUpstream, fail, busy, detached bool
	}{
		{name: "behind", behind: true, want: "updated"},
		{name: "current", want: "current"},
		{name: "dirty", dirty: true, behind: true, want: "dirty"},
		{name: "untracked", untracked: true, behind: true, want: "dirty"},
		{name: "ahead", ahead: true, want: "ahead"},
		{name: "diverged", ahead: true, behind: true, want: "diverged"},
		{name: "no upstream", noUpstream: true, want: "no_upstream"},
		{name: "fetch failed", fail: true, want: "fetch_failed"},
		{name: "fetch only", mode: "fetch", behind: true, want: "fetch_only"},
		{name: "off", mode: "off", behind: true, want: "disabled"},
		{name: "busy", busy: true, behind: true, want: "directory_busy"},
		{name: "detached", detached: true, behind: true, want: "detached_head"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			remote := filepath.Join(root, "remote")
			seed := filepath.Join(root, "seed")
			local := filepath.Join(root, "local")
			git := func(dir string, args ...string) string {
				t.Helper()
				c := exec.Command("git", args...)
				c.Dir = dir
				c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
				out, err := c.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %s %v", args, out, err)
				}
				return string(out)
			}
			git(root, "init", "--bare", "--initial-branch=main", remote)
			git(root, "clone", remote, seed)
			if err := os.WriteFile(filepath.Join(seed, "file"), []byte("initial"), 0600); err != nil {
				t.Fatal(err)
			}
			git(seed, "add", ".")
			git(seed, "commit", "-m", "initial")
			git(seed, "push", "-u", "origin", "main")
			git(root, "clone", remote, local)
			before := git(local, "rev-parse", "HEAD")
			if tc.behind {
				git(seed, "commit", "--allow-empty", "-m", "remote advance")
				git(seed, "push")
			}
			if tc.ahead {
				git(local, "commit", "--allow-empty", "-m", "local advance")
				before = git(local, "rev-parse", "HEAD")
			}
			if tc.dirty {
				os.WriteFile(filepath.Join(local, "file"), []byte("dirty"), 0600)
			}
			if tc.untracked {
				os.WriteFile(filepath.Join(local, "new"), []byte("untracked"), 0600)
			}
			// Ignored files must never prevent a safe fast-forward.
			os.WriteFile(filepath.Join(local, ".git", "info", "exclude"), []byte("ignored\n"), 0600)
			os.WriteFile(filepath.Join(local, "ignored"), []byte("ignored"), 0600)
			if tc.noUpstream {
				git(local, "branch", "--unset-upstream")
			}
			if tc.detached {
				git(local, "checkout", "--detach")
			}
			if tc.fail {
				git(local, "remote", "set-url", "origin", filepath.Join(root, "missing"))
			}
			got := syncLocalDirectory(context.Background(), local, tc.mode, !tc.busy)
			actual := got.Reason
			if actual == "" {
				actual = got.Status
			}
			if actual != tc.want {
				t.Fatalf("got %+v want %s", got, tc.want)
			}
			after := git(local, "rev-parse", "HEAD")
			if tc.want == "updated" {
				if before == after || got.Updated != 1 {
					t.Fatalf("did not fast forward: %+v", got)
				}
			} else if after != before {
				t.Fatal("unsafe HEAD change")
			}
			if tc.behind && tc.mode != "off" && !tc.fail {
				if git(local, "rev-parse", "origin/main") != git(seed, "rev-parse", "HEAD") {
					t.Fatal("fetch did not update remote ref")
				}
			}
		})
	}
}
