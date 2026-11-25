#!/usr/bin/env python3
# packaging/preview-routes.py [--print | --remove] - one Gateway API rule per
# room, so that HOST/dev/<room> and below reach the preview listener.
#
# The routes have no hostname of their own and match the Host header instead:
# on a gateway whose other routes are hostless, a route that names a host takes
# every request for it and hides them. /dev/<room> is longer than a /dev
# catch-all, so it wins for that room only.
#
# Reads /etc/aiagent/preview.conf; does nothing unless HOST, GATEWAY,
# GATEWAY_NAMESPACE, NAMESPACE, SERVICE and PORT are all set.
import grp, json, os, re, subprocess, sys
CONF = os.environ.get("AIAGENT_PREVIEW_CONF", "/etc/aiagent/preview.conf")
KUBECTL = os.environ.get("KUBECTL", "kubectl")
LABEL = "app.kubernetes.io/managed-by=aiagent-preview"
# keep in step with reservedRooms in internal/world/preview.go
RESERVED = {"ci", "cd", "api", "kapi", "events", "gateway", "dev", "admin", "assets", "static", "ws", "login", "health"}
ROOM = re.compile(r"[a-z][a-z0-9-]{1,30}")
PER_ROUTE = 16  # the API's limit of rules in one route
K8S = re.compile(r"[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?")


def load(path):
    conf = {}
    with open(path) as f:
        for line in f:
            k, eq, v = line.strip().partition("=")
            if eq and not k.startswith("#"):
                conf[k.strip()] = v.strip()
    return conf


def rooms(reserved):
    if os.environ.get("AIAGENT_ROOMS") is not None:  # for --print and tests
        names = os.environ["AIAGENT_ROOMS"].split()
    else:
        try:
            names = grp.getgrnam("aiagent-users").gr_mem
        except KeyError:
            names = []
    return sorted(n for n in names if ROOM.fullmatch(n) and n not in reserved)


def render(conf, names):
    items = []
    for i in range(0, len(names), PER_ROUTE):
        parent = {"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": conf["GATEWAY"], "namespace": conf["GATEWAY_NAMESPACE"]}
        if conf.get("SECTION"):
            parent["sectionName"] = conf["SECTION"]
        items.append({
            "apiVersion": "gateway.networking.k8s.io/v1", "kind": "HTTPRoute",
            "metadata": {"name": "aiagent-preview-%d" % (i // PER_ROUTE), "namespace": conf["NAMESPACE"], "labels": dict([LABEL.split("=")])},
            "spec": {"parentRefs": [parent], "rules": [{
                # a path-element prefix: /dev/room and /dev/room/..., not /dev/room2
                "matches": [{"path": {"type": "PathPrefix", "value": "/dev/" + n},
                             "headers": [{"name": "Host", "type": "Exact", "value": conf["HOST"].lower()}]}],
                "backendRefs": [{"name": conf["SERVICE"], "port": int(conf["PORT"])}],
            } for n in names[i:i + PER_ROUTE]]}})
    return items


def kubectl(*args, data=None):
    return subprocess.run([KUBECTL, *args], input=data, capture_output=True, text=True, timeout=60, check=True).stdout


def main():
    try:
        conf = load(CONF)
    except OSError:
        return 0
    need = ("HOST", "GATEWAY", "GATEWAY_NAMESPACE", "NAMESPACE", "SERVICE", "PORT")
    if any(not conf.get(k) for k in need):
        return 0
    for k in need[:-1]:
        if not K8S.fullmatch(conf[k].lower() if k == "HOST" else conf[k]):
            print("bad " + k, file=sys.stderr)
            return 2
    if not conf["PORT"].isdigit():
        print("bad PORT", file=sys.stderr)
        return 2
    reserved = RESERVED | set(conf.get("RESERVED", "").replace(",", " ").lower().split())
    items = [] if "--remove" in sys.argv else render(conf, rooms(reserved))
    if "--print" in sys.argv:
        print(json.dumps({"apiVersion": "v1", "kind": "List", "items": items}, indent=2))
        return 0
    ns = conf["NAMESPACE"]
    try:
        if items:
            kubectl("apply", "-f", "-", data=json.dumps({"apiVersion": "v1", "kind": "List", "items": items}))
        have = kubectl("get", "httproute", "-n", ns, "-l", LABEL, "-o", "name").split()
        keep = {"httproute.gateway.networking.k8s.io/" + i["metadata"]["name"] for i in items}
        for old in have:
            if old not in keep:
                kubectl("delete", "-n", ns, old)
    except subprocess.CalledProcessError as e:
        print(e.stderr.strip()[-400:], file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
