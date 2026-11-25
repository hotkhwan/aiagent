#!/usr/bin/env python3
# packaging/host-menu.py - the first window of the shared tmux session: which
# agents exist, by the names the web shows, and a number to open each.
import glob, json, os, pwd, re, subprocess, time

ID = re.compile(r"^[0-9a-f]{8}-[0-9a-f-]{27}$")
USER = re.compile(r"^[a-z][a-z0-9_-]{0,31}$")


def clean(s):
    return "".join(c for c in str(s or "") if c.isprintable())[:40]


def sessions():
    rows = []
    for f in glob.glob(os.path.expanduser("~/.claude/sessions/*.json")):
        try:
            d = json.load(open(f))
            os.kill(int(d["pid"]), 0)
            sid = str(d["sessionId"])
        except (OSError, ValueError, KeyError, TypeError):
            continue
        if not ID.match(sid):
            continue
        if d.get("parkedJobId") and not glob.glob(os.path.expanduser("~/.claude/projects/*/" + sid + ".jsonl")):
            continue  # a terminal showing the agent view, not an agent
        name = clean(d.get("name")) or sid[:8]
        if d.get("kind") == "bg":
            rows.append((name, "background", ["claude", "attach", sid[:8]]))
        else:
            rows.append((name, "open in its own terminal", None))
    if os.getuid() == 0:
        for d in sorted(glob.glob("/run/aiagent-tmux/*/")):
            u = os.path.basename(d.rstrip("/"))
            try:
                if not USER.match(u) or pwd.getpwnam(u).pw_uid < 700000:
                    continue
            except KeyError:
                continue
            # As the user, never as root: a tmux server can make its client
            # run a command.
            rows.append((u, "room", ["runuser", "-u", u, "--pty", "--", "env", "-i", "TERM=screen-256color",
                         "LANG=C.UTF-8", "PATH=/usr/bin:/bin", "TMUX_TMPDIR=/run/aiagent-tmux/" + u,
                         "tmux", "attach", "-t", "core"]))
    return rows


while True:
    try:
        rows = sessions()
        print("\033[2J\033[HAI Agent sessions on " + os.uname().nodename + "\n")
        for i, (name, kind, cmd) in enumerate(rows, 1):
            print("  %2s  %-28s %s" % (i if cmd else "-", name, kind))
        print("\nNumber + Enter opens one; Enter alone refreshes.")
        print("Leave a background session: left arrow or Ctrl-Z. Leave a room: Ctrl-b Ctrl-b d. Leave this tmux: Ctrl-b d.")
        a = input("> ").strip()
        if a.isdigit() and 0 < int(a) <= len(rows) and rows[int(a) - 1][2]:
            subprocess.run(rows[int(a) - 1][2])
    except (EOFError, KeyboardInterrupt, OSError):
        time.sleep(1)
