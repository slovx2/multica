package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type directorySyncResult struct {
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Updated  int    `json:"updated"`
}

func (r directorySyncResult) Summary() string {
	return fmt.Sprintf("Local directory sync: %s; upstream=%s, ahead=%d, behind=%d, updated=%d. %s", r.Status, r.Upstream, r.Ahead, r.Behind, r.Updated, r.Reason)
}

// syncLocalDirectory never resets, stashes, cleans, or merges divergent history.
// All commands are bounded and non-interactive, including remote credential prompts.
func syncLocalDirectory(ctx context.Context, path, mode string, allowMerge bool) directorySyncResult {
	r := directorySyncResult{Status: "skipped"}
	if mode == "off" {
		r.Reason = "disabled"
		return r
	}
	if mode == "" {
		mode = "fetch_ff"
	}
	if mode != "fetch" && mode != "fetch_ff" {
		r.Reason = "unsupported_mode"
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath="}, args...)...)
		cmd.WaitDelay = 2 * time.Second
		cmd.Dir = path
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := git("fetch", "--all", "--no-recurse-submodules"); err != nil {
		r.Status = "warning"
		r.Reason = "fetch_failed"
		return r
	}
	if _, err := git("symbolic-ref", "--quiet", "HEAD"); err != nil {
		r.Reason = "detached_head"
		return r
	}
	upstream, err := git("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		r.Reason = "no_upstream"
		return r
	}
	r.Upstream = upstream
	target, err := git("rev-parse", "@{upstream}")
	if err != nil {
		r.Reason = "no_upstream"
		return r
	}
	counts, err := git("rev-list", "--left-right", "--count", "HEAD..."+target)
	if err != nil {
		r.Reason = "comparison_failed"
		return r
	}
	if _, err := fmt.Sscanf(counts, "%d %d", &r.Ahead, &r.Behind); err != nil {
		r.Reason = "comparison_failed"
		return r
	}
	dirty, err := git("status", "--porcelain", "--untracked-files=all")
	if err != nil {
		r.Reason = "status_failed"
		return r
	}
	switch {
	case dirty != "":
		r.Reason = "dirty"
	case r.Ahead > 0 && r.Behind > 0:
		r.Reason = "diverged"
	case r.Ahead > 0:
		r.Reason = "ahead"
	case r.Behind == 0:
		r.Status = "current"
	case mode == "fetch":
		r.Reason = "fetch_only"
	case !allowMerge:
		r.Reason = "directory_busy"
	default:
		if _, err := git("-c", "merge.autostash=false", "merge", "--ff-only", "--no-edit", target); err != nil {
			r.Reason = "fast_forward_failed"
			return r
		}
		r.Status = "updated"
		r.Updated = r.Behind
		r.Behind = 0
	}
	return r
}

func (d *Daemon) handleDirectorySync(ctx context.Context, runtimeID string, req protocol.DaemonHeartbeatPendingDirectorySync) {
	result := directorySyncResult{Status: "skipped", Reason: "invalid_directory"}
	var ref localDirectoryRef
	if json.Unmarshal(req.ResourceRef, &ref) == nil && ref.DaemonID == d.cfg.DaemonID {
		if path, err := normalizeLocalPath(ref.LocalPath); err == nil && validateLocalPath(path) == nil {
			real, _ := resolveRealPath(path)
			release := d.localPathLocks.TryAcquire(real, "sync:"+req.ID)
			result = syncLocalDirectory(ctx, path, "fetch_ff", release != nil)
			if release != nil {
				release()
			}
		}
	}
	reportCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := d.client.postJSON(reportCtx, fmt.Sprintf("/api/daemon/runtimes/%s/directory-sync", runtimeID), map[string]any{"id": req.ID, "result": result}, nil); err != nil {
		d.logger.Warn("report directory sync", "error", err)
	}
}
