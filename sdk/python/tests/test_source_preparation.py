"""Directory selection shared with Node and the CLI, plus independent tar verification."""

import asyncio
import hashlib
import io
import json
import os
import tarfile
from dataclasses import FrozenInstanceError
from pathlib import Path

import pytest

import faas_sdk as public
import faas_sdk.preview as preview
from faas_sdk import SourcePreparationError, SourcePreparationOptions, inspect_source, prepare_source

SDK = Path(__file__).resolve().parents[1]
CONTRACT = SDK / "request-contracts/source-preparation.json"
if not CONTRACT.exists():
    CONTRACT = SDK.parent / "request-contracts/source-preparation.json"
FIXTURE = json.loads(CONTRACT.read_text())


def create(root, case):
    root.mkdir()
    for path, content in case["files"].items():
        target = root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)
    for path, target in case.get("links", {}).items():
        (root / path).symlink_to(target)
    for path in case.get("executable", []):
        (root / path).chmod(0o755)


@pytest.mark.parametrize("sdk", [public, preview], ids=["public", "preview"])
@pytest.mark.parametrize("case", FIXTURE["cases"], ids=lambda c: c["name"])
async def test_shared_selection_and_archives(tmp_path, case, sdk):
    root = tmp_path / "source"
    create(root, case)
    options = SourcePreparationOptions(source_root=case.get("source_root", "."), exclude=tuple(case.get("exclude", ())))
    plan = await sdk.inspect_source(root, options=options)
    assert not plan.issues
    assert sorted(e.path for e in plan.entries if e.included and e.kind == "file") == sorted(case["included"])
    with pytest.raises(FrozenInstanceError):
        plan.file_count = 0
    first = await sdk.prepare_source(root, tmp_path / "first.tgz", options=options)
    for path in case["files"]:
        os.utime(root / path, (1, 1))
    second = await sdk.prepare_source(root, tmp_path / "second.tgz", options=options)
    data = Path(first.path).read_bytes()
    assert data == Path(second.path).read_bytes()
    assert first.sha256 == hashlib.sha256(data).hexdigest() and first.size == len(data)
    assert Path(first.path).stat().st_mode & 0o777 == 0o600
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
        assert sorted(e.name for e in archive if e.isfile()) == sorted(case["included"])
        for entry in archive:
            planned = next(e for e in plan.entries if e.path == entry.name)
            assert entry.uid == entry.gid == entry.mtime == 0
            assert entry.uname == entry.gname == ""
            assert entry.mode == planned.mode
            if entry.isfile():
                assert archive.extractfile(entry).read() == (root / entry.name).read_bytes()


@pytest.mark.parametrize("case", FIXTURE["secrets"], ids=lambda c: c["name"])
async def test_strict_dotenv_contract(tmp_path, case):
    root = tmp_path / "source"
    root.mkdir()
    line = "".join(case["line_parts"])
    (root / ".env").write_text(line + "\n")
    plan = await inspect_source(root)
    assert any(e.code == "secret_detected" for e in plan.issues) == case["blocked"]
    assert line not in repr(plan)
    if case["blocked"]:
        with pytest.raises(SourcePreparationError) as error:
            await prepare_source(root, tmp_path / "out.tgz")
        assert error.value.code == "invalid_source" and error.value.plan is not None
        assert not (await inspect_source(root, options=SourcePreparationOptions(secret_scan="off"))).issues


async def test_unsafe_links_and_irregular_files_are_blocking_issues(tmp_path):
    root = tmp_path / "source"
    root.mkdir()
    (tmp_path / "private").write_text("private")
    (root / "main.py").write_text("source")
    (root / "escape").symlink_to("../private")
    (root / "dangling").symlink_to("absent")
    (root / "cycle").symlink_to(".")
    os.mkfifo(root / "pipe")
    plan = await inspect_source(root)
    assert len(plan.issues) == 4 and any(e.code == "unsupported_file" for e in plan.issues)
    with pytest.raises(SourcePreparationError, match="blocking issues"):
        await prepare_source(root, tmp_path / "out.tgz")


@pytest.mark.parametrize("change", ["content", "directory", "ignore", "symlink"])
async def test_source_changes_preserve_existing_destination(tmp_path, change):
    root, output = tmp_path / "source", tmp_path / "out.tgz"
    root.mkdir()
    (root / "file").write_text("before")
    output.write_text("existing")

    def on_plan(plan):
        if change == "content":
            (root / "file").write_text("after!")
        elif change == "directory":
            (root / "added").write_text("new")
        elif change == "ignore":
            (root / ".gregaleignore").write_text("file\n")
        elif change == "symlink":
            (root / "file").unlink()
            (root / "file").symlink_to("../out.tgz")

    with pytest.raises(SourcePreparationError) as error:
        await prepare_source(root, output, options=SourcePreparationOptions(on_plan=on_plan))
    assert error.value.code == "source_changed"
    assert output.read_text() == "existing" and sorted(p.name for p in tmp_path.iterdir()) == ["out.tgz", "source"]


