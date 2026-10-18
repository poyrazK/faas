#!/usr/bin/env python3
"""Per-role content hashes of the host roles a bootstrap play runs
(docs/adr/20261018133100-host-configuration-lane.md).

A role's content is everything the repository decides about what Ansible
does when it runs the role on a host:

  * the role's own files under deploy/ansible/roles/<role>/ (tasks,
    handlers, defaults, vars, meta, templates, files), except
    documentation (*.md) and tests;
  * every repository path the role's task, handler, variable and meta
    files name outside the role: relative `src:` and `include_tasks:`
    paths (`../../_shared/templates/x.j2`), and `{{ role_path }}/...` and
    `{{ playbook_dir }}/...` paths, resolved as Ansible resolves them. A
    directory counts as every file under it;
  * the content of every role it includes (include_role, import_role,
    meta dependencies);
  * its entries in the group's plays (the `- role:` item with its `when:`,
    `tags:` and vars), without comments.

What every role of a group shares are the group's globals: the bodies of
the bootstrap.yml plays that target the group, without their roles:
lists; ansible.cfg; requirements.yml (collection pins); the group's
converge playbook; group_vars and host_vars. A globals change changes
every role. The rendered inventory and the operator variables are not in
the repository: `gregalectl deploy converge-control-plane` hashes those
(ADR-580).

A hash is sha256 over a preamble, the role name, its entries and every
input's path and bytes in path order. It depends on content only, so two
commits with the same role content give the same hash.

Usage:
  host_roles.py hashes  [--group G] [--commit REV | --root DIR]
  host_roles.py changed [--group G] --to REV (--from REV | --record FILE)
  host_roles.py owners  [--group G] [--commit REV | --root DIR]
  host_roles.py check   [--root DIR]     every role reference resolves
"""
import argparse
import hashlib
import json
import os
import posixpath
import re
import subprocess
import sys
import weakref

FORMAT = 1
ANSIBLE = "deploy/ansible"
ROLES = ANSIBLE + "/roles"
BOOTSTRAP = ANSIBLE + "/bootstrap.yml"
GROUPS = ("control_plane", "compute_nodes")
# The playbook each group converges through, on top of bootstrap.yml.
CONVERGE_PLAYBOOK = {
    "control_plane": ANSIBLE + "/control_plane_converge.yml",
    "compute_nodes": ANSIBLE + "/node_join.yml",
}
COMMON_FILES = (ANSIBLE + "/ansible.cfg", ANSIBLE + "/requirements.yml")
# Role subdirectories whose YAML Ansible executes or loads; files/ and
# templates/ are data a task names, scanned for nothing.
SCANNED_DIRS = ("tasks", "handlers", "defaults", "vars", "meta")
# Where Ansible looks up a relative path a role names.
LOOKUP_DIRS = ("files", "templates", "tasks", "handlers", "vars", "defaults")
NOT_CONTENT = re.compile(r"(^|/)(tests|molecule)/|\.md$")
REFERENCES = (
    ("role", re.compile(r"\{\{\s*role_path\s*\}\}/([^\s\"'{}]*)")),
    ("playbook", re.compile(r"\{\{\s*playbook_dir\s*\}\}/([^\s\"'{}]*)")),
    ("relative", re.compile(r"(?<![\w/.{}-])((?:\.\./)+[^\s\"'{}]*)")),
)
ROLE_INCLUDE = re.compile(r"(?:include_role|import_role):\s*\n?\s*name:\s*[\"']?([\w.-]+)")


def git(*args, root="."):
    out = subprocess.run(["git", *args], capture_output=True, cwd=root)
    if out.returncode != 0:
        raise RuntimeError(f"git {' '.join(args)}: {out.stderr.decode(errors='replace').strip()}")
    return out.stdout


class Tree:
    """Repository files, read lazily: a commit (git_tree), a directory
    (fs_tree) or a dict of path -> bytes (tests)."""

    def __init__(self, paths, reader, label):
        self.paths = frozenset(paths)
        self.dirs = frozenset(_parents(self.paths))
        self._reader = reader
        self._cache = {}
        self.label = label

    def is_file(self, path):
        return path in self.paths

    def is_dir(self, path):
        return path in self.dirs

    def walk(self, prefix):
        prefix = prefix.rstrip("/") + "/"
        return sorted(p for p in self.paths if p.startswith(prefix))

    def read(self, path):
        if path not in self._cache:
            self._cache[path] = self._reader(path)
        return self._cache[path]

    def text(self, path):
        return self.read(path).decode("utf-8", errors="replace")


