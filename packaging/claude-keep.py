#!/usr/bin/env python3
# packaging/claude-keep.py save|restore - remember which Claude background
# sessions run, and start the same ones again after the machine restarted.
# A conversation is on disk anyway; this only reopens it, under its own id.
import glob, json, os, re, subprocess, sys

HOME = os.path.expanduser("~")
LIST = HOME + "/.local/state/aiagent/claude-sessions.json"
ID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")


def boot():
    return open("/proc/sys/kernel/random/boot_id").read().strip()


def live():
    out = {}
    for f in glob.glob(HOME + "/.claude/sessions/*.json"):
        try:
            d = json.load(open(f))
            pid, sid = int(d["pid"]), str(d["sessionId"])
            os.kill(pid, 0)
            start = open("/proc/%d/stat" % pid).read().rsplit(")", 1)[1].split()[19]
        except (OSError, ValueError, KeyError, TypeError, IndexError):
            continue
        # a pid reused after a restart is some other process
        if str(d.get("procStart")) != start or not ID.match(sid):
            continue
        out[sid] = d
    return out


def load():
    try:
        return json.load(open(LIST))
    except (OSError, ValueError):
        return {}


def write(d):
    os.makedirs(os.path.dirname(LIST), exist_ok=True)
    with open(LIST + ".tmp", "w") as f:
        json.dump(d, f)
    os.replace(LIST + ".tmp", LIST)


def save():
    old = load()
    if old and old.get("boot") != boot():
        return  # restore has not run yet: do not forget what ran before
    run = [{"id": s, "cwd": d.get("cwd", "")} for s, d in sorted(live().items())
           if d.get("kind") == "bg" and glob.glob(HOME + "/.claude/projects/*/" + s + ".jsonl")]
    write({"boot": boot(), "sessions": run})


def restore():
    old = load()
    if not old or old.get("boot") == boot():
        return
    now, failed = live(), []
    for s in old.get("sessions", []):
        sid, cwd = str(s.get("id")), str(s.get("cwd"))
        if not ID.match(sid) or sid in now:
            continue
        r = subprocess.run(["claude", "--bg", "--resume", sid], cwd=cwd if os.path.isdir(cwd) else HOME,
                           stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=120)
        ok = r.returncode == 0 and ("backgrounded · " + sid[:8]) in r.stdout
        print(sid[:8], "restored" if ok else "NOT restored: " + (r.stdout + r.stderr).strip()[-200:])
        if not ok:
            failed.append(s)
    write({"boot": boot(), "sessions": old.get("sessions", [])})
    sys.exit(1 if failed else 0)


{"save": save, "restore": restore}[sys.argv[1]]()
