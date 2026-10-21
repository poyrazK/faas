#!/usr/bin/env python3
"""Unit tests for binary_code.py (the code digest of a release binary)."""
import importlib.util
import os
import pathlib
import struct
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("binary_code", HERE / "binary_code.py")
bc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bc)

ALLOC, PROGBITS, NOBITS, STRTAB = 0x2, 1, 8, 3


def elf(sections):
    """A minimal 64-bit little-endian ELF file with sections
    [(name, type, flags, addr, body or size)]; .shstrtab is added last."""
    names = b"\0" + b"".join(n.encode() + b"\0" for n, *_ in sections) + b".shstrtab\0"
    entries, blob, offset, name_off = [], b"", 64, 1
    for name, kind, flags, addr, body in sections:
        size = body if kind == NOBITS else len(body)
        entries.append(struct.pack("<IIQQQQIIQQ", name_off, kind, flags, addr, offset, size, 0, 0, 1, 0))
        if kind != NOBITS:
            blob += body
            offset += len(body)
        name_off += len(name) + 1
    entries.append(struct.pack("<IIQQQQIIQQ", name_off, STRTAB, 0, 0, offset, len(names), 0, 0, 1, 0))
    blob += names
    shoff = 64 + len(blob)
    header = b"\x7fELF" + bytes([2, 1, 1]) + bytes(9)
    header += struct.pack("<HHIQQQIHHHHHH", 2, 62, 1, 0, 0, shoff, 0, 64, 0, 0, 64, len(entries) + 1, len(entries))
    null = bytes(64)
    return header + blob + null + b"".join(entries)


def program(text=b"\x90" * 16, lines=b"L" * 32, typelink_addr=0x2000, module=b"M" * 8, bss=64):
    return elf([
        (".text", PROGBITS, ALLOC | 0x4, 0x1000, text),
        (".gopclntab", PROGBITS, ALLOC, 0x1800, lines),
        (".typelink", PROGBITS, ALLOC, typelink_addr, b"T" * 8),
        (".go.module", PROGBITS, ALLOC | 0x1, 0x3000, module),
        (".bss", NOBITS, ALLOC | 0x1, 0x4000, bss),
    ])


class ElfDigestsTest(unittest.TestCase):
    def digests(self, data):
        fd, path = tempfile.mkstemp()
        self.addCleanup(os.remove, path)
        with os.fdopen(fd, "wb") as fh:
            fh.write(data)
        return bc.elf_digests(path)

    def test_cases(self):
        base = self.digests(program())
        cases = [
            # name, binary, same file, same code
            ("identical build", program(), True, True),
            ("a longer line table moves the sections after it and the module data",
             program(lines=b"L" * 48, typelink_addr=0x2010, module=b"N" * 8), False, True),
            ("different instructions", program(text=b"\x90" * 15 + b"\xcc"), False, False),
            ("a larger zero-filled section", program(bss=128), False, False),
        ]
        for name, binary, same_file, same_code in cases:
            with self.subTest(name):
                got = self.digests(binary)
                self.assertEqual(got[0] == base[0], same_file)
                self.assertEqual(got[1] == base[1], same_code)
                self.assertEqual(bc.compare({"x": base}, {"x": got}, ["x"])["x"],
                                 "same" if same_file else "lines" if same_code else "changed")

    def test_rejects_other_formats(self):
        with self.assertRaisesRegex(RuntimeError, "not a 64-bit little-endian ELF"):
            self.digests(b"\xcf\xfa\xed\xfe" + bytes(60))


class PromisorRemoteTest(unittest.TestCase):
    def test_outside_a_git_partial_clone(self):
        self.assertIsNone(bc.promisor_remote(tempfile.gettempdir()))


if __name__ == "__main__":
    unittest.main()
