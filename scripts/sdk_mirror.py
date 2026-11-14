#!/usr/bin/env python3
"""Publish the SDKs to their public repositories in the gregale-dev org.

This repository stays the source of truth for the SDKs
(docs/adr/20261112110000-sdk-public-repositories.md). sdk/mirror.json maps
tracked files under sdk/ into each public repository; this script builds that
tree and, from CI, commits it to the repository's main branch.

  sdk_mirror.py build --repo gregale-go --out DIR   write the tree to DIR
  sdk_mirror.py check                               build every repo, validate
  sdk_mirror.py push --repo gregale-go --remote URL [--dry-run]

Markdown links that leave a mapped directory are rewritten: a page published
on the docs site (docs/customer-pages.json) links there, anything else links
to this repository on GitHub, and a path that another mapping publishes in
the same repository keeps a relative link.

Standard library only.
"""
from __future__ import annotations

import argparse
import json
import os
import posixpath
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CONFIG = "sdk/mirror.json"
OVERLAY = "sdk/mirror"
PAGES = "docs/customer-pages.json"
BOT = ("github-actions[bot]", "41898282+github-actions[bot]@users.noreply.github.com")

# [text](target) and [label]: target. Images use the same syntax after "!".
INLINE_LINK = re.compile(r"(\]\()([^)\s]+)((?:\s+\"[^\"]*\")?\))")
REF_LINK = re.compile(r"^(\s{0,3}\[[^\]]+\]:\s+)(\S+)", re.MULTILINE)
FENCE = re.compile(r"^(\s*)(```|~~~)")


class MirrorError(Exception):
    pass


def load_config(root: Path = ROOT) -> dict:
    config = json.loads((root / CONFIG).read_text())
    names = [r["name"] for r in config["repos"]]
    if len(names) != len(set(names)):
        raise MirrorError(f"{CONFIG}: duplicate repository name")
    return config


def repo_config(config: dict, name: str) -> dict:
    for repo in config["repos"]:
        if repo["name"] == name:
            return repo
    raise MirrorError(f"{CONFIG}: no repository named {name!r}")


def tracked_files(root: Path, path: str) -> list[str]:
    out = subprocess.run(
        ["git", "ls-files", "-z", "--", path],
        cwd=root, check=True, capture_output=True,
    ).stdout.decode()
    return sorted(f for f in out.split("\0") if f)


def published_pages(root: Path, upstream: str) -> dict[str, str]:
    """Source path -> public URL: the docs site page, or "" for this repository on GitHub."""
    pages = json.loads((root / PAGES).read_text())
    base = pages["base_url"].rstrip("/")
    out = {}
    for route in pages["routes"]:
        source = route.get("source")
        if not source or route.get("unpublished") or route.get("external"):
            continue
        out[source] = f"{base}/{route['path']}" if route["path"] else base
    out[""] = f"https://github.com/{upstream}/blob/main/"
    return out


def plan(root: Path, repo: dict) -> dict[str, str]:
    """Destination path -> source path for every file the repository gets."""
    files: dict[str, str] = {}
    for mapping in repo["paths"]:
        src, dst = mapping["from"].rstrip("/"), mapping["to"].strip("/")
        sources = tracked_files(root, src)
        if not sources:
            raise MirrorError(f"{repo['name']}: {src} has no tracked files")
        for source in sources:
            rel = posixpath.relpath(source, src) if source != src else ""
            dest = posixpath.join(dst, rel) if rel else (dst or posixpath.basename(src))
            dest = dest.strip("/")
            if dest in files and files[dest] != source:
                raise MirrorError(f"{repo['name']}: {dest} mapped from {files[dest]} and {source}")
            files[dest] = source
    for overlay in (f"{OVERLAY}/common", f"{OVERLAY}/{repo['name']}"):
        for source in tracked_files(root, overlay):
            dest = posixpath.relpath(source, overlay)
            files[dest] = source  # an overlay replaces a mapped file
    return files