def _parents(paths):
    out = set()
    for p in paths:
        d = posixpath.dirname(p)
        while d and d not in out:
            out.add(d)
            d = posixpath.dirname(d)
    return out


def dict_tree(files, label="dict"):
    data = {p: (b if isinstance(b, bytes) else b.encode()) for p, b in files.items()}
    return Tree(data, data.__getitem__, label)


def git_tree(rev, root="."):
    """The tree of a commit, blobs read on demand through one cat-file."""
    sha = git("rev-parse", "--verify", f"{rev}^{{commit}}", root=root).decode().strip()
    blobs = {}
    for entry in git("ls-tree", "-r", "-z", "--full-tree", sha, root=root).split(b"\0"):
        if not entry:
            continue
        meta, path = entry.split(b"\t", 1)
        mode, kind, blob = meta.split()
        if kind == b"blob":
            blobs[path.decode()] = blob.decode()
    proc = []

    def read(path):
        if not proc:
            proc.append(subprocess.Popen(["git", "cat-file", "--batch"], cwd=root,
                                         stdin=subprocess.PIPE, stdout=subprocess.PIPE))
        p = proc[0]
        p.stdin.write(blobs[path].encode() + b"\n")
        p.stdin.flush()
        header = p.stdout.readline().split()
        size = int(header[2])
        body = p.stdout.read(size)
        p.stdout.read(1)
        return body

    def close():
        for p in proc:
            p.stdin.close()
            p.wait()
            p.stdout.close()

    tree = Tree(blobs, read, sha)
    weakref.finalize(tree, close)
    return tree


def fs_tree(root="."):
    """The checked-out files git tracks under root."""
    paths = [p for p in git("ls-files", "-z", root=root).decode().split("\0") if p]
    paths = [p for p in paths if os.path.isfile(os.path.join(root, p))]

    def read(path):
        with open(os.path.join(root, path), "rb") as fh:
            return fh.read()

    return Tree(paths, read, os.path.abspath(root))


def uncomment(text):
    """YAML text without comment-only lines and trailing blanks."""
    return "\n".join(line.rstrip() for line in text.split("\n")
                     if line.strip() and not line.lstrip().startswith("#"))


def plays(text):
    """Top-level plays of a playbook, as text."""
    return [c for c in re.split(r"(?m)^(?=- )", text) if c.startswith("- ")]


def play_hosts(play):
    m = re.search(r"(?m)^(?:- |  )hosts:\s*[\"']?([^\"'\n#]+)", play)
    return m.group(1).strip() if m else ""


def split_roles(play):
    """(play text without its roles: list, [(role, entry text)])."""
    m = re.search(r"(?m)^  roles:[ \t]*\n", play)
    if not m:
        return play, []
    end = re.search(r"(?m)^(?:  \S|\S)", play[m.end():])
    stop = m.end() + end.start() if end else len(play)
    section = play[m.end():stop]
    entries = []
    for chunk in re.split(r"(?m)^(?=    - )", section):
        if not chunk.startswith("    - "):
            continue
        body = uncomment(chunk)
        name = re.match(r"\s*-\s*(?:role:\s*)?[\"']?([\w.-]+)", body)
        if name:
            entries.append((name.group(1), body))
    return play[:m.start()] + play[stop:], entries


def group_plays(tree, group):
    """(globals play text, ordered roles, {role: entry text}) for the plays
    of bootstrap.yml whose hosts pattern names group."""
    rest, order, entries = [], [], {}
    for play in plays(tree.text(BOOTSTRAP)):
        if group not in play_hosts(play):
            continue
        body, roles = split_roles(play)
        rest.append(uncomment(body))
        for name, entry in roles:
            if name not in entries:
                order.append(name)
                entries[name] = entry
            else:
                entries[name] += "\n" + entry
    if not rest:
        raise ValueError(f"{BOOTSTRAP} has no play for {group} in {tree.label}")
    return "\n".join(rest), order, entries


def references(text):
    """(kind, path) of every path a role file names."""
    text = uncomment(text)
    for kind, pattern in REFERENCES:
        for m in pattern.finditer(text):
            ref = m.group(1).rstrip(".,;:)]")
            if ref and ref not in ("../", ".."):
                yield kind, ref


