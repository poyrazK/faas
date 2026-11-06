#!/usr/bin/env python3
"""Unit tests for guest_init_reuse.py (reuse the previous release's guest-init
while its code is the same)."""
import contextlib
import importlib.util
import io
import os
import pathlib
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / f"{name}.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


gir = load("guest_init_reuse")
bct = load("binary_code_test")
ALLOC, PROGBITS = bct.ALLOC, bct.PROGBITS


def guest_init(text=b"\x90" * 16, lines=b"L" * 32, build_id=b"id-one", rodata_addr=0x2000, module=b"M" * 8):
    """A guest-init-shaped ELF: code, the line table, the module data, and the
    build ID notes a release build leaves in."""
    return bct.elf([
        (".note.go.buildid", PROGBITS, ALLOC, 0x400, build_id),
        (".note.gnu.build-id", PROGBITS, ALLOC, 0x440, b"gnu-" + build_id),
        (".text", PROGBITS, ALLOC | 0x4, 0x1000, text),
        (".gopclntab", PROGBITS, ALLOC, 0x1800, lines),
        (".rodata", PROGBITS, ALLOC, rodata_addr, b"R" * 8),
        (".go.module", PROGBITS, ALLOC | 0x1, 0x3000, module),
    ])


class ClassifyTest(unittest.TestCase):
    def write(self, data):
        fd, path = tempfile.mkstemp()
        self.addCleanup(os.remove, path)
        with os.fdopen(fd, "wb") as fh:
            fh.write(data)
        return path

    def test_cases(self):
        prior = guest_init()
        cases = [
            # name, head binary, verdict
            ("the same bytes", guest_init(), gir.SAME),
            # What changes between two builds of unchanged code: a Go build
            # ID that hashes the whole import closure, and a line table that
            # moved every section after it.
            ("only the build ID differs", guest_init(build_id=b"id-two"), gir.LINES),
            ("only the line table differs", guest_init(lines=b"X" * 32), gir.LINES),
            ("a longer line table moves later sections and the module data",
             guest_init(lines=b"L" * 48, rodata_addr=0x2010, module=b"N" * 8), gir.LINES),
            ("all three together", guest_init(build_id=b"id-two", lines=b"X" * 32, module=b"N" * 8), gir.LINES),
            ("different instructions", guest_init(text=b"\x90" * 15 + b"\xcc"), gir.CHANGED),
            ("different instructions and build ID", guest_init(text=b"\xcc" * 16, build_id=b"id-two"), gir.CHANGED),
        ]
        for name, head, want in cases:
            with self.subTest(name):
                self.assertEqual(gir.classify(self.write(head), self.write(prior)), want)

    def test_the_build_id_only_matters_to_daemon_digests(self):
        # binary_code.elf_digests keeps its recipe for the daemons whose code
        # digests hosts record: it covers the build ID note unless told not to.
        a, b = self.write(guest_init()), self.write(guest_init(build_id=b"id-two"))
        self.assertNotEqual(gir.bc.elf_digests(a)[1], gir.bc.elf_digests(b)[1])
        self.assertEqual(gir.bc.elf_digests(a, ignore=gir.IGNORED_SECTIONS)[1],
                         gir.bc.elf_digests(b, ignore=gir.IGNORED_SECTIONS)[1])


class MainTest(unittest.TestCase):
    def run_main(self, head, prior):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = gir.main(["compare", "--head", head, "--prior", prior])
        return code, out.getvalue().strip(), err.getvalue()

    def test_exit_status_tells_the_release_script_what_to_ship(self):
        with tempfile.TemporaryDirectory() as tmp:
            paths = {}
            for name, data in (("prior", guest_init()), ("same", guest_init()),
                               ("lines", guest_init(build_id=b"id-two")),
                               ("changed", guest_init(text=b"\xcc" * 16)),
                               ("junk", b"\xcf\xfa\xed\xfe" + bytes(60))):
                paths[name] = os.path.join(tmp, name)
                with open(paths[name], "wb") as fh:
                    fh.write(data)
            self.assertEqual(self.run_main(paths["same"], paths["prior"])[:2], (0, "same"))
            self.assertEqual(self.run_main(paths["lines"], paths["prior"])[:2], (0, "lines"))
            self.assertEqual(self.run_main(paths["changed"], paths["prior"])[:2], (1, "changed"))
            code, out, err = self.run_main(paths["junk"], paths["prior"])
            self.assertEqual((code, out), (2, ""))
            self.assertIn("not a 64-bit little-endian ELF", err)
            code, out, err = self.run_main(os.path.join(tmp, "missing"), paths["prior"])
            self.assertEqual((code, out), (2, ""))


if __name__ == "__main__":
    unittest.main()