def mapped_dest(resolved: str, mappings: list[dict]) -> str | None:
    """Where a source path lands in the repository, or None if it is not published."""
    best = None
    for mapping in mappings:
        src, dst = mapping["from"].rstrip("/"), mapping["to"].strip("/")
        if resolved == src or resolved.startswith(src + "/"):
            if best is None or len(src) > len(best[0]):
                best = (src, dst)
    if best is None:
        return None
    src, dst = best
    rest = resolved[len(src):].lstrip("/")
    return posixpath.join(dst, rest).strip("/") if rest else (dst or ".")


def rewrite_target(target: str, source: str, dest: str, mappings: list[dict],
                   pages: dict[str, str]) -> str:
    if re.match(r"^[a-zA-Z][a-zA-Z0-9+.-]*:", target) or target.startswith(("#", "/")):
        return target
    path, sep, anchor = target.partition("#")
    if not path:
        return target
    resolved = posixpath.normpath(posixpath.join(posixpath.dirname(source), path))
    if resolved.startswith("../"):
        return target  # outside the repository; the check reports it
    fragment = sep + anchor
    landed = mapped_dest(resolved, mappings)
    if landed is not None:
        rel = posixpath.relpath(landed, posixpath.dirname(dest) or ".")
        if path.endswith("/") and not rel.endswith("/"):
            rel += "/"
        return rel + fragment
    if resolved in pages:
        return pages[resolved] + fragment
    return pages[""] + resolved + fragment


def rewrite_markdown(text: str, source: str, dest: str, mappings: list[dict],
                     pages: dict[str, str]) -> str:
    out, in_fence, fence = [], False, ""
    for line in text.splitlines(keepends=True):
        m = FENCE.match(line)
        if m:
            if not in_fence:
                in_fence, fence = True, m.group(2)
            elif m.group(2) == fence:
                in_fence = False
            out.append(line)
            continue
        if in_fence:
            out.append(line)
            continue
        line = INLINE_LINK.sub(
            lambda m: m.group(1) + rewrite_target(m.group(2), source, dest, mappings, pages) + m.group(3),
            line,
        )
        line = REF_LINK.sub(
            lambda m: m.group(1) + rewrite_target(m.group(2), source, dest, mappings, pages),
            line,
        )
        out.append(line)
    return "".join(out)


def build(root: Path, upstream: str, repo: dict, out: Path) -> dict[str, str]:
    files = plan(root, repo)
    pages = published_pages(root, upstream)
    for dest, source in files.items():
        target = out / dest
        target.parent.mkdir(parents=True, exist_ok=True)
        src_path = root / source
        if src_path.is_symlink():
            raise MirrorError(f"{source}: symlinks are not mirrored")
        if source.endswith(".md") and not source.startswith(OVERLAY + "/"):
            text = src_path.read_text()
            target.write_text(rewrite_markdown(text, source, dest, repo["paths"], pages))
        else:
            shutil.copyfile(src_path, target)
        shutil.copymode(src_path, target)
    return files


def check_tree(org: str, repo: dict, out: Path, files: dict[str, str]) -> list[str]:
    problems = []
    for dest in ("README.md", "LICENSE"):
        if dest not in files:
            problems.append(f"{repo['name']}: no {dest} at the repository root")
    for dest, source in files.items():
        if not dest.endswith(".md"):
            continue
        text = (out / dest).read_text()
        in_fence, fence = False, ""
        for n, line in enumerate(text.splitlines(), 1):
            m = FENCE.match(line)
            if m:
                if not in_fence:
                    in_fence, fence = True, m.group(2)
                elif m.group(2) == fence:
                    in_fence = False
                continue
            if in_fence:
                continue
            for target in [m.group(2) for m in INLINE_LINK.finditer(line)] + [
                m.group(2) for m in REF_LINK.finditer(line)
            ]:
                if re.match(r"^[a-zA-Z][a-zA-Z0-9+.-]*:", target) or target.startswith("#"):
                    continue
                path = target.partition("#")[0]
                if not path:
                    continue
                resolved = posixpath.normpath(posixpath.join(posixpath.dirname(dest), path))
                if resolved.startswith("../") or not (out / resolved).exists():
                    problems.append(f"{repo['name']}/{dest}:{n}: broken link {target} (from {source})")
    if repo["name"] == "gregale-go":
        module = (out / "go.mod").read_text().splitlines()[0]
        want = f"module github.com/{org}/gregale-go"
        if module != want:
            problems.append(f"gregale-go/go.mod: {module!r}, want {want!r}")
    return problems