def resolve(tree, role, source, kind, ref):
    """Repository files a reference names, or None when nothing matches."""
    role_dir = f"{ROLES}/{role}"
    if kind == "role":
        candidates = [f"{role_dir}/{ref}"]
    elif kind == "playbook":
        candidates = [f"{ANSIBLE}/{ref}"]
    else:
        candidates = [f"{role_dir}/{d}/{ref}" for d in LOOKUP_DIRS] + [f"{posixpath.dirname(source)}/{ref}"]
    for candidate in candidates:
        path = posixpath.normpath(candidate)
        if path.startswith("../") or path == "..":
            continue
        if tree.is_file(path):
            return [path]
        if tree.is_dir(path):
            return tree.walk(path)
    return None


class Roles:
    """Inputs of every role of a tree, computed once."""

    def __init__(self, tree):
        self.tree = tree
        self._own = {}
        self.unresolved = {}

    def own(self, role):
        """(role files, external inputs, included roles) of one role."""
        if role in self._own:
            return self._own[role]
        role_dir = f"{ROLES}/{role}"
        files = [p for p in self.tree.walk(role_dir) if not NOT_CONTENT.search(p[len(role_dir) + 1:])]
        external, included, missing = set(), set(), []
        for path in files:
            sub = path[len(role_dir) + 1:].split("/", 1)[0]
            if sub not in SCANNED_DIRS or not path.endswith((".yml", ".yaml")):
                continue
            text = self.tree.text(path)
            for kind, ref in references(text):
                found = resolve(self.tree, role, path, kind, ref)
                if found is None:
                    missing.append(f"{path}: {ref}")
                else:
                    external.update(found)
            included.update(ROLE_INCLUDE.findall(uncomment(text)))
            if sub == "meta":
                deps = re.search(r"(?ms)^dependencies:\s*\n(.*?)(?=^\S|\Z)", uncomment(text) + "\n")
                if deps:
                    included.update(re.findall(r"(?m)^\s*-\s*(?:role:\s*)?[\"']?([\w.-]+)", deps.group(1)))
        if missing:
            self.unresolved[role] = missing
        included.discard(role)
        self._own[role] = (files, external, included)
        return self._own[role]

    def inputs(self, role):
        """Every repository file the role reads, included roles' too."""
        seen, stack, out = {role}, [role], set()
        while stack:
            files, external, included = self.own(stack.pop())
            out.update(files)
            out.update(external)
            for name in included:
                if name not in seen:
                    seen.add(name)
                    stack.append(name)
        return out


def globals_inputs(tree, group):
    paths = [p for p in (*COMMON_FILES, CONVERGE_PLAYBOOK.get(group, "")) if p and tree.is_file(p)]
    for d in (f"{ANSIBLE}/group_vars/{group}", f"{ANSIBLE}/group_vars/all", f"{ANSIBLE}/host_vars"):
        paths.extend(p for p in tree.walk(d) if not NOT_CONTENT.search(p))
    return sorted(set(paths))


def digest(tree, preamble, fields, paths):
    h = hashlib.sha256()
    for name, body in [("format", preamble.encode()), *fields] + [(p, tree.read(p)) for p in sorted(paths)]:
        h.update(name.encode() + b"\0" + body + b"\0")
    return "sha256:" + h.hexdigest()


def group_hashes(tree, group, roles=None):
    """{format_version, group, globals, order, roles: {role: hash}}."""
    rest, order, entries = group_plays(tree, group)
    roles = roles or Roles(tree)
    out = {"format_version": FORMAT, "group": group, "order": order, "roles": {}}
    out["globals"] = digest(tree, f"gregale-host-globals/{FORMAT}", [("group", group.encode()),
                            ("plays", rest.encode())], globals_inputs(tree, group))
    for role in order:
        out["roles"][role] = digest(tree, f"gregale-host-role/{FORMAT}", [("role", role.encode()),
                                    ("entry", entries[role].encode())], roles.inputs(role))
    return out


def group_owners(tree, group, roles=None):
    """{path: sorted roles whose content it is}; globals map to every role
    ("*")."""
    _, order, _ = group_plays(tree, group)
    roles = roles or Roles(tree)
    owners = {BOOTSTRAP: {"*"}}
    for path in globals_inputs(tree, group):
        owners.setdefault(path, set()).add("*")
    for role in order:
        for path in roles.inputs(role):
            owners.setdefault(path, set()).add(role)
    return {p: sorted(v) for p, v in owners.items()}


