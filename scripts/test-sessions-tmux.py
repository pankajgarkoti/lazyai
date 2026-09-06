#!/usr/bin/env python3
"""Drive LazyAI in an isolated tmux server and disposable projects.

Requires tmux 3.7+ (literal paste support), git, Python 3 and a built LazyAI.
--real-opencode additionally exercises the installed OpenCode without submitting
an agent request. Artifacts are retained in the printed temporary directory.
"""

import argparse, os, subprocess, pathlib, tempfile, time, json, shlex, signal, sqlite3

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--binary", default="./bin/lazyai")
parser.add_argument("--real-opencode", action="store_true")
options = parser.parse_args()
base = pathlib.Path(tempfile.mkdtemp(prefix="lazyai-drive-", dir="/private/tmp"))
print("ARTIFACTS", base, flush=True)
binary = str(pathlib.Path(options.binary).resolve())
if not os.access(binary, os.X_OK):
    parser.error("build LazyAI first: make build")
tmux = ["tmux", "-f", "/dev/null", "-L", "lazyai-drive-" + str(os.getpid())]
print("TMUX_SOCKET", tmux[-1], flush=True)
env = os.environ.copy()
env.update(
    LAZYAI_RUNTIME_DIR=str(base / "run"),
    LAZYAI_DB=str(base / "db"),
    DRIVE_DATA=str(base),
)


def cmd(args, **kw):
    return subprocess.check_output(
        args, text=True, stderr=subprocess.STDOUT, **kw
    ).strip()


def tm(*args):
    return cmd(tmux + list(args))


def wait(fn, label, timeout=15):
    end = time.time() + timeout
    while time.time() < end:
        try:
            value = fn()
            if value:
                return value
        except (OSError, subprocess.CalledProcessError, sqlite3.OperationalError):
            pass
        time.sleep(0.1)
    raise AssertionError("timeout: " + label)


def check(ok, label):
    assert ok, label
    print("PASS", label, flush=True)


repo = base / "repo"
(repo / "src" / "pkg").mkdir(parents=True)
cmd(["git", "init", "-q", "-b", "main", str(repo)])
cmd(
    [
        "git",
        "-C",
        str(repo),
        "-c",
        "user.name=test",
        "-c",
        "user.email=test@example.com",
        "commit",
        "--allow-empty",
        "-qm",
        "init",
    ]
)
helper = base / "fake-opencode"
helper.write_text("""#!/usr/bin/env python3
import os,sys,time,signal,subprocess,tty,select,json,pathlib
base=pathlib.Path(os.environ['DRIVE_DATA'])
key=pathlib.Path(os.getcwd()).name
worker=subprocess.Popen([sys.executable,'-c',"import signal,time; signal.signal(signal.SIGHUP,signal.SIG_IGN); signal.signal(signal.SIGTERM,signal.SIG_IGN); time.sleep(600)"],start_new_session=True)
(base/(key+'.pids')).write_text(json.dumps([os.getpid(),worker.pid]))
tty.setraw(0)
sys.stdout.write('\\x1b[?1000h\\x1b[?1006h\\x1b[?2004hREADY '+key+'\\r\\n');sys.stdout.flush()
count=0
with (base/(key+'.input')).open('ab',buffering=0) as log:
 while True:
  ready,_,_=select.select([0],[],[],.2)
  if ready:
   data=os.read(0,4096)
   if not data:break
   log.write(data)
  count+=1
  (base/(key+'.tick')).write_text(str(count))
  sys.stdout.write('\\x1b[2;1HTICK '+str(count)+'\\x1b[K');sys.stdout.flush()
""")
helper.chmod(0o755)
settings = " ".join(
    shlex.quote(k + "=" + env[k])
    for k in ["LAZYAI_RUNTIME_DIR", "LAZYAI_DB", "DRIVE_DATA"]
)


def start(name, root, extra=(), real=False):
    args = [binary, "--dir", str(root)]
    if not real:
        args += ["--opencode", str(helper)]
    args += list(extra)
    launch = (
        "env "
        + settings
        + " "
        + shlex.join(args)
        + '; printf "\\nCLIENT_EXIT\\n"; sleep 600'
    )
    tm("new-session", "-d", "-s", name, "-x", "110", "-y", "32", launch)
    return name + ":0.0"


