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
	if r.Status == "current" || r.Status == "updated" {
		return ""
	}
	reasons := map[string]string{
		"disabled": "已关闭自动同步", "dirty": "存在未提交改动或未跟踪文件",
		"ahead": "本地分支超前", "diverged": "本地与上游分支已分叉",
		"no_upstream": "当前分支没有上游", "detached_head": "当前未处于分支上",
		"fetch_failed": "抓取远端更新失败，将继续使用本地代码",
		"fetch_only":   "当前设置为仅抓取远端更新", "directory_busy": "目录正在被其他任务使用",
		"fast_forward_failed": "无法安全快进", "comparison_failed": "无法比较本地与上游分支",
		"status_failed": "无法读取工作区状态", "unsupported_mode": "不支持此同步设置",
		"invalid_directory": "本地目录不可用",
	}
	reason := reasons[r.Reason]
	if reason == "" {
		reason = "无法安全更新本地目录"
	}
	prefix := ""
	if r.Behind > 0 {
		prefix = fmt.Sprintf("本地落后 %s %d 个提交，", r.Upstream, r.Behind)
	}
	return prefix + reason + "，未自动更新。"
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
			result = syncLocalDirectory(ctx, path, "fetch_ff", req.Force || release != nil)
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
