"""Install the closed, hash-pinned wheel set while building a platform image.

Uses only the standard library, installs no package manager, and runs no wheel
build hooks or scripts. Dependency acquisition never occurs in a tenant Run.
"""

import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import sys
import sysconfig
import urllib.request
import zipfile


def unpack_wheel(data: bytes, destination: Path) -> int:
    unpacked = 0
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        for member in archive.infolist():
            path = PurePosixPath(member.filename)
            if path.is_absolute() or ".." in path.parts or "\\" in member.filename:
                raise ValueError("invalid wheel path")
            if (member.external_attr >> 16) & 0o170000 == 0o120000:
                raise ValueError("wheel contains a symlink")
            if not path.parts:
                continue
            if path.parts[0].endswith(".data"):
                if len(path.parts) < 3 or path.parts[1] not in ("purelib", "platlib"):
                    continue  # No scripts or development headers in the runtime.
                path = PurePosixPath(*path.parts[2:])
            target = destination.joinpath(*path.parts)
            if member.is_dir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with target.open("xb") as output:
                    output.write(archive.read(member))
                target.chmod(0o644)
                unpacked += member.file_size
    return unpacked


def install(manifest_path: Path) -> None:
    manifest = json.loads(manifest_path.read_text())
    if manifest["profile"] != "python-data-v1" or manifest["runtime"] != "python313" or sys.version_info[:2] != (3, 13):
        raise ValueError("profile interpreter mismatch")
    destination = Path(sysconfig.get_path("purelib"))
    destination.mkdir(parents=True, exist_ok=True)
    compressed = unpacked = 0
    for wheel in manifest["wheels"]:
        if not wheel["url"].startswith("https://files.pythonhosted.org/"):
            raise ValueError("untrusted wheel origin")
        with urllib.request.urlopen(wheel["url"], timeout=60) as response:
            data = response.read()
        if hashlib.sha256(data).hexdigest() != wheel["sha256"]:
            raise ValueError("wheel checksum mismatch")
        compressed += len(data)
        unpacked += unpack_wheel(data, destination)
    manifest_path.with_name("execution-profile-build.json").write_text(
        json.dumps({"profile": manifest["profile"], "wheel_bytes": compressed, "installed_bytes": unpacked}) + "\n"
    )


if __name__ == "__main__":
    install(Path(sys.argv[1]))