def screen(p):
    return tm("capture-pane", "-p", "-t", p)


def modes(p):
    return tm(
        "display-message",
        "-p",
        "-t",
        p,
        "#{mouse_any_flag} #{mouse_sgr_flag} #{bracket_paste_flag} #{alternate_on}",
    )


def sessions():
    with sqlite3.connect(base / "db") as db:
        return db.execute("select project,pid,status from runtime_sessions").fetchall()


def alive(pid):
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False


p = None
try:
    p = start("first", repo / "src" / "pkg")
    wait(lambda: "READY" in screen(p), "first screen")
    check(
        modes(p) == "1 1 1 1",
        "client enables mouse, SGR, bracketed paste and alternate screen",
    )
    rows = sessions()
    superpid = rows[0][1]
    check(rows[0][0] == str(repo), "nested launch uses canonical repository identity")
    pids = json.loads((base / "pkg.pids").read_text())
    tm("send-keys", "-t", p, "-l", "typed")
    wait(lambda: b"typed" in (base / "pkg.input").read_bytes(), "typed input")
    payload = "line one\nline two jk\x11 END"
    paste = base / "paste"
    paste.write_bytes(payload.encode())
    tm("load-buffer", str(paste))
    tm("paste-buffer", "-p", "-r", "-S", "-t", p)
    wait(
        lambda: payload.encode() in (base / "pkg.input").read_bytes(), "paste contents"
    )
    data = (base / "pkg.input").read_bytes()
    check(
        b"\x1b[200~" + payload.encode() + b"\x1b[201~" in data,
        "multiline paste including Ctrl+Q remains framed and literal",
    )
    # Send an SGR click into the content pane; app translates coordinates for helper.
    mouse = b"\x1b[<0;60;8M"
    tm("send-keys", "-t", p, "-H", *[f"{b:02x}" for b in mouse])
    wait(
        lambda: b"\x1b[<0;" in (base / "pkg.input").read_bytes(),
        "mouse event reaches child",
    )
    check(True, "mouse click routed through app to child")
    tm("resize-window", "-t", "first", "-x", "130", "-y", "40")
    wait(lambda: len(screen(p).splitlines()) == 40, "resize")
    check(True, "attached terminal resizes")
    tm("send-keys", "-t", p, "C-q")
    wait(lambda: "CLIENT_EXIT" in screen(p), "detach")
    check(modes(p) == "0 0 0 0", "detach restores terminal modes")
    before = int((base / "pkg.tick").read_text())
    time.sleep(0.6)
    check(
        all(alive(pid) for pid in pids)
        and alive(superpid)
        and int((base / "pkg.tick").read_text()) > before,
        "same supervisor/child/worker remain alive and progress detached",
    )
    second = start("second", repo, ("--worktree", "unused"))
    wait(lambda: "READY" in screen(second), "reattach")
    check(
        not (repo / ".worktrees").exists(),
        "reattach with worktree flag makes no unused worktree",
    )
    check(
        sessions()[0][1] == superpid
        and json.loads((base / "pkg.pids").read_text()) == pids,
        "reattach preserves process identities",
    )
    third = start("third", repo / "src")
    wait(lambda: "READY" in screen(third), "takeover")
    wait(lambda: "CLIENT_EXIT" in screen(second), "old client takeover exit")
    check(
        modes(second) == "0 0 0 0" and modes(third) == "1 1 1 1",
        "takeover restores old terminal and enables new terminal modes",
    )
    # Kill the attached foreground client with TERM; supervisor must survive.
    parent = int(tm("display-message", "-p", "-t", third, "#{pane_pid}"))
    processes = cmd(["/bin/ps", "-axo", "pid=,ppid=,command="]).splitlines()
    client = [
        int(line.split(None, 2)[0])
        for line in processes
        if len(line.split(None, 2)) == 3
        and line.split(None, 2)[1] == str(parent)
        and binary in line
    ][0]
    os.kill(client, signal.SIGTERM)
    wait(lambda: "CLIENT_EXIT" in screen(third), "signal detach")
    check(
        modes(third) == "0 0 0 0" and all(alive(pid) for pid in pids),
        "SIGTERM restores client terminal and preserves work",
    )
    result = cmd([binary, "stop", "--dir", str(repo / "src")], env=env)
    wait(lambda: all(not alive(pid) for pid in pids), "all descendants stopped")
    check(
        "stopped" in result and sessions()[0][2] == "stopped",
        "stop from subdirectory terminates nested session workers",
    )
    # Fresh launch applies worktree options and close uses the same cleanup path.
    fourth = start("fourth", repo, ("--worktree", "fresh"))
    wait(lambda: "READY" in screen(fourth), "new worktree screen")
    check(
        (repo / ".worktrees" / "fresh").exists(), "fresh launch applies worktree option"
    )
    newpids = json.loads((base / "fresh.pids").read_text())
    tm("send-keys", "-t", fourth, "Escape")
    time.sleep(0.2)
    tm("send-keys", "-t", fourth, "w")
    time.sleep(0.2)
    tm("send-keys", "-t", fourth, "-l", "auxiliary")
    tm("send-keys", "-t", fourth, "Enter")
    wait(lambda: "branch off" in screen(fourth), "new workstream base prompt")
    tm("send-keys", "-t", fourth, "m")
    wait(lambda: (base / "auxiliary.pids").exists(), "second workstream startup")
    auxiliary = json.loads((base / "auxiliary.pids").read_text())
    tm("send-keys", "-t", fourth, "Escape")
    time.sleep(0.2)
    tm("send-keys", "-t", fourth, "a")
    wait(lambda: all(not alive(pid) for pid in auxiliary), "archive worker cleanup")
    check(
        all(alive(pid) for pid in newpids),
        "archive stops selected workers and preserves sibling workstream",
    )
    tm("send-keys", "-t", fourth, "Escape")
    time.sleep(0.2)
    tm("send-keys", "-t", fourth, "x")
    time.sleep(0.2)
    tm("send-keys", "-t", fourth, "x")
    wait(lambda: "CLIENT_EXIT" in screen(fourth), "close last workstream")
    wait(lambda: all(not alive(pid) for pid in newpids), "close descendant cleanup")
    check(True, "closing final workstream kills nested workers")
    if options.real_opencode:
        # Real OpenCode startup uses the same binary, actual terminal and local config.
        realrepo = base / "real"
        realrepo.mkdir()
        cmd(["git", "init", "-q", "-b", "main", str(realrepo)])
        real = start("real", realrepo, real=True)
        wait(
            lambda: any(
                w in screen(real)
                for w in (
                    "Ask anything",
                    "What do you want",
                    "Connect provider",
                    "Build",
                )
            ),
            "real OpenCode prompt",
            timeout=60,
        )
        tm("send-keys", "-t", real, "-l", "detach-attach smoke draft")
        wait(
            lambda: "detach-attach smoke draft" in screen(real),
            "real OpenCode typed draft",
        )
        (base / "real-opencode-screen.txt").write_text(screen(real))
        realpid = [r[1] for r in sessions() if r[0] == str(realrepo)][0]
        tm("send-keys", "-t", real, "C-q")
        wait(lambda: "CLIENT_EXIT" in screen(real), "real OpenCode detach")
        real2 = start("real2", realrepo, real=True)
        wait(
            lambda: "detach-attach smoke draft" in screen(real2),
            "real OpenCode draft restored",
            timeout=30,
        )
        check(
            [r[1] for r in sessions() if r[0] == str(realrepo)][0] == realpid,
            "real OpenCode renders, accepts a draft, detaches and restores it on the same supervisor",
        )
        cmd([binary, "stop", "--dir", str(realrepo)], env=env)
    print("ALL TMUX CHECKS PASSED", flush=True)
finally:
    try:
        for pane in tm(
            "list-panes", "-a", "-F", "#{session_name}:#{window_index}.#{pane_index}"
        ).splitlines():
            (base / (pane.replace(":", "-") + ".screen")).write_text(screen(pane))
    except Exception:
        pass
    if (base / "db").exists():
        for project, _, status in sessions():
            if status == "running":
                try:
                    cmd([binary, "stop", "--dir", project], env=env)
                except Exception as e:
                    print("cleanup:", e, flush=True)
    for f in base.glob("*.pids"):
        for pid in json.loads(f.read_text()):
            if alive(pid):
                try:
                    os.kill(pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
    try:
        tm("kill-server")
    except subprocess.CalledProcessError:
        pass
    print("ARTIFACTS", base, flush=True)
