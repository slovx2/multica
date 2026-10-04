# Chat context and local directory sync

Chat turns can safely refresh a project's `local_directory` before starting the
runtime. `resource_ref.auto_sync` accepts `off`, `fetch`, or `fetch_ff`; an absent
value means `fetch_ff`. The daemon always fetches unless sync is off. It only
fast-forwards a clean, tracked branch that is behind and not ahead. Ignored files
do not count as changes. Dirty trees, detached HEADs, missing upstreams, divergent
history, and a directory held by another daemon task are preserved. Only skipped
updates and fetch failures add a readable Chinese prompt note;
current or successfully updated directories add no note. Fetch failures also
produce a warning without failing the chat.

The Sync button uses a bounded heartbeat request independently of the chat queue.
The project-scoped endpoint works before a chat session exists. An explicit click
requests fetch and safe fast-forward even when automatic sync is off. A confirmed
`force` request bypasses the directory task lock, while preserving clean-tree,
upstream and fast-forward checks. Requests time out after one minute and old records are removed when new
requests are created. Workspace and project deletion also remove their sync requests.
No agent/model runs for this operation.

Context usage is native request data, separate from accumulated billing usage:

- Claude: the latest main-session assistant's input, cache-read, and cache-creation
  counts, together with its model's `contextWindow` in the result. A native
  `compact_boundary.post_tokens` updates usage after manual compaction when a
  native window is also available.
- Codex: `tokenUsage.last.totalTokens` and `tokenUsage.modelContextWindow`.

Missing values remain hidden until native data is available. Later turns without
new data retain the last valid usage for the same runtime. Claude uses the native
session model to select a window from multi-model results, falling back to the
previous native window on resumed sessions when unavailable. The composer shows percentage and window size, with
exact counts on hover; values strictly above 80% are yellow and above 95% are red.
Automatic and manual compaction share the same status display. Claude's native
before/after counts are shown when present; Codex counts are not estimated.

The plus menu's Compact context action requires confirmation, then joins the same
queue as a chat turn. Claude resumes with `/compact`; Codex resumes the thread and
calls `thread/compact/start`. The action cannot fall back to a new conversation or
produce a normal assistant reply. Other providers cannot invoke it. An updated
daemon advertising `chat-context-v1` is required; older daemons fail the action
explicitly. Terminal compaction failures display an error beside the retained
usage badge. Queued compaction can be removed, but cannot be edited or steered.
This feature is shared by the web and desktop composer. Mobile UI is
not changed.

Migrations 570–581 include rollback files. Migration 573 replaces the sync
session reference with project/requester IDs and force. Project cleanup and chat
supplement cleanup are application-owned; no new foreign keys are introduced.
Deploy backend/web changes and update
local daemons (and the desktop bundle's daemon) to enable all controls. No new
application dependencies are introduced.

`docs/assets/qora-13/` contains browser screenshots of the real shared components
rendered with sanitized native-event fixture data, rather than a live provider run.

## Running chat guidance

`POST /api/chat/sessions/{sessionId}/queued-tasks/{taskId}/steer` accepts a UUID
`client_request_id` and returns `task_id`, `active_task_id`, `message_id`, `status`
and optional `failure_reason`. Pending-task responses expose `steerable` on the
running head and `supplement_status` / `supplement_failure_reason` on queued rows.
Only a negotiated `task-supplement-v1` turn is steerable. Unsupported providers,
older provider versions and older daemons retain the stop-and-send action.

The queued task stays in its original position until the provider acknowledges
injection. Success moves its user message to the running task and retires the
queued task. Rejection, timeout or turn completion keeps the input queued for
normal execution. While delivery is pending, editing and duplicate steering are
rejected; removal remains available. Queued file attachments use the same ID/filename and authenticated CLI download
instructions as ordinary chat messages.

Claude accepts guidance at hook boundaries. A long-running tool can therefore
delay injection until the next hook; it does not interrupt that tool.
