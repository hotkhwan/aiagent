#!/usr/bin/env python3
# packaging/ai-quota.py - read each plan's usage with the CLI's own /usage, which
# spends no tokens, and save only the numbers where the service looks for them.
# A CLI that is absent, signed out or answers differently changes nothing.
import datetime as dt, json, os, re, shutil, subprocess

HOME = os.path.expanduser("~")
UTC = dt.timezone.utc


def write(path, d):
    if not os.path.isdir(os.path.dirname(path)):
        return
    fd = os.open(path + ".tmp", os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump(d, f)
    os.replace(path + ".tmp", path)


def ask(argv):
    try:
        r = subprocess.run(argv, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=90, cwd=HOME)
        return json.loads(r.stdout)
    except (OSError, ValueError, subprocess.TimeoutExpired):
        return {}


def stamp(t):
    return t.astimezone(UTC).strftime("%Y-%m-%dT%H:%M:%SZ")


def reset(text, now):
    m = re.search(r"resets (\w{3}) (\d+)(?:, | at )(\d+)(?::(\d+))?(am|pm) \(UTC\)", text)
    if not m:
        return None
    hour = int(m[3]) % 12 + (12 if m[5] == "pm" else 0)
    mon = "JanFebMarAprMayJunJulAugSepOctNovDec".index(m[1]) // 3 + 1
    t = dt.datetime(now.year, mon, int(m[2]), hour, int(m[4] or 0), tzinfo=UTC)
    if t < now - dt.timedelta(days=30):
        t = t.replace(year=now.year + 1)
    return stamp(t)


def claude(now):
    out = ask(["claude", "-p", "/usage", "--output-format", "json", "--no-session-persistence"])
    text, u = str(out.get("result", "")), {"observedAt": stamp(now)}
    for key, label in (("fiveHour", r"Current session"), ("sevenDay", r"Current week \(all models\)")):
        m = re.search(label + r": (\d+(?:\.\d+)?)% used.*", text)
        if m:
            u[key] = {"usedPercent": float(m[1])}
            at = reset(m[0], now)
            if at:
                u[key]["resetsAt"] = at
    if len(u) > 1:
        write(HOME + "/.claude/aiagent-usage.json", u)


def agy(now):
    box = "/usr/local/lib/aiagent/agent-box.sh"
    if not os.path.exists(HOME + "/.gemini/antigravity-cli/antigravity-oauth-token") or not os.path.exists(box):
        return
    out, u = ask([box, "agy", "usage"]), {"observedAt": stamp(now)}
    for g in ((out.get("command") or {}).get("data") or {}).get("groups", []):
        for b in g.get("buckets", []):
            key = {"gemini-5h": "fiveHour", "gemini-weekly": "sevenDay"}.get(b.get("id"))
            if key and isinstance(b.get("remaining_fraction"), (int, float)):
                u[key] = {"usedPercent": max(0.0, min(100.0, 100 * (1 - b["remaining_fraction"])))}
                if not str(b.get("reset_time", "1970")).startswith("1970"):
                    u[key]["resetsAt"] = b["reset_time"]
    if len(u) > 1:
        write(HOME + "/.gemini/aiagent-usage.json", u)


now = dt.datetime.now(UTC)
if shutil.which("claude"):
    claude(now)
if shutil.which("agy"):
    agy(now)
