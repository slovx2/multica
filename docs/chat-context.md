# Chat context and local directory sync

Chat turns can safely refresh a project's `local_directory` before starting the
runtime. `resource_ref.auto_sync` accepts `off`, `fetch`, or `fetch_ff`; an absent
value means `fetch_ff`. The daemon always fetches unless sync is off. It only
fast-forwards a clean, tracked branch that is behind and not ahead. Ignored files
do not count as changes. Dirty trees, detached HEADs, missing upstreams, divergent
history, and a directory held by another daemon task are preserved. Fetch failures
produce a warning and a prompt note, without failing the chat.

The Sync button uses a bounded heartbeat request independently of the chat queue.
An explicit click requests fetch and safe fast-forward even when automatic sync
is off. Requests time out after one minute and old records are removed when new
requests are created. Workspace and chat deletion also remove their sync requests.
No agent/model runs for this operation.

Context usage is native request data, separate from accumulated billing usage:

- Claude: the latest main-session assistant's input, cache-read, and cache-creation
  counts, together with its model's `contextWindow` in the result. A native
  `compact_boundary.post_tokens` updates usage after manual compaction when a
  native window is also available.
- Codex: `tokenUsage.last.totalTokens` and `tokenUsage.modelContextWindow`.

Missing values remain hidden. The composer shows percentage and window size, with
exact counts on hover; values strictly above 80% are yellow and above 95% are red.
Automatic and manual compaction share the same status display. Claude's native
before/after counts are shown when present; Codex counts are not estimated.

The plus menu's Compact context action requires confirmation, then joins the same
queue as a chat turn. Claude resumes with `/compact`; Codex resumes the thread and
calls `thread/compact/start`. The action cannot fall back to a new conversation or
produce a normal assistant reply. Other providers cannot invoke it. An updated
daemon advertising `chat-context-v1` is required; older daemons fail the action
explicitly. This feature is shared by the web and desktop composer. Mobile UI is
not changed.

Migrations 570–572 include rollback files. Deploy backend/web changes and update
local daemons (and the desktop bundle's daemon) to enable all controls. No new
application dependencies are introduced.

`docs/assets/qora-13/` contains browser screenshots of the real shared components
rendered with sanitized native-event fixture data, rather than a live provider run.
