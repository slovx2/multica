#!/usr/bin/env python3
"""Opt-in, real-agent protocol spike. No Multica product code is changed.

Requires Python 3.11+ and authenticated claude/codex CLIs. Logs are private by
default; review/redact before publishing. All child processes are reaped.
"""

import argparse
import json
import os
from pathlib import Path
import queue
import signal
import subprocess
import sys
import tempfile
import threading
import time
import tomllib


class Wire:
    def __init__(self, argv, cwd, log, env=None):
        self.log = open(log, "w", encoding="utf-8")
        self.lock = threading.Lock()
        self.events = []
        self.q = queue.Queue()
        self.record("launch", {"argv": argv, "cwd": str(cwd)})
        self.p = subprocess.Popen(argv, cwd=cwd, env=env, stdin=subprocess.PIPE,
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                  text=True, bufsize=1, start_new_session=True)
        self.readers = []
        for name, stream in [("recv", self.p.stdout), ("stderr", self.p.stderr)]:
            t = threading.Thread(target=self.read, args=(name, stream))
            t.start()
            self.readers.append(t)

    def record(self, direction, payload):
        with self.lock:
            event = {"time": time.time(), "direction": direction, "payload": payload}
            self.events.append(event)
            self.log.write(json.dumps(event, ensure_ascii=False) + "\n")
            self.log.flush()

    def read(self, direction, stream):
        for line in stream:
            try:
                obj = json.loads(line)
            except ValueError:
                obj = line.rstrip()
            self.record(direction, obj)
            if direction == "recv":
                self.q.put(obj)
        if direction == "recv":
            self.q.put(None)

    def send(self, obj):
        self.record("send", obj)
        self.p.stdin.write(json.dumps(obj) + "\n")
        self.p.stdin.flush()

    def next(self, deadline):
        try:
            obj = self.q.get(timeout=max(0.01, deadline-time.monotonic()))
        except queue.Empty:
            raise TimeoutError("protocol deadline exceeded") from None
        if obj is None:
            raise EOFError(f"child closed stdout (exit={self.p.poll()})")
        return obj

    def close(self):
        if not self.p.stdin.closed:
            self.p.stdin.close()
        try:
            self.p.wait(timeout=10)
        except subprocess.TimeoutExpired:
            # This group belongs to claude/codex, never to the Multica daemon.
            os.killpg(self.p.pid, signal.SIGTERM)
            try:
                self.p.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(self.p.pid, signal.SIGKILL)
                self.p.wait()
        for t in self.readers:
            t.join()
        self.record("exit", self.p.returncode)
        self.log.close()


def fixture(root):
    root.mkdir(parents=True, exist_ok=True)
    (root / "README.md").write_text("# Color greeting demo\nA proposed CLI prints Hello in a selected color. No code exists yet.\n")
    bin_dir = root / "bin"
    bin_dir.mkdir(exist_ok=True)
    # Deliberately a local fake: testing a write without creating real issues.
    mock = bin_dir / "multica"
    mock.write_text("#!/usr/bin/env python3\nimport json,sys,pathlib\n"
                    "p=pathlib.Path(__file__).resolve().parent.parent/'mock-issues.jsonl'\n"
                    "with p.open('a') as f: f.write(json.dumps(sys.argv[1:])+'\\n')\n"
                    "print('MOCK_ONLY: issue recorded locally; no network request')\n")
    mock.chmod(0o755)
    return root


DENY_QUESTION = ("Your questions have been captured for a user card. 回答将在下一条消息给出。 "
                 "No answer is available now. End this turn now with one short waiting sentence. "
                 "Do not guess, repeat the question tool, or continue the task.")
DENY_PLAN = ("不批准。The plan has been captured for user review, not approved for execution. "
             "End this turn now with one short waiting sentence. Do not implement or call "
             "ExitPlanMode again. The decision will arrive as a new user message.")


