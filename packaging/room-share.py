#!/usr/bin/env python3
# packaging/room-share.py - give every agent the admin's rules, skills and
# chosen memories: the rooms (Claude Code, through ROOM), and Codex and
# Antigravity (AGENTS.md / GEMINI.md in their work folder).
#
# /etc/aiagent/share.conf, every line optional:
#   BASE=/etc/aiagent/room-base.md   rules written for the agents
#   RULES=/path/to/CLAUDE.md         a project's rules, appended
#   SKILLS=/root/.claude/skills      every skill here is shared
#   MEMORY=/path/to/memory           only files whose frontmatter has "share: rooms"
#   DUTIES=/etc/aiagent/room-duties.conf  each room's duty (room=frontend|backend|fullstack|tester|admin),
#                                    written by aiagent when the admin sets one
# A file that looks like it holds a secret is skipped and reported.
import os, re, shutil, sys
ROOM = "/usr/local/share/aiagent/room"
conf = {"BASE": "/etc/aiagent/room-base.md", "RULES": "", "SKILLS": os.path.expanduser("~/.claude/skills"), "MEMORY": "",
        "CODE": "", "WORK": os.path.expanduser("~/ai-work"), "DUTIES": "/etc/aiagent/room-duties.conf"}
for f in ("/etc/aiagent/agents.conf", "/etc/aiagent/share.conf"):
    try:
        for line in open(f):
            k, eq, v = line.strip().partition("=")
            if eq and k in conf: conf[k] = v
    except OSError: pass
MEM = conf["MEMORY"]
SECRET = re.compile(r"glpat-[\w.-]{16}|gh[pousr]_\w{20}|github_pat_\w{20}|sk-[\w-]{24}|kaw_[\w-]{20}|AKIA[0-9A-Z]{16}|xox[abprs]-[\w-]{10}|eyJ[\w-]{10,}\.[\w-]{10,}\.|PRIVATE KEY-----\s*\n[A-Za-z0-9+/=]{20}|://[^/\s:@]+:[^/\s@<$]{6,}@")
# In code a variable named token is normal; in prose and settings it is a value.
PROSE = re.compile(r"(?i:password|passwd|secret|api[_-]?key|token)\s*[:=]\s*[\"']?(?=[^\s\"']*\d)[A-Za-z0-9+/_.-]{12,}")
bad = []
def ok(path):
    try:
        t = open(path, encoding="utf-8").read()
    except (UnicodeDecodeError, OSError):
        return True
    if SECRET.search(t) or (not path.endswith(('.py', '.js', '.ts', '.mjs', '.html', '.css', '.sh')) and PROSE.search(t)):
        bad.append(path); return False
    return True
def put(path, text):
    try:
        if open(path).read() == text: return
    except OSError: pass
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path + ".tmp", "w") as f: f.write(text)
    os.chmod(path + ".tmp", 0o644); os.replace(path + ".tmp", path)
src = conf["SKILLS"]; os.makedirs(ROOM + "/skills", exist_ok=True); names = set()
for n in sorted(os.listdir(src)) if os.path.isdir(src) else []:
    d = os.path.realpath(os.path.join(src, n))
    if not re.match(r"^[a-z0-9-]+$", n) or not os.path.isfile(d + "/SKILL.md"): continue
    files = [os.path.join(p, f) for p, _, fs in os.walk(d) for f in fs]
    files = [f for f in files if ok(f)]
    if d + '/SKILL.md' not in files: continue
    names.add(n); dst = ROOM + "/skills/" + n; want = set()
    for f in files:
        if os.path.islink(f) or "/.git/" in f: continue
        rel = os.path.relpath(f, d); want.add(rel); t = dst + "/" + rel
        if not os.path.exists(t) or open(t, "rb").read() != open(f, "rb").read():
            os.makedirs(os.path.dirname(t), exist_ok=True); shutil.copyfile(f, t)
        os.chmod(t, 0o755 if os.access(f, os.X_OK) and os.stat(f).st_mode & 0o111 else 0o644)
    for p, _, fs in os.walk(dst):
        os.chmod(p, 0o755)
        for f in fs:
            if os.path.relpath(os.path.join(p, f), dst) not in want: os.remove(os.path.join(p, f))
if os.path.isdir(src):
    for n in os.listdir(ROOM + "/skills"):
        if n not in names: shutil.rmtree(ROOM + "/skills/" + n)
idx = []; keep = set()
for f in sorted(os.listdir(MEM)) if os.path.isdir(MEM) else []:
    p = MEM + "/" + f
    if not f.endswith(".md") or f == "MEMORY.md" or os.path.islink(p): continue
    t = open(p).read(); head = t.split("\n---", 1)[0]
    if not re.search(r"^\s*share: rooms\s*$", head, re.M) or not ok(p): continue
    keep.add(f); put(ROOM + "/memory/" + f, t)
    m = re.search(r"^description: (.*)$", head, re.M)
    idx.append("- `%s/memory/%s`: %s" % (ROOM, f, m.group(1) if m else ""))
