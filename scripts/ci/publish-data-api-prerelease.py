"""Publish only the explicitly authorized, qualified Data API draft release."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
from urllib.parse import quote

REPO = "poyrazK/faas"
RELEASE_ID = 408205988
TAG = "data-api/v0.1.0-rc.1"
SOURCE = "9e6901dbe6a0f1d6ea0ba54370fc662963184b88"
ACTION = "23004fba8a66cd83dcd940ea9b816d97d3e38fb5"
VERSION = "v0.1.18-data-api.1"


def gh(*args):
    return subprocess.check_output(["gh", *args], text=True)


def api(path):
    return json.loads(gh("api", f"repos/{REPO}/{path}"))


def require(condition, detail):
    if not condition:
        raise RuntimeError(detail)


def verify_tag(required=False):
    result = subprocess.run(
        ["gh", "api", f"repos/{REPO}/git/ref/tags/{quote(TAG, safe='')}"],
        capture_output=True, text=True, check=False,
    )
    if result.returncode:
        require(not required and "HTTP 404" in result.stderr,
                "Could not verify the release tag")
        return
    obj = json.loads(result.stdout)["object"]
    for _ in range(4):
        if obj["type"] != "tag":
            break
        obj = api(f"git/tags/{obj['sha']}")["object"]
    require(obj["type"] == "commit" and obj["sha"] == SOURCE,
            "The release tag does not identify the authorized source")


def draft():
    release = api(f"releases/{RELEASE_ID}")
    require(release["draft"] and release["prerelease"],
            "Refusing to change a published release or a stable release")
    require(release["tag_name"] == TAG and release["target_commitish"] == SOURCE,
            "The draft tag or source differs from the authorized release")
    return release


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--notes", type=Path, required=True)
    args = parser.parse_args()
    root = args.directory
    manifest = json.loads((root / "data-api-bundle.json").read_text())
    require(manifest["source"]["commit"] == SOURCE, "Unexpected bundle source")
    require(manifest["cli"]["version"] == VERSION, "Unexpected CLI version")
    require(manifest["cli"]["action_sha"] == ACTION, "Unexpected deploy Action pin")
    require(manifest["sdk"]["name"] == "@gregale/data" and
            manifest["sdk"]["version"] == "0.1.0", "Unexpected SDK")
    targets = {(x["os"], x["arch"]) for x in manifest["cli"]["archives"]}
    require(targets == {("linux", "amd64"), ("linux", "arm64"),
                        ("darwin", "amd64"), ("darwin", "arm64")} and
            len(manifest["cli"]["archives"]) == 4, "Incomplete target inventory")
    names = {x["file"] for x in manifest["cli"]["archives"]}
    names.update([manifest["sdk"]["file"], "data-api-bundle.json", "DATA-API-SHA256SUMS"])
    require(len(names) == 7 and all(Path(n).name == n for n in names),
            "Invalid release asset inventory")
    require({p.name for p in root.iterdir()} == names,
            "Missing or unexpected release files")
    require(all((root / n).is_file() and not (root / n).is_symlink() for n in names),
            "Release assets must be regular files")
    digests = {n: "sha256:" + hashlib.sha256((root / n).read_bytes()).hexdigest()
               for n in names}
    for entry in [*manifest["cli"]["archives"], manifest["sdk"]]:
        require(digests[entry["file"]] == "sha256:" + entry["sha256"] and
                (root / entry["file"]).stat().st_size == entry["bytes"],
                "Artifact differs from its qualified manifest")
    sums = {}
    for line in (root / "DATA-API-SHA256SUMS").read_text().splitlines():
        checksum, name = line.split("  ", 1)
        require(name in names and name not in sums, "Invalid checksum inventory")
        sums[name] = "sha256:" + checksum
    require(set(sums) == names - {"DATA-API-SHA256SUMS"} and
            all(sums[n] == digests[n] for n in sums), "Checksum mismatch")
    verify_tag()
    release = draft()
    existing = {a["name"]: a for a in release["assets"]}
    require(set(existing) <= names, "Unexpected asset already attached to draft")
    require(all(a.get("digest") == digests[n] and
                a["size"] == (root / n).stat().st_size for n, a in existing.items()),
            "Refusing to replace a mismatched uploaded asset")
    for name in sorted(names):
        if name not in existing:
            gh("release", "upload", TAG, str(root / name), "--repo", REPO)
        print(f"Uploaded or verified {name}", flush=True)
    release = draft()
    assets = {a["name"]: a for a in release["assets"]}
    require(set(assets) == names and len(release["assets"]) == 7,
            "Uploaded asset inventory is incomplete")
    require(all(a.get("digest") == digests[n] and a["state"] == "uploaded" and
                a["size"] == (root / n).stat().st_size for n, a in assets.items()),
            "Uploaded asset digest or size mismatch")
    with tempfile.TemporaryDirectory(prefix="data-api-release-verify-") as work:
        downloaded = Path(work) / "downloaded"
        gh("release", "download", TAG, "--repo", REPO, "--dir", str(downloaded))
        require({p.name for p in downloaded.iterdir()} == names,
                "Downloaded asset inventory mismatch")
        require(all("sha256:" + hashlib.sha256((downloaded / n).read_bytes()).hexdigest()
                    == digests[n] for n in names), "Downloaded bytes differ from qualification")
        verify_tag()
        draft()
        payload = Path(work) / "publish.json"
        payload.write_text(json.dumps({"draft": False, "prerelease": True,
                                       "make_latest": "false", "body": args.notes.read_text()}))
        published = json.loads(gh("api", "--method", "PATCH",
                                  f"repos/{REPO}/releases/{RELEASE_ID}",
                                  "--input", str(payload)))
    require(not published["draft"] and published["prerelease"], "Publication did not complete")
    verify_tag(required=True)
    print(f"Published verified prerelease: {published['html_url']}")


if __name__ == "__main__":
    main()