async def test_limits_deadlines_and_paths(tmp_path):
    root, output = tmp_path / "source", tmp_path / "out.tgz"
    root.mkdir()
    (root / "one").write_text("12345")
    (root / "two").write_text("12345")
    output.write_text("existing")
    for options in [
        SourcePreparationOptions(max_bytes=1),
        SourcePreparationOptions(max_entries=1),
        SourcePreparationOptions(max_archive_bytes=1),
    ]:
        with pytest.raises(SourcePreparationError) as error:
            await prepare_source(root, output, options=options)
        assert error.value.code == "limit_exceeded"
    with pytest.raises(SourcePreparationError, match="outside"):
        await prepare_source(root, root / "out.tgz")
    with pytest.raises(SourcePreparationError, match="relative"):
        await inspect_source(root, options=SourcePreparationOptions(source_root="../outside"))

    async def never(plan):
        await asyncio.Event().wait()

    with pytest.raises(SourcePreparationError) as error:
        await prepare_source(root, output, options=SourcePreparationOptions(timeout=0.02, on_plan=never))
    assert error.value.code == "deadline_exceeded"
    await asyncio.sleep(0)
    assert output.read_text() == "existing" and sorted(p.name for p in tmp_path.iterdir()) == ["out.tgz", "source"]


async def test_cancellation_retires_callbacks_that_suppress_it(tmp_path):
    root, output = tmp_path / "source", tmp_path / "out.tgz"
    root.mkdir()
    (root / "file").write_text("content")
    output.write_text("existing")
    entered, released, retired = asyncio.Event(), asyncio.Event(), asyncio.Event()

    async def progress(event):
        if event.phase == "packing":
            entered.set()
            try:
                await released.wait()
            except asyncio.CancelledError:
                await released.wait()
            retired.set()

    task = asyncio.create_task(prepare_source(root, output, options=SourcePreparationOptions(on_progress=progress)))
    await entered.wait()
    task.cancel("caller stopped")
    with pytest.raises(asyncio.CancelledError, match="caller stopped"):
        await task
    released.set()
    await retired.wait()
    for _ in range(10):
        await asyncio.sleep(0)
    assert output.read_text() == "existing"
    assert sorted(p.name for p in tmp_path.iterdir()) == ["out.tgz", "source"]


async def test_independent_concurrent_preparations_use_bounded_reads(tmp_path):
    roots = [tmp_path / "a", tmp_path / "b"]
    for i, root in enumerate(roots):
        root.mkdir()
        (root / "file").write_bytes(bytes([i + 1]) * (3 * 1024 * 1024))
    largest, previous = 0, {}

    def progress(index, event):
        nonlocal largest
        key = index, event.phase, event.path
        largest = max(largest, event.bytes - previous.get(key, 0))
        previous[key] = event.bytes

    results = await asyncio.gather(
        *(
            prepare_source(
                root, tmp_path / f"{i}.tgz", options=SourcePreparationOptions(on_progress=lambda e, i=i: progress(i, e))
            )
            for i, root in enumerate(roots)
        )
    )
    assert largest == 65536 and results[0].sha256 != results[1].sha256


async def test_empty_plans_and_excluded_application_roots_are_blocking(tmp_path):
    root = tmp_path / "source"
    root.mkdir()
    assert any(e.code == "invalid_source" for e in (await inspect_source(root)).issues)
    (root / "apps").mkdir()
    (root / "apps/main.py").write_text("source")
    (root / "other.py").write_text("source")
    plan = await inspect_source(root, options=SourcePreparationOptions(source_root="apps", exclude=("apps",)))
    assert any(e.path == "apps" for e in plan.issues)


async def test_mutation_while_streaming_cannot_publish(tmp_path):
    root, output = tmp_path / "source", tmp_path / "out.tgz"
    root.mkdir()
    (root / "file").write_bytes(bytes([1]) * (256 * 1024))
    output.write_text("existing")
    changed = False

    def progress(event):
        nonlocal changed
        if event.phase == "packing" and not changed:
            changed = True
            (root / "file").write_bytes(bytes([2]) * (256 * 1024))

    with pytest.raises(SourcePreparationError) as error:
        await prepare_source(root, output, options=SourcePreparationOptions(on_progress=progress))
    assert error.value.code == "source_changed" and output.read_text() == "existing"