def claude_turn(args, cwd, label, prompt, session=None, mode="plan", stdio=True, settings=None):
    argv = ["claude", "-p", "--output-format", "stream-json", "--input-format",
            "stream-json", "--verbose", "--permission-mode", mode, "--max-turns", "10",
            "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}']
    if stdio:
        argv += ["--permission-prompt-tool", "stdio"]
    if args.claude_model:
        argv += ["--model", args.claude_model]
    if session:
        argv += ["--resume", session]
    if settings:
        argv += ["--settings", json.dumps(settings)]
    env = dict(os.environ)
    env.pop("CLAUDECODE", None)
    w = Wire(argv, cwd, args.out / (label + ".jsonl"), env)
    summary = {"label": label, "mode": mode, "controls": [], "tools": []}
    try:
        w.send({"type": "user", "message": {"role": "user", "content": prompt}})
        deadline = time.monotonic() + args.timeout
        while True:
            event = w.next(deadline)
            if not isinstance(event, dict):
                continue
            if event.get("type") == "system" and event.get("subtype") == "init":
                summary.update(session=event.get("session_id"), tools=event.get("tools"),
                               model=event.get("model"), permissionMode=event.get("permissionMode"))
            if event.get("type") == "control_request":
                req = event["request"]
                summary["controls"].append(req)
                name = req.get("tool_name")
                if name in ["AskUserQuestion", "ExitPlanMode"]:
                    if name == "ExitPlanMode":
                        path = req.get("input", {}).get("planFilePath")
                        if path and Path(path).is_file():
                            summary.setdefault("plan_snapshots", []).append({
                                "path": path, "at": "control_request", "text": Path(path).read_text()})
                    answer = {"behavior": "deny", "message": DENY_QUESTION if name == "AskUserQuestion" else DENY_PLAN}
                else:
                    answer = {"behavior": "allow", "updatedInput": req.get("input", {})}
                w.send({"type": "control_response", "response": {"subtype": "success",
                       "request_id": event["request_id"], "response": answer}})
            if event.get("type") == "result":
                summary["result"] = event
                break
    except (TimeoutError, EOFError) as e:
        summary["error"] = str(e)
    finally:
        w.close()
    summary["exit"] = w.p.returncode
    return summary


class Codex:
    def __init__(self, args, cwd, label):
        self.args = args
        self.cwd = cwd
        self.seq = 0
        argv = ["codex", "app-server"]
        config_path = Path(os.environ.get("CODEX_HOME", Path.home()/".codex"))/"config.toml"
        config = tomllib.loads(config_path.read_text()) if config_path.exists() else {}
        # Keep authentication/provider/model config; disable unrelated MCP servers.
        for name in config.get("mcp_servers", {}):
            if not all(c.isalnum() or c in "_-" for c in name):
                raise ValueError("Use a managed Codex config without dotted MCP names")
            argv += ["-c", f"mcp_servers.{name}.enabled=false"]
        self.w = Wire(argv, cwd, args.out / (label + ".jsonl"))
        try:
            self.rpc("initialize", {"clientInfo": {"name": "multica-plan-spike", "version": "1.0"},
                                    "capabilities": {"experimentalApi": True}})
            self.w.send({"method": "initialized"})
        except Exception:
            self.w.close()
            raise

    def send(self, method, params):
        self.seq += 1
        self.w.send({"id": self.seq, "method": method, "params": params})
        return self.seq

    def rpc(self, method, params):
        seq = self.send(method, params)
        deadline = time.monotonic()+self.args.timeout
        while True:
            e = self.w.next(deadline)
            if isinstance(e, dict) and e.get("id") == seq and "method" not in e:
                if "error" in e:
                    raise RuntimeError(e["error"])
                return e["result"]

    def turn(self, prompt, mode, thread=None, question_action="interrupt"):
        method = "thread/resume" if thread else "thread/start"
        params = {"cwd": str(self.cwd), "approvalPolicy": "never", "sandbox": "danger-full-access"}
        if thread:
            params["threadId"] = thread
        if self.args.codex_model:
            params["model"] = self.args.codex_model
        result = self.rpc(method, params)
        thread = result["thread"]["id"]
        model = result["model"]
        summary = {"thread": thread, "model": model, "mode": mode, "requests": [], "items": [], "deltas": []}
        seq = self.send("turn/start", {"threadId": thread,
                        "input": [{"type": "text", "text": prompt}],
                        "collaborationMode": {"mode": mode, "settings": {"model": model}}})
        deadline = time.monotonic()+self.args.timeout
        while True:
            e = self.w.next(deadline)
            if not isinstance(e, dict):
                continue
            if e.get("id") == seq and "error" in e:
                raise RuntimeError(e["error"])
            method = e.get("method")
            p = e.get("params", {})
            if method == "item/tool/requestUserInput":
                summary["requests"].append(e)
                if question_action in ["interrupt", "hold-interrupt"]:
                    if question_action == "hold-interrupt":
                        time.sleep(3)
                        completed = any(x["direction"] == "recv" and isinstance(x["payload"], dict)
                                        and x["payload"].get("method") == "turn/completed"
                                        for x in self.w.events)
                        summary["hold_observation"] = {"seconds": 3, "completed": completed}
                        self.w.record("observation", summary["hold_observation"])
                    # Leave the server request unanswered, then interrupt the turn.
                    self.send("turn/interrupt", {"threadId": thread, "turnId": p["turnId"]})
                elif question_action == "empty":
                    self.w.send({"id": e["id"], "result": {"answers": {}}})
                else:
                    answers = {q["id"]: {"answers": [(q.get("options") or [{"label": "Blue"}])[0]["label"]]}
                               for q in p["questions"]}
                    self.w.send({"id": e["id"], "result": {"answers": answers}})
            elif method == "item/completed":
                summary["items"].append(p["item"])
            elif method == "item/plan/delta":
                summary["deltas"].append(p)
            elif method == "turn/completed":
                summary["turn"] = p["turn"]
                return summary
            elif "id" in e and method:
                summary.setdefault("unexpected_requests", []).append(e)
                self.w.send({"id": e["id"], "error": {"code": -32601, "message": "Unsupported by spike"}})


def save(args, summary):
    with (args.out/"summary.jsonl").open("a") as f:
        f.write(json.dumps(summary, ensure_ascii=False)+"\n")
    print(json.dumps({k: v for k, v in summary.items() if k in ["label", "session", "thread", "mode", "error", "exit"]}), flush=True)
    print("  result:", (summary.get("result", {}).get("result") or summary.get("turn", {}).get("status")), flush=True)


def run_claude(args, cwd):
    s = claude_turn(args, cwd, "claude-01-question", "We are planning a color greeting CLI. Before planning, use AskUserQuestion to ask which color: Blue or Red (with descriptions, header Color, multiSelect false). Do not choose for me. No research or other tools are needed. Remember marker ORCHID-73 for subsequent turns.")
    save(args, s)
    if not s.get("session") or s.get("error"):
        return
    session = s["session"]
    cases = [
        ("claude-02-answer-plan", "plan", "Answer to your color question: Blue. For the same greeting CLI, propose a brief implementation plan, remembering the marker. No questions or exploration needed. Write your native plan file if needed, then call ExitPlanMode to submit it. Do not implement."),
        ("claude-03-approved-plan-shell", "plan", "The proposed plan is approved for issue drafting only, not implementation. Keep plan mode. Run the real read-only command `multica --version` using Bash now and report its exact output plus the chosen color and remembered marker. Do not call ExitPlanMode."),
        ("claude-04-default", "manual", "We are now in the ordinary non-plan permission mode. Write implemented.txt containing the chosen color and remembered marker from our earlier conversation. This tiny fixture write is authorized. Then report the content. No other actions."),
        ("claude-05-back-plan", "plan", "We are back in plan mode. Read implemented.txt, then revise the previous plan by adding a --quiet flag with a test for no stdout. Keep the chosen color and marker. Submit the updated plan with ExitPlanMode. Do not implement."),
        ("claude-06-plan-write-probe", "plan", "The plan is approved for issue creation only; do not implement it. For this isolated experiment ./bin/multica is a fake CLI that only appends argv to mock-issues.jsonl and prints MOCK_ONLY, with no network access. While staying in plan mode, run `./bin/multica issue create --title PLAN_SPIKE_ONLY` using Bash to record the issue locally, then stop. Do not switch modes."),
    ]
    for label, mode, prompt in cases:
        s = claude_turn(args, cwd, label, prompt, session, mode)
        save(args, s)
    # A control session verifies that the stdio flag, not just removing disallow,
    # exposes AskUserQuestion in print mode.
    save(args, claude_turn(args, cwd, "claude-07-no-stdio", "List whether AskUserQuestion is available; no tool calls needed.", stdio=False))


def run_codex(args, cwd):
    thread = None
    cases = [
        ("codex-01-plan", "plan", "Draft a concise complete implementation plan for a standalone CLI that prints Hello in Blue and supports --plain. All requirements are fixed; no questions, tools, or exploration are needed. Remember marker ORCHID-73 and color Blue for later turns. Do not implement.", "interrupt"),
        ("codex-02-question-interrupt", "plan", "We need a user preference before revising that plan. Use request_user_input now with one question: which output style, Compact (one line) or Verbose (extra detail), id output_style, header Style. Do not choose for me or produce a plan until I answer.", "interrupt"),
        ("codex-03-answer-resume", "plan", "Answer to your interrupted question output_style: Verbose. Revise the previous greeting CLI plan using this answer, the original color, and the remembered marker. No further tools or questions needed. Do not implement.", "interrupt"),
        ("codex-04-approved-plan-shell", "plan", "The plan is approved for issue drafting only, not implementation. Keep plan mode. Run the real read-only shell command `multica --version` and report its exact output, chosen style, color and marker. No research or questions needed.", "interrupt"),
        ("codex-05-default", "default", "We are now in default mode. Write implemented.txt containing the chosen color, style and remembered marker from our earlier conversation. This tiny fixture write is authorized. Report the content and stop; no other actions.", "interrupt"),
        ("codex-06-back-plan", "plan", "We are back in plan mode. Read implemented.txt and revise the previous plan by adding a --quiet flag with a test for no stdout. Keep the chosen color, style and marker. Produce the updated plan; do not implement.", "interrupt"),
        ("codex-07-question-answer", "plan", "Use request_user_input to ask which test approach: Unit (quick) or Integration (end-to-end), id tests, header Tests. After receiving the tool answer, acknowledge it and end the turn without any more tools.", "answer"),
        ("codex-08-question-empty", "plan", "Use request_user_input to ask which documentation language: English or Chinese (with descriptions), id language, header Language. If the host returns no answer, end this turn with a waiting sentence and do not infer an answer or use more tools. The actual answer will arrive next turn.", "empty"),
        ("codex-09-empty-resume", "plan", "Answer to your documentation language question: Chinese. Acknowledge that and the chosen test approach, color, style and marker. No tools or plan needed.", "interrupt"),
        ("codex-10-plan-write-probe", "plan", "The plan is approved for issue creation only; do not implement it. For this isolated experiment ./bin/multica is a fake CLI that only appends argv to mock-issues.jsonl and prints MOCK_ONLY, with no network access. While staying in plan mode, run `./bin/multica issue create --title PLAN_SPIKE_ONLY` to record the issue locally, then stop. Do not switch modes.", "interrupt"),
    ]
    for label, mode, prompt, action in cases:
        c = None
        try:
            c = Codex(args, cwd, label)
            s = c.turn(prompt, mode, thread, action)
            thread = s["thread"]
            s["label"] = label
        except (TimeoutError, EOFError, RuntimeError) as e:
            s = {"label": label, "error": str(e)}
        finally:
            if c:
                c.w.close()
        save(args, s)
        if s.get("error"):
            break


def run_supplemental(args, cwd):
    if args.runtime == "claude":
        cases = [
            ("claude-08-forced-bash-approval", "plan", "Run only the read-only Bash command `multica --version` and report its exact output. No planning needed.", {"permissions": {"ask": ["Bash"]}}),
            ("claude-09-staged-plan", "plan", "Remember marker IRIS-92. Make a minimal plan for a Blue greeting CLI. First write the native plan file. Wait for the successful Write tool result before calling ExitPlanMode in a SEPARATE assistant response. Do not batch Write/Edit with ExitPlanMode and do not implement.", None),
            ("claude-10-bypass", "bypassPermissions", "We are now in ordinary daemon execution mode. Write bypass.txt with the earlier marker, and execute `./bin/multica issue create --title PLAN_SPIKE_ONLY`. This executable is a local fake that only records argv; there is no real issue creation or network call. Both fixture writes are authorized. Then stop.", None),
            ("claude-11-back-plan-multiselect", "plan", "We are back in plan mode. Read bypass.txt and report the remembered marker. Then use AskUserQuestion with multiSelect true to ask which features to include: Color (ANSI colors), Quiet (no stdout), Plain (no colors). Wait for my choice; do not implement.", None),
        ]
        session = None
        for label, mode, prompt, settings in cases:
            s = claude_turn(args, cwd, label, prompt, session, mode, settings=settings)
            save(args, s)
            session = s.get("session", session)
            if s.get("error"):
                break
    else:
        thread = None
        cases = [
            ("codex-11-hold-interrupt", "plan", "Ask me now using request_user_input: Which color, Blue or Red, id color, header Color, with descriptions. Wait for my answer; do not assume. Remember marker IRIS-92.", "hold-interrupt"),
            ("codex-12-hold-resume-default", "default", "Answer to the interrupted color question: Red. We are now in default mode. Write resumed.txt containing my answer and our remembered marker. Also run `./bin/multica issue create --title PLAN_SPIKE_ONLY`. This executable is a local fake that only records argv with no network call. Both fixture writes are authorized. Then stop.", "interrupt"),
            ("codex-13-back-plan-question", "plan", "We are back in plan mode. Read resumed.txt, report its content, and use request_user_input to ask which layout: Compact (one line) or Verbose (two lines), id layout, header Layout. Do not implement.", "interrupt"),
        ]
        for label, mode, prompt, action in cases:
            c = None
            try:
                c = Codex(args, cwd, label)
                s = c.turn(prompt, mode, thread, action)
                thread = s["thread"]
                s["label"] = label
            except (TimeoutError, EOFError, RuntimeError) as e:
                s = {"label": label, "error": str(e)}
            finally:
                if c:
                    c.w.close()
            save(args, s)
            if s.get("error"):
                break


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("runtime", choices=["claude", "codex"])
    p.add_argument("--out", type=Path, required=True)
    p.add_argument("--timeout", type=int, default=240)
    p.add_argument("--claude-model")
    p.add_argument("--codex-model")
    p.add_argument("--supplemental", action="store_true")
    args = p.parse_args()
    if os.environ.get("MULTICA_RUN_REAL_AGENT_SMOKE") != "1":
        p.error("Set MULTICA_RUN_REAL_AGENT_SMOKE=1 to authorize live model calls")
    args.out = args.out.resolve()
    if args.out.exists() and any(args.out.iterdir()):
        p.error("--out must be empty; preserve previous evidence instead of overwriting it")
    args.out.mkdir(parents=True, exist_ok=True)
    os.chmod(args.out, 0o700)
    cwd = fixture(Path(tempfile.mkdtemp(prefix=f"multica-plan-{args.runtime}-")))
    print(f"Fixture retained at {cwd}; private logs at {args.out}", flush=True)
    if args.supplemental:
        run_supplemental(args, cwd)
    else:
        (run_claude if args.runtime == "claude" else run_codex)(args, cwd)
    summaries = [json.loads(line) for line in (args.out / "summary.jsonl").read_text().splitlines()]
    if any(s.get("error") or s.get("result", {}).get("is_error")
           or s.get("turn", {}).get("status") == "failed" or s.get("exit", 0) != 0
           for s in summaries):
        sys.exit(1)


if __name__ == "__main__":
    main()
