#!/usr/bin/env python3
"""The code of a release binary: what it runs, apart from its version stamp,
Go build ID and line table.

Shared by blast_radius.py (the pull-request report) and affected_units.py
(per-unit release selection, docs/adr/1650-per-unit-control-plane-releases.md).

A release binary built at two commits can differ for three reasons that do
not change what it runs:

  version stamp  release builds stamp pkg/wire.Version (a tag or
                 unit-<commit>). Code builds stamp NEUTRAL_VERSION instead.
  build ID       the Go build ID hashes every package the binary links, so
                 it changes whenever anything in the import closure does.
                 Code builds blank it (-buildid=).
  line table     an edit above a function shifts the line numbers every
                 binary linking later functions of that file carries in
                 .gopclntab. The code digest leaves that section out; a
                 binary whose only difference is there is "lines". When the
                 table grows, the sections after it move and the runtime's
                 module data (.go.module) points at their new addresses, so
                 the digest also leaves out .go.module and every address:
                 a moved section that code or data refers to changes the
                 referring bytes (an instruction's displacement, a stored
                 pointer), which the digest covers.

.gopclntab also holds Go's function metadata (GC stack maps, inline trees,
defer records). A change that alters only that metadata, with identical
instructions, data and type descriptors, looks like "lines": in practice a
word reinterpreted between uintptr and a pointer type and nothing else. The
next full release ships every binary regardless.

Builds use the release flags: linux/amd64, CGO off, the Makefile's
GO_BUILD_FLAGS, its DAEMON_TAGS for daemons, -s -w. Release builds are
reproducible, so the digests are a function of the source, the recipe and
the Go toolchain.
"""
import hashlib
import os
import struct
import subprocess

# The stamp code builds give pkg/wire.Version. Digests recorded on hosts
# (unit-versions.json) were taken with it: changing it changes every
# binary's code digest once, so the next unit release restarts every unit
# it carries.
NEUTRAL_VERSION = "code"
VERSION_VAR = "pkg/wire.Version"
# The ELF section holding Go's line, file and function-name tables.
LINE_TABLE = ".gopclntab"
# Go 1.26's section for runtime.firstmoduledata: pointers to the other
# sections and the line table's sizes.
MODULE_DATA = ".go.module"
SHF_ALLOC = 0x2
SHT_NOBITS = 8
# compare() results that mean the binary runs different code.
CODE_CHANGED = ("changed", "added", "removed")


def promisor_remote(root="."):
    """The remote a partial clone fetches missing objects from, or None."""
    config = subprocess.run(["git", "config", "--get-regexp", r"^(extensions\.partialclone|remote\..*\.promisor)$"],
                            cwd=root, capture_output=True, text=True).stdout
    for line in config.splitlines():
        key, _, value = line.partition(" ")
        if key == "extensions.partialclone" and value:
            return value
        if key.endswith(".promisor") and value == "true":
            return key[len("remote."):-len(".promisor")]
    return None


def prefetch_tree(sha, have, root="."):
    """In a partial clone, fetch in one request the files of commit sha
    that differ from commit have (whose files the checkout already has).
    git archive would otherwise fetch each missing file separately. Returns
    how many were requested; 0 outside a partial clone."""
    remote = promisor_remote(root)
    if not remote:
        return 0
    diff = subprocess.run(["git", "diff", "--raw", "--no-abbrev", "--no-renames", have, sha], cwd=root,
                          capture_output=True, text=True, check=True).stdout
    blobs = set()
    for line in diff.splitlines():
        meta = line.split("\t", 1)[0].split()
        # :old-mode new-mode old-id new-id status; gitlinks and deletions
        # have no file to fetch.
        if len(meta) >= 5 and meta[1] != "160000" and meta[3].strip("0"):
            blobs.add(meta[3])
    if blobs:
        # The request git makes for a missing object, for all of them at once
        # (scripts/ci/fetch_release_history.sh does the same for deploy/).
        subprocess.run(["git", "-c", "fetch.negotiationAlgorithm=noop", "fetch", "--no-tags", "--no-write-fetch-head",
                        "--recurse-submodules=no", "--filter=blob:none", "--stdin", remote],
                       cwd=root, input="\n".join(sorted(blobs)) + "\n", capture_output=True, text=True, check=True)
    return len(blobs)


