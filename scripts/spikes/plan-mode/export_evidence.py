#!/usr/bin/env python3
"""Export a small reviewed protocol trace, excluding reasoning/auth/bootstrap data.

This is an allowlist for this fixture, NOT a general-purpose secret scrubber.
Manually review the output before committing it.
"""

import argparse
import hashlib
import json
from pathlib import Path
import re


def keep(event):
    direction, p = event["direction"], event["payload"]
    if direction in ["launch", "exit", "observation"]:
        return event
    if direction == "send":
        return event
    if direction != "recv" or not isinstance(p, dict):
        return None
    typ = p.get("type")
    if typ == "system" and p.get("subtype") == "init":
        return dict(event, payload={k: p[k] for k in ["type", "subtype", "session_id", "tools", "model", "permissionMode", "claude_code_version"] if k in p})
    if typ in ["assistant", "user"]:
        blocks = [b for b in p.get("message", {}).get("content", [])
                  if isinstance(b, dict) and b.get("type") in ["tool_use", "tool_result", "text"]]
        return dict(event, payload={"type": typ, "message": {"content": blocks}}) if blocks else None
    if typ == "control_request":
        return event
    if typ == "result":
        return dict(event, payload={k: p[k] for k in ["type", "subtype", "is_error", "terminal_reason", "stop_reason", "session_id", "num_turns", "result"] if k in p})
    method = p.get("method", "")
    if method in ["item/tool/requestUserInput", "item/plan/delta", "serverRequest/resolved", "error"]:
        return event
    if method in ["turn/started", "turn/completed"]:
        params = p["params"]
        turn = {k: v for k, v in params["turn"].items() if k != "items"}
        return dict(event, payload={"method": method, "params": dict(params, turn=turn)})
    if method in ["item/started", "item/completed"]:
        item = p["params"]["item"]
        if item["type"] in ["plan", "agentMessage", "commandExecution", "fileChange"]:
            return event
    if "id" in p and not method:
        if "error" in p:
            return event
        r = p.get("result", {})
        if "thread" in r:
            return dict(event, payload={"id": p["id"], "result": {"thread": {"id": r["thread"]["id"]}, "model": r.get("model")}})
        if "turn" in r:
            return dict(event, payload={"id": p["id"], "result": {"turn": {k: v for k, v in r["turn"].items() if k != "items"}}})
        if r == {}:
            return event
    return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("logs", type=Path, nargs="+")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    for directory in args.logs:
        for path in sorted(directory.glob("*.jsonl")):
            if path.name == "summary.jsonl":
                continue
            events = [json.loads(line) for line in path.read_text().splitlines()]
            deltas = [e for e in events if isinstance(e["payload"], dict) and e["payload"].get("method") == "item/plan/delta"]
            delta_times = {e["time"] for e in deltas[:2] + deltas[-2:]}
            selected = []
            for event in events:
                if isinstance(event["payload"], dict) and event["payload"].get("method") == "item/plan/delta" and event["time"] not in delta_times:
                    continue
                exported = keep(event)
                if exported:
                    selected.append(exported)
            if deltas:
                text = "".join(e["payload"]["params"]["delta"] for e in deltas)
                completed = [e["payload"]["params"]["item"]["text"] for e in events
                             if isinstance(e["payload"], dict) and e["payload"].get("method") == "item/completed"
                             and e["payload"].get("params", {}).get("item", {}).get("type") == "plan"]
                selected.append({"direction": "observation", "payload": {"plan_delta_count": len(deltas),
                                 "concatenation_matches_completed_plan": completed == [text],
                                 "sha256": hashlib.sha256(text.encode()).hexdigest()}})
            data = "\n".join(json.dumps(e, ensure_ascii=False) for e in selected) + "\n"
            # Redact only known local paths, retaining protocol/session identifiers.
            data = re.sub(r"/tmp/[^\s\"\\]*multica-plan-(?:claude|codex)-[a-z0-9_]+", "<FIXTURE>", data)
            data = data.replace("/root/.claude/plans/", "<CLAUDE_PLANS>/")
            (args.out / path.name).write_text(data)
            print(path.name, len(selected), "events")


if __name__ == "__main__":
    main()