def git(*args: str, cwd: Path, env: dict | None = None) -> str:
    return subprocess.run(
        ["git", *args], cwd=cwd, check=True, capture_output=True, text=True,
        env={**os.environ, **(env or {})},
    ).stdout


def upstream_sha(root: Path) -> str:
    return git("rev-parse", "HEAD", cwd=root).strip()


def push(root: Path, upstream: str, repo: dict, remote: str, dry_run: bool) -> None:
    sha = upstream_sha(root)
    with tempfile.TemporaryDirectory() as tmp:
        work = Path(tmp) / "repo"
        empty = not git("ls-remote", "--heads", remote, "main", cwd=root).strip()
        if empty:
            work.mkdir()
            git("init", "--quiet", "--initial-branch=main", cwd=work)
            git("remote", "add", "origin", remote, cwd=work)
        else:
            git("clone", "--quiet", "--depth", "1", "--branch", "main", remote, str(work), cwd=root)
        for entry in work.iterdir():
            if entry.name == ".git":
                continue
            shutil.rmtree(entry) if entry.is_dir() and not entry.is_symlink() else entry.unlink()
        build(root, upstream, repo, work)
        git("add", "--all", cwd=work)
        if not git("status", "--porcelain", cwd=work).strip():
            print(f"{repo['name']}: up to date with {sha[:12]}")
            return
        title = "Initial import" if empty else f"Update from upstream {sha[:12]}"
        name, email = BOT
        env = {"GIT_AUTHOR_NAME": name, "GIT_AUTHOR_EMAIL": email,
               "GIT_COMMITTER_NAME": name, "GIT_COMMITTER_EMAIL": email}
        git("commit", "--quiet", "-m", title, "-m", f"Upstream-Commit: {sha}", cwd=work, env=env)
        stat = git("show", "--stat", "--format=%s", "HEAD", cwd=work).strip().splitlines()
        print(f"{repo['name']}: {stat[0]} ({stat[-1].strip()})")
        if dry_run:
            print(f"{repo['name']}: dry run, not pushing")
            return
        git("push", "--quiet", "origin", "HEAD:refs/heads/main", cwd=work)
        print(f"{repo['name']}: pushed")



def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)
    b = sub.add_parser("build")
    b.add_argument("--repo", required=True)
    b.add_argument("--out", required=True, type=Path)
    sub.add_parser("check")
    p = sub.add_parser("push")
    p.add_argument("--repo", required=True)
    p.add_argument("--remote", required=True)
    p.add_argument("--dry-run", action="store_true")
    sub.add_parser("list", help="print each repository name and secret key, tab-separated")
    args = parser.parse_args(argv)

    try:
        config = load_config()
        if args.cmd == "list":
            for repo in config["repos"]:
                print(f"{repo['name']}\t{repo['key']}")
            return 0
        if args.cmd == "build":
            repo = repo_config(config, args.repo)
            if args.out.exists() and any(args.out.iterdir()):
                raise MirrorError(f"{args.out} is not empty")
            files = build(ROOT, config["upstream"], repo, args.out)
            problems = check_tree(config["org"], repo, args.out, files)
            for problem in problems:
                print(problem, file=sys.stderr)
            print(f"{repo['name']}: {len(files)} files in {args.out}")
            return 1 if problems else 0
        if args.cmd == "check":
            problems = []
            for repo in config["repos"]:
                with tempfile.TemporaryDirectory() as tmp:
                    files = build(ROOT, config["upstream"], repo, Path(tmp))
                    problems += check_tree(config["org"], repo, Path(tmp), files)
                    print(f"{repo['name']}: {len(files)} files")
            for problem in problems:
                print(problem, file=sys.stderr)
            return 1 if problems else 0
        if args.cmd == "push":
            push(ROOT, config["upstream"], repo_config(config, args.repo), args.remote, args.dry_run)
            return 0
    except MirrorError as err:
        print(f"sdk_mirror: {err}", file=sys.stderr)
        return 1
    except subprocess.CalledProcessError as err:
        print(f"sdk_mirror: {' '.join(err.cmd)} failed:\n{err.stderr or ''}", file=sys.stderr)
        return 1
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