def export_tree(sha, dest, root="."):
    """Write the files of commit sha into dest."""
    os.makedirs(dest, exist_ok=True)
    archive = subprocess.Popen(["git", "archive", "--format=tar", sha], cwd=root, stdout=subprocess.PIPE)
    untar = subprocess.run(["tar", "-x", "-f", "-", "-C", dest], stdin=archive.stdout, capture_output=True)
    archive.stdout.close()
    if archive.wait() != 0 or untar.returncode != 0:
        raise RuntimeError(f"export {sha}: {untar.stderr.decode().strip()}")


def elf_digests(path):
    """(whole file, code) sha256 digests of a 64-bit little-endian ELF
    binary. The code digest covers the name, size and contents of every
    section the program loads, except the line table and the module data,
    and no addresses (see the module docstring)."""
    with open(path, "rb") as fh:
        data = fh.read()
    if data[:4] != b"\x7fELF" or data[4] != 2 or data[5] != 1:
        raise RuntimeError(f"{path} is not a 64-bit little-endian ELF binary")
    shoff, = struct.unpack_from("<Q", data, 0x28)
    shentsize, shnum, shstrndx = struct.unpack_from("<HHH", data, 0x3A)
    headers = [struct.unpack_from("<IIQQQQ", data, shoff + i * shentsize) for i in range(shnum)]
    names = data[headers[shstrndx][4]:headers[shstrndx][4] + headers[shstrndx][5]]
    code = hashlib.sha256()
    for name_off, kind, flags, _, offset, size in headers:
        name = names[name_off:names.index(b"\0", name_off)]
        if not flags & SHF_ALLOC or name.decode() in (LINE_TABLE, MODULE_DATA):
            continue
        body = b"" if kind == SHT_NOBITS else data[offset:offset + size]
        code.update(name + b"\0" + struct.pack("<Q", size) + hashlib.sha256(body).digest())
    return hashlib.sha256(data).hexdigest(), code.hexdigest()


def build(side, names, out):
    """{binary: (file digest, code digest)} of names built from side's tree
    with the release flags and NEUTRAL_VERSION (elf_digests). side has
    .path, .sha, .flags (GO_BUILD_FLAGS), .module and .tags(name)."""
    hashes = {}
    env = dict(os.environ, GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")
    ldflags = f"-X {side.module}/{VERSION_VAR}={NEUTRAL_VERSION} -s -w -buildid="
    for tags in sorted({tuple(side.tags(n)) for n in names}):
        group = sorted(n for n in names if tuple(side.tags(n)) == tags)
        cmd = [os.environ.get("GO", "go"), "build", *side.flags]
        if tags:
            cmd.append("-tags=" + ",".join(tags))
        cmd += ["-ldflags", ldflags, "-o", out + os.sep] + [f"./cmd/{n}" for n in group]
        run = subprocess.run(cmd, cwd=side.path, env=env, capture_output=True, text=True)
        if run.returncode != 0:
            raise RuntimeError(f"go build at {side.sha[:12]}: {run.stderr.strip()}")
        for n in group:
            hashes[n] = elf_digests(os.path.join(out, n))
            os.remove(os.path.join(out, n))
    return hashes


def compare(base_hashes, head_hashes, names):
    """{binary: "changed" | "lines" | "same" | "added" | "removed"} for
    names. "lines": only the line table differs (build's digests)."""
    out = {}
    for n in names:
        if n not in base_hashes:
            out[n] = "added"
        elif n not in head_hashes:
            out[n] = "removed"
        elif base_hashes[n] == head_hashes[n]:
            out[n] = "same"
        else:
            out[n] = "lines" if base_hashes[n][1] == head_hashes[n][1] else "changed"
    return out


def go_version(root="."):
    """The Go toolchain version builds in root use (`go env GOVERSION`)."""
    run = subprocess.run([os.environ.get("GO", "go"), "env", "GOVERSION"], cwd=root, capture_output=True, text=True)
    if run.returncode != 0:
        raise RuntimeError(f"go env GOVERSION: {run.stderr.strip()}")
    return run.stdout.strip()
