"""Platform wheel installation checks; no OCI builder or network required."""

import importlib.util
import io
from pathlib import Path
import tempfile
import unittest
import zipfile

SPEC = importlib.util.spec_from_file_location("installer", Path(__file__).with_name("install-execution-profile.py"))
installer = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(installer)


def wheel(entries):
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w") as archive:
        for name, content in entries:
            archive.writestr(name, content)
    return buffer.getvalue()


class InstallProfileTests(unittest.TestCase):
    def test_installs_libraries_and_metadata_without_scripts(self):
        data = wheel([
            ("pkg/__init__.py", "value = 42"),
            ("pkg-1.dist-info/METADATA", "Name: pkg\nVersion: 1\n"),
            ("pkg-1.data/purelib/extra.py", "extra = 1"),
            ("pkg-1.data/scripts/unsafe-hook", "raise RuntimeError('executed')"),
        ])
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            count = installer.unpack_wheel(data, root)
            self.assertEqual(count, sum(p.stat().st_size for p in root.rglob("*") if p.is_file()))
            self.assertTrue((root / "extra.py").is_file())
            self.assertTrue((root / "pkg-1.dist-info/METADATA").is_file())
            self.assertFalse((root / "pkg-1.data").exists())
            self.assertEqual((root / "pkg/__init__.py").stat().st_mode & 0o777, 0o644)

    def test_rejects_unsafe_paths(self):
        for name in ("../escape", "/escape", "pkg/../../escape", "pkg\\escape"):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                with self.assertRaises(ValueError):
                    installer.unpack_wheel(wheel([(name, "bad")]), Path(directory))

    def test_rejects_symlinks(self):
        info = zipfile.ZipInfo("pkg/link")
        info.create_system = 3
        info.external_attr = 0o120777 << 16
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(ValueError):
                installer.unpack_wheel(wheel([(info, "../../escape")]), Path(directory))

    def test_refuses_overwrite(self):
        data = wheel([("existing.py", "replacement")])
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "existing.py").write_text("original")
            with self.assertRaises(FileExistsError):
                installer.unpack_wheel(data, root)
            self.assertEqual((root / "existing.py").read_text(), "original")


if __name__ == "__main__":
    unittest.main()
