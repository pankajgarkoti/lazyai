#!/usr/bin/env python3
"""Offline compatibility check against an installed Codex app-server.

Uses a local deterministic Responses fixture and an isolated CODEX_HOME;
does not contact a model provider, copy credentials, or modify user hook trust.
Run after go build -o bin/lazyai ./cmd/lazyai.
"""
import argparse
import json
import os
import pathlib
import queue
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--lazyai", default="bin/lazyai")
    parser.add_argument("--codex", default="codex")
    args = parser.parse_args()
    binary = str(pathlib.Path(args.lazyai).resolve())
    events = queue.Queue()
    model_calls = 0
    snapshots = {}

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            nonlocal model_calls
            if self.path == "/v1/responses":
                json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
                model_calls += 1
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.end_headers()
                if model_calls == 1:
                    item = {"type": "custom_tool_call", "id": "patch-item", "call_id": "patch-call", "name": "apply_patch", "input": "*** Begin Patch\n*** Update File: probe.txt\n@@\n-pre-existing dirty content\n+fixture contents\n*** End Patch"}
                elif model_calls == 2:
                    item = {"type": "tool_search_call", "id": "search-item", "call_id": "search-call", "execution": "client", "arguments": {"query": "lazyai read_file show_locations setup_workstreams", "limit": 3}}
                elif model_calls in (3, 4, 5):
                    name, arguments = {
                        3: ("read_file", {"path": "probe.txt"}),
                        4: ("show_locations", {"locations": [{"path": "probe.txt", "line": 1, "text": "fixture location"}]}),
                        5: ("setup_workstreams", {"workstreams": [{"branch": "fixture", "nickname": "fixture"}]}),
                    }[model_calls]
                    item = {"type": "function_call", "id": f"tool_{model_calls}", "call_id": f"call_{model_calls}", "namespace": "mcp__lazyai", "name": name, "arguments": json.dumps(arguments)}
                elif model_calls == 6:
                    item = {"type": "function_call", "id": "question-item", "call_id": "question-call", "name": "request_user_input", "arguments": json.dumps({"questions": [{"id": "fixture", "header": "Fixture", "question": "Continue?", "options": [{"label": "Proceed", "description": "Complete the fixture"}, {"label": "Stop", "description": "Stop the fixture"}]}]})}
                else:
                    item = {"type": "message", "id": "message-item", "role": "assistant", "content": [{"type": "output_text", "text": "Compatibility fixture complete.", "annotations": []}]}
                wire = [("response.created", {"response": {"id": f"resp_{model_calls}", "status": "in_progress", "output": []}}),
                        ("response.output_item.done", {"output_index": 0, "item": item}),
                        ("response.completed", {"response": {"id": f"resp_{model_calls}", "status": "completed", "output": [item], "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})]
                for event, payload in wire:
                    self.wfile.write(f"event: {event}\ndata: {json.dumps(dict(type=event, **payload))}\n\n".encode())
                return
            if self.headers.get("Authorization") != "Bearer compatibility-test":
                self.send_error(401)
                return
            event = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if event.get("type") == "file.snapshot":
                snapshots[event["path"]] = pathlib.Path(event["path"]).read_text()
            events.put(event)
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"{}")

        def log_message(self, format, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    with tempfile.TemporaryDirectory(prefix="lazyai-codex-") as tmp:
        home = pathlib.Path(tmp) / "home"
        home.mkdir()
        root = pathlib.Path(tmp) / "project"
        root.mkdir()
        (root / "probe.txt").write_text("pre-existing dirty content\n")
        original_config = '[[hooks.SessionStart]]\n[[hooks.SessionStart.hooks]]\ntype="command"\ncommand="true"\n'
        (home / "config.toml").write_text(original_config)
        env = dict(os.environ, CODEX_HOME=str(home), LAZYAI_WORKTREE=str(root),
                   LAZYAI_HOOK_URL=f"http://127.0.0.1:{server.server_port}",
                   LAZYAI_HOOK_TOKEN="compatibility-test")
        command = "'" + binary.replace("'", "'\"'\"'") + "' __codex-hook"
        overrides = []
        for event in ("SessionStart", "SubagentStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop", "Interrupt", "SessionEnd"):
            overrides += ["-c", f"hooks.{event}=[{{hooks=[{{type=\"command\",command={json.dumps(command)},timeout=3}}]}}]"]
        mcp_env = ",".join(f"{k}={json.dumps(env[k])}" for k in ("LAZYAI_WORKTREE", "LAZYAI_HOOK_URL", "LAZYAI_HOOK_TOKEN"))
        overrides += ["-c", f'mcp_servers.lazyai={{command={json.dumps(binary)},args=["__mcp"],env={{{mcp_env}}},tool_timeout_sec=130,default_tools_approval_mode="approve"}}']
        overrides += ["-c", 'model_provider="lazyai_fixture"', "-c", 'model="gpt-5.4"',
                      "-c", "features.enable_request_compression=false",
                      "-c", "features.default_mode_request_user_input=true",
                      "-c", f'model_providers.lazyai_fixture={{name="Local compatibility fixture",base_url="http://127.0.0.1:{server.server_port}/v1",wire_api="responses"}}']

        def launch():
            proc = subprocess.Popen([args.codex, *overrides, "app-server"], env=env,
                                    cwd=root, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                    stderr=subprocess.PIPE, text=True, bufsize=1)
            assert proc.stdin is not None and proc.stdout is not None and proc.stderr is not None
            stdin, stdout, stderr_stream = proc.stdin, proc.stdout, proc.stderr
            messages = queue.Queue()
            errors = []

            def read():
                for line in stdout:
                    try:
                        message = json.loads(line)
                        if "id" in message and message.get("method", "").endswith("/requestApproval"):
                            # Approve only the known fixture patch in the disposable project.
                            assert message["method"] == "item/fileChange/requestApproval", message
                            stdin.write(json.dumps({"jsonrpc": "2.0", "id": message["id"], "result": {"decision": "accept"}}) + "\n")
                            stdin.flush()
                            continue
                        if "id" in message and message.get("method") == "item/tool/requestUserInput":
                            stdin.write(json.dumps({"jsonrpc": "2.0", "id": message["id"], "result": {"answers": {"fixture": {"answers": ["Proceed"]}}}}) + "\n")
                            stdin.flush()
                            continue
                        messages.put(message)
                        if "hook" in message.get("method", "").lower():
                            events.put({"diagnostic": message})
                    except json.JSONDecodeError:
                        errors.append(line)

            def stderr():
                for line in stderr_stream:
                    errors.append(line)

            threading.Thread(target=read, daemon=True).start()
            threading.Thread(target=stderr, daemon=True).start()
            seq = 0

            def request(method, params):
                nonlocal seq
                seq += 1
                stdin.write(json.dumps({"jsonrpc": "2.0", "id": seq, "method": method, "params": params}) + "\n")
                stdin.flush()
                deadline = time.monotonic() + 25
                while time.monotonic() < deadline:
                    try:
                        msg = messages.get(timeout=1)
                    except queue.Empty:
                        if proc.poll() is not None:
                            break
                        continue
                    if msg.get("id") == seq:
                        if "error" in msg:
                            raise RuntimeError(f"{method}: {msg['error']}")
                        return msg.get("result")
                raise RuntimeError(f"{method} timed out: {''.join(errors)[-4000:]}")

            request("initialize", {"clientInfo": {"name": "lazyai-compatibility", "version": "1"}, "capabilities": {"experimentalApi": True}})
            return proc, request

        proc, request = launch()
        try:
            listed = request("hooks/list", {"cwds": [str(root)]})
            hooks = listed["data"][0]["hooks"]
            assert len(hooks) == 10 and sum(h["command"] == command for h in hooks) == 9, listed
            assert not listed["data"][0]["warnings"], listed
            assert all(h["trustStatus"] == "untrusted" for h in hooks), listed
            print("PASS: all nine hooks discovered alongside an existing user hook; native trust required")
            # Trust only these exact test hooks in the disposable home. The real
            # user's hook state is never read, changed or bypassed.
            state = "\n".join(f'[hooks.state.{json.dumps(h["key"])}]\ntrusted_hash = {json.dumps(h["currentHash"])}\n' for h in hooks)
            (home / "config.toml").write_text(original_config + state)
            proc.terminate()
            proc.wait(timeout=5)
            proc, request = launch()
            trusted = request("hooks/list", {"cwds": [str(root)]})
            assert all(h["trustStatus"] == "trusted" for h in trusted["data"][0]["hooks"]), trusted
            started = request("thread/start", {"cwd": str(root), "ephemeral": True})
            thread_id = started["thread"]["id"]
            status = request("mcpServerStatus/list", {"threadId": thread_id})
            bridge = next(s for s in status["data"] if s["name"] == "lazyai")
            assert bridge["runtimeStatus"] == "connected", status
            assert set(bridge["tools"]) == {"show_locations", "setup_workstreams", "read_file"}, status
            request("turn/start", {"threadId": thread_id, "input": [{"type": "text", "text": "Run the local compatibility fixture."}]})
            observed = []
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                try:
                    event = events.get(timeout=1)
                    observed.append(event)
                    if any(e.get("type") == "idle" for e in observed):
                        break
                except queue.Empty:
                    pass
            assert any(e.get("component") == "tools" for e in observed), "MCP did not connect"
            assert any(e.get("component") == "hooks" for e in observed), f"SessionStart hook did not reach LazyAI: {observed}"
            for kind in ("tool.before", "file.snapshot", "file.write", "tool.after", "attention", "file.read", "show", "setup", "idle"):
                assert any(e.get("type") == kind for e in observed), f"missing {kind}: {observed}"
            assert (root / "probe.txt").read_text() == "fixture contents\n"
            assert snapshots[str(root / "probe.txt")] == "pre-existing dirty content\n", snapshots
            assert any(e.get("type") == "attention" and e.get("tool") == "request_user_input" for e in observed), "question attention was not reported"
            print("PASS: native hooks preserve the dirty pre-image, report approvals/questions/activity, and call all three MCP tools using an offline fixture")
        finally:
            assert proc.stdin is not None
            proc.stdin.close()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.terminate()
                proc.wait(timeout=5)
    server.shutdown()


if __name__ == "__main__":
    main()