if os.path.isdir(ROOM + "/memory"):
    os.chmod(ROOM + "/memory", 0o755)
    for f in os.listdir(ROOM + "/memory"):
        if f not in keep: os.remove(ROOM + "/memory/" + f)
out = ""
try: out = open(conf["BASE"]).read()
except OSError: pass
if conf["RULES"] and os.path.isfile(conf["RULES"]) and ok(conf["RULES"]):
    out += "\n\n# Project rules (from " + conf["RULES"] + ")\n\n" + open(conf["RULES"]).read()
duties = []
try:
    for line in open(conf["DUTIES"]):
        k, eq, v = line.strip().partition("=")
        if eq and re.match(r"^[a-z][a-z0-9-]{1,30}$", k.strip()) and v.strip() in ("frontend", "backend", "fullstack", "tester", "admin"):
            duties.append((k.strip(), v.strip()))
except OSError: pass
if duties:
    label = {"frontend": "frontend (dev env: run the dev server behind dev/<room>)",
             "backend": "backend (has a preview too: run its own dev server behind dev/<room> showing Swagger/OpenAPI or a small page of its own; plus tests)",
             "fullstack": "fullstack (frontend + backend + the services they use, on dev/UAT; dev server behind dev/<room>)",
             "tester": "tester (runs no app; tests on the shared UAT)",
             "admin": "admin (its sessions are on the guard's admin list)"}
    out += ("\n\n# Room duties (set by the admin in aiagent; your room is `whoami`)\n\n| room | duty |\n|---|---|\n" +
            "".join("| %s | %s |\n" % (k, label[v]) for k, v in sorted(duties)) +
            "\nEvery duty: merge requests go through the admin.\n" +
            "\nA room not listed: ask the user what it is for before choosing how to work.\n")
# Environments: names and tiers only, from aiagent room-env-sync (docs/ENVIRONMENTS.md).
# The values are only in each room's own files, never in this text.
envs = []
try:
    for line in open("/etc/aiagent/room-environments.conf"):
        k, eq, v = line.strip().partition("=")
        if eq and re.match(r"^[a-z0-9][a-z0-9-]{0,39}$", k) and v in ("dev", "uat", "prod"):
            envs.append((k, v))
except OSError: pass
if envs:
    where = {"dev": "`~/.config/aiagent/env/%s.env`", "uat": "`~/.config/aiagent/env/%s.env`", "prod": "never in a room: prod work goes through the admin (a merge request)"}
    out += ("\n\n# Environments (set by the admin in aiagent)\n\n| environment | tier | credentials |\n|---|---|---|\n" +
            "".join("| %s | %s | %s |\n" % (k, v, where[v].replace("%s", k)) for k, v in sorted(envs)) +
            "\nOnly rooms whose duty is frontend, backend, fullstack or admin get the dev and uat files; a tester room gets none. "
            "Load one for a single command: `set -a; . ~/.config/aiagent/env/NAME.env; set +a; <command>`. "
            "Never print, echo, log, commit or paste a value, and never copy the file into the work folder, a review page or a message.\n")
if idx: out += "\n\n# Shared memory from the admin (read a file when it is relevant)\n\n" + "\n".join(idx) + "\n"
put(ROOM + "/CLAUDE.md", out)
# Codex and Antigravity read AGENTS.md (and GEMINI.md) in their work folder.
# They see the room folder read-only, so skills and memory are named by path.
work = conf["WORK"]
sk = []
for n in sorted(os.listdir(ROOM + "/skills")):
    try: head = open(ROOM + "/skills/" + n + "/SKILL.md").read().split("\n---", 1)[0]
    except OSError: continue
    m = re.search(r"^description: (.*)$", head, re.M)
    sk.append("- `%s/skills/%s/SKILL.md`: %s" % (ROOM, n, (m.group(1) if m else "")[:300]))
for cli in ("codex", "agy"):
    d = work + "/" + cli
    if not os.path.isdir(d): continue
    text = ("# Rules for this agent (generated from the admin's; do not edit)\n\n"
            "Work in this folder only." + (" The code in " + conf["CODE"] + " is read-only: copy what you change here first." if conf["CODE"] else "") +
            " Shell commands have no network. Hand work back as a summary and the paths of the files.\n\n"
            "# Skills (read the file when it fits the task, then follow it)\n\n" + "\n".join(sk) + "\n\n" + out)
    for name in ("AGENTS.md", "GEMINI.md") if cli == "agy" else ("AGENTS.md",):
        put(d + "/" + name, text)
for p in bad: print("skipped, looks like a secret:", p, file=sys.stderr)