def notify_targets(tree, order):
    """{role: other roles whose handlers it notifies}, from handler names
    and listen: topics. A role converged alone must not skip a handler
    another role defines, so selecting it selects them too."""
    owners = {}
    for role in order:
        for path in tree.walk(f"{ROLES}/{role}/handlers"):
            if path.endswith((".yml", ".yaml")):
                for topic in _topics(uncomment(tree.text(path)), handlers=True):
                    owners.setdefault(topic, set()).add(role)
    deps = {}
    for role in order:
        for path in tree.walk(f"{ROLES}/{role}/tasks"):
            if path.endswith((".yml", ".yaml")):
                for topic in _topics(uncomment(tree.text(path)), handlers=False):
                    # A topic the role handles itself needs no other role.
                    if role not in owners.get(topic, ()):
                        deps.setdefault(role, set()).update(owners.get(topic, ()))
    return {role: owned for role, owned in deps.items() if owned}


def _topics(text, handlers):
    keys = ("name", "listen") if handlers else ("notify",)
    lines = text.split("\n")
    for i, line in enumerate(lines):
        m = re.match(r"^(\s*)(?:- )?(\w+):\s*(.*)$", line)
        if not m or m.group(2) not in keys:
            continue
        if handlers and m.group(2) == "name" and not line.startswith("- name:"):
            continue
        value = m.group(3).strip()
        if value.startswith("["):
            yield from (v.strip().strip("\"'") for v in value.strip("[]").split(",") if v.strip())
        elif value:
            yield value.strip("\"'")
        else:
            indent = len(m.group(1))
            for nxt in lines[i + 1:]:
                item = re.match(r"^(\s*)-\s+(.+)$", nxt)
                if not item or len(item.group(1)) < indent:
                    break
                yield item.group(2).strip().strip("\"'")


def changed(old, new, deps=None):
    """Roles whose content differs between two group_hashes results (old
    may be a host's record, or None when nothing is known), plus the roles
    whose handlers they notify. {"all": bool, "roles": [...]} in play
    order."""
    order = new["order"]
    if (not old or old.get("format_version") != new["format_version"] or old.get("group") != new["group"]
            or old.get("globals") != new["globals"]):
        return {"all": True, "roles": list(order)}
    picked = {r for r in order if old.get("roles", {}).get(r) != new["roles"][r]}
    for role in list(picked):
        picked |= set((deps or {}).get(role, ()))
    return {"all": False, "roles": [r for r in order if r in picked]}


def tree_for(args):
    if getattr(args, "commit", None):
        return git_tree(args.commit, args.repo)
    return fs_tree(getattr(args, "root", None) or args.repo)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--repo", default=".", help="git checkout (default .)")
    sub = ap.add_subparsers(dest="command", required=True)
    for name in ("hashes", "owners"):
        p = sub.add_parser(name)
        p.add_argument("--group", default="control_plane", choices=GROUPS)
        src = p.add_mutually_exclusive_group()
        src.add_argument("--commit", help="read the tree of this commit")
        src.add_argument("--root", help="read this checkout's files (default --repo)")
    p = sub.add_parser("changed")
    p.add_argument("--group", default="control_plane", choices=GROUPS)
    p.add_argument("--to", required=True, help="commit to converge to")
    old = p.add_mutually_exclusive_group(required=True)
    old.add_argument("--from", dest="from_", help="commit the host converged last")
    old.add_argument("--record", help="the host's recorded hashes (JSON)")
    p = sub.add_parser("check")
    p.add_argument("--root", help="checkout to check (default --repo)")
    args = ap.parse_args(argv)
    try:
        if args.command == "hashes":
            out = group_hashes(tree_for(args), args.group)
        elif args.command == "owners":
            out = group_owners(tree_for(args), args.group)
        elif args.command == "changed":
            new_tree = git_tree(args.to, args.repo)
            new = group_hashes(new_tree, args.group)
            if args.record:
                with open(args.record, encoding="utf-8") as fh:
                    old = json.load(fh)
            else:
                old = group_hashes(git_tree(args.from_, args.repo), args.group)
            out = changed(old, new, notify_targets(new_tree, new["order"]))
        else:
            tree = fs_tree(args.root or args.repo)
            roles = Roles(tree)
            for group in GROUPS:
                group_owners(tree, group, roles)
            if roles.unresolved:
                for role, refs in sorted(roles.unresolved.items()):
                    for ref in refs:
                        print(f"host_roles: {role}: unresolved reference {ref}", file=sys.stderr)
                return 1
            out = {"ok": True}
    except (RuntimeError, ValueError, OSError) as err:
        print(f"host_roles: {err}", file=sys.stderr)
        return 1
    json.dump(out, sys.stdout, indent=2, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
