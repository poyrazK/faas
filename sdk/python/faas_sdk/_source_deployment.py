"""Durable source deployment composition for existing apps; no implicit new attempts."""

from __future__ import annotations

import asyncio
import copy
import hashlib
import inspect
import json
import math
import os
import re
import stat
import tempfile
import threading
from collections.abc import Awaitable, Callable, Coroutine, Mapping
from contextlib import suppress
from dataclasses import asdict, dataclass, field, replace
from pathlib import Path
from typing import TYPE_CHECKING, Any, Literal, TypeVar
from uuid import uuid4

from ._async_request import RequestOptions, snapshot_async_client
from ._project_inspection import (
    ProjectInspectionError,
    ProjectInspectionOptions,
    ProjectPlan,
    project_build_files,
    validate_project_plan,
)
from ._source_preparation import (
    SourcePlan,
    SourcePreparationError,
    SourcePreparationOptions,
    SourcePreparationProgress,
    _prepare_source_with_files,
)
from ._source_preparation import _Operation as _PreparationOperation
from ._source_upload import (
    SourceUploadCheckpoint,
    SourceUploadError,
    SourceUploadOptions,
    SourceUploadProgress,
    _base,
    _options_hash,
    commit_source,
    upload_source,
)
from ._waiters import DeploymentWaitOptions, WaiterError, WaiterFailureCode, wait_for_deployment
from .models.deployment_submission_receipt import DeploymentSubmissionReceipt
from .models.release_timeline import ReleaseTimeline
from .types import UNSET

if TYPE_CHECKING:
    from ._wrapper import FaaSClient
    from .models.upload_deploy_options import UploadDeployOptions

SourceDeploymentPhase = Literal["preparing", "uploading", "committing", "observing", "complete"]
SourceDeploymentCode = Literal[
    "invalid_state",
    "state_mismatch",
    "busy",
    "state_failed",
    "preparation_failed",
    "upload_failed",
    "acceptance_unknown",
    "wait_failed",
    "progress_failed",
    "timeout",
    "cancelled",
]
T = TypeVar("T")
_ACTIVE: set[str] = set()
_LOCK = threading.Lock()
_BACKGROUND: set[asyncio.Future[Any]] = set()


def _retire(task: asyncio.Future[Any]) -> None:
    _BACKGROUND.add(task)

    def done(value: asyncio.Future[Any]) -> None:
        _BACKGROUND.discard(value)
        if not value.cancelled():
            value.exception()

    task.add_done_callback(done)


def _hash(value: Any) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[a-f0-9]{64}", value) is not None


@dataclass(frozen=True, slots=True)
class SourceDeploymentArchive:
    size: int
    sha256: str


@dataclass(frozen=True, slots=True)
class SourceDeploymentState:
    version: int
    workflow_id: str
    base_url: str
    app_slug: str
    directory: str
    metadata_sha256: str
    preparation_sha256: str
    archive: SourceDeploymentArchive | None = None
    upload: SourceUploadCheckpoint | None = None

    def __post_init__(self) -> None:
        if (
            type(self.version) is not int
            or self.version != 1
            or not isinstance(self.workflow_id, str)
            or not re.fullmatch(
                r"[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}", self.workflow_id
            )
            or not isinstance(self.base_url, str)
            or _base(self.base_url) != self.base_url
            or not isinstance(self.app_slug, str)
            or not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]*[a-z0-9])?", self.app_slug)
            or not isinstance(self.directory, str)
            or not os.path.isabs(self.directory)
            or not _hash(self.metadata_sha256)
            or not _hash(self.preparation_sha256)
        ):
            raise ValueError("Invalid source deployment state")
        a, u = self.archive, self.upload
        if a is not None and (
            not isinstance(a, SourceDeploymentArchive)
            or type(a.size) is not int
            or not 0 < a.size <= 2**53 - 1
            or not _hash(a.sha256)
        ):
            raise ValueError("Invalid source deployment archive")
        if u is not None and (
            not isinstance(u, SourceUploadCheckpoint)
            or a is None
            or u.base_url != self.base_url
            or u.app_slug != self.app_slug
            or u.options_sha256 != self.metadata_sha256
            or u.total_size != a.size
            or u.sha256_hex != a.sha256
        ):
            raise ValueError("Invalid source deployment upload binding")

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> SourceDeploymentState:
        try:
            if set(value) != set(cls.__dataclass_fields__):
                raise ValueError("Invalid source deployment fields")
            data = dict(value)
            if data["archive"] is not None:
                if set(data["archive"]) != {"size", "sha256"}:
                    raise ValueError("Invalid archive fields")
                data["archive"] = SourceDeploymentArchive(**data["archive"])
            if data["upload"] is not None:
                data["upload"] = SourceUploadCheckpoint.from_dict(data["upload"])
            return cls(**data)
        except (TypeError, ValueError, AttributeError):
            raise ValueError("Invalid source deployment state") from None


def _state_path(path: str | os.PathLike[str]) -> Path:
    target = Path(path).absolute()
    return target.parent.resolve(strict=True) / target.name


def load_source_deployment_state(path: str | os.PathLike[str]) -> SourceDeploymentState:
    fd = os.open(_state_path(path), os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    with os.fdopen(fd, "rb") as file:
        info = os.fstat(file.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > 65536:
            raise ValueError("Invalid source deployment state file")
        data = file.read(65537)
        if len(data) > 65536:
            raise ValueError("Invalid source deployment state file")
        return SourceDeploymentState.from_dict(json.loads(data))


def _write_state(path: Path, state: SourceDeploymentState) -> None:
    text = json.dumps(state.to_dict(), ensure_ascii=False, separators=(",", ":"))
    if len(text.encode()) > 65536:
        raise ValueError("Source deployment state exceeds 64 KiB")
    fd, temporary = tempfile.mkstemp(prefix="." + path.name + ".", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf8") as file:
            file.write(text)
            file.flush()
            os.fsync(file.fileno())
        os.replace(temporary, path)
        if os.name != "nt":
            directory = os.open(path.parent, os.O_RDONLY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


@dataclass(frozen=True, kw_only=True)
class SourceDeploymentPreparationOptions:
    dockerfile: bool | None = None
    exclude: tuple[str, ...] = ()
    secret_scan: Literal["strict", "off"] = "strict"
    max_entries: int = 10_000
    max_bytes: int = 2 << 30
    max_archive_bytes: int = (1 << 53) - 1
    on_plan: Callable[[SourcePlan], Awaitable[None] | None] | None = None


@dataclass(frozen=True, kw_only=True)
class SourceDeploymentWaitOptions:
    min_delay: float = 1.0
    max_delay: float = 5.0
    max_polls: int | None = None
    stop_on_blocked: bool = True

    def __post_init__(self) -> None:
        DeploymentWaitOptions(timeout=1, **asdict(self))


@dataclass(frozen=True, slots=True)
class SourceDeploymentReleaseProgress:
    phase: str
    status: str
    traffic_percent: float
    poll: int


@dataclass(frozen=True, slots=True)
class SourceDeploymentProgress:
    workflow_id: str
    phase: SourceDeploymentPhase
    elapsed_ms: float
    source: SourcePreparationProgress | None = None
    upload: SourceUploadProgress | None = None
    release: SourceDeploymentReleaseProgress | None = None


@dataclass(frozen=True, kw_only=True)
class SourceDeploymentOptions(RequestOptions):
    state_path: str | os.PathLike[str]
    timeout: float
    preparation: SourceDeploymentPreparationOptions = field(default_factory=SourceDeploymentPreparationOptions)
    wait: SourceDeploymentWaitOptions = field(default_factory=SourceDeploymentWaitOptions)
    max_recoveries: int = 3
    project: ProjectPlan | None = None
    on_progress: Callable[[SourceDeploymentProgress], Awaitable[None] | None] | None = None

    def __post_init__(self) -> None:
        SourceUploadOptions(
            timeout=self.timeout,
            on_checkpoint=lambda _: None,
            retry=self.retry,
            on_response=self.on_response,
            max_recoveries=self.max_recoveries,
        )
        if (
            not os.fspath(self.state_path)
            or not isinstance(self.preparation, SourceDeploymentPreparationOptions)
            or not isinstance(self.wait, SourceDeploymentWaitOptions)
        ):
            raise TypeError("A state_path and typed preparation/wait options are required")
        if self.on_progress is not None and not callable(self.on_progress):
            raise TypeError("on_progress must be callable")


@dataclass(frozen=True, slots=True)
class SourceDeploymentResult:
    outcome: Literal["succeeded", "blocked", "failed"]
    workflow_id: str
    state_path: str
    deployment_id: str
    receipt: DeploymentSubmissionReceipt
    timeline: ReleaseTimeline
    code: WaiterFailureCode | None = None


class SourceDeploymentError(Exception):
    def __init__(
        self,
        code: SourceDeploymentCode,
        phase: SourceDeploymentPhase,
        state_path: str,
        state: SourceDeploymentState | None,
        cause: BaseException | None = None,
    ):
        super().__init__(f"SDK source deployment stopped: {code}")
        self.code, self.phase, self.state_path, self.state, self.cause = code, phase, state_path, state, cause
        self.outcome: Literal["failed", "timed_out", "cancelled", "acceptance_unknown"] = (
            "timed_out"
            if code == "timeout"
            else "cancelled"
            if code == "cancelled"
            else "acceptance_unknown"
            if code == "acceptance_unknown"
            else "failed"
        )


class SourceDeploymentCancelledError(asyncio.CancelledError):
    def __init__(self, error: SourceDeploymentError):
        super().__init__(*(error.cause.args if error.cause else ()))
        self.code, self.outcome, self.phase = error.code, error.outcome, error.phase
        self.state_path, self.state, self.cause = error.state_path, error.state, error.cause


class _Workflow:
    def __init__(
        self,
        owner: FaaSClient,
        app_slug: str,
        options: SourceDeploymentOptions,
        deploy_options: UploadDeployOptions | None,
    ):
        self.options = replace(
            options, preparation=replace(options.preparation, exclude=tuple(options.preparation.exclude))
        )
        if options.project is not None:
            project = ProjectPlan.from_dict(options.project.to_dict())
            self.options = replace(
                self.options,
                project=project,
                preparation=replace(self.options.preparation, dockerfile=project.dockerfile),
            )
        self.owner = copy.copy(owner)
        self.owner._gen = copy.copy(owner._gen)
        self.client = snapshot_async_client(owner.async_httpx_client)
        self.owner._gen.set_async_httpx_client(self.client)
        self.metadata = copy.deepcopy(deploy_options, {id(UNSET): UNSET})
        self.base = _base(str(self.client.base_url))
        self.app_slug, self.path = app_slug, Path(options.state_path).absolute()
        self.state: SourceDeploymentState | None = None
        self.phase: SourceDeploymentPhase = "preparing"
        self.active, self.start = True, math.inf

        async def before_request(_: Any) -> None:
            self.check()

        self.client.event_hooks["request"].append(before_request)
        self.metadata_hash = _options_hash(
            {
                "present": self.metadata is not None,
                "value": self.metadata.to_dict() if self.metadata is not None else {},
            }
        )
        self.source_root = self.metadata.to_dict().get("source_root", ".") if self.metadata else "."
        prep = self.options.preparation
        self.prep_hash = hashlib.sha256(
            json.dumps(
                {
                    "sourceRoot": self.source_root,
                    "dockerfile": prep.dockerfile,
                    "exclude": sorted(set(prep.exclude)),
                    "secretScan": prep.secret_scan,
                    "maxEntries": prep.max_entries,
                    "maxBytes": prep.max_bytes,
                    "maxArchiveBytes": prep.max_archive_bytes,
                    **(
                        {"projectFingerprint": self.options.project.fingerprint}
                        if self.options.project is not None
                        else {}
                    ),
                },
                ensure_ascii=False,
                separators=(",", ":"),
            ).encode()
        ).hexdigest()
        _PreparationOperation(SourcePreparationOptions(source_root=self.source_root, timeout=1, **vars(prep)))

    def error(self, code: SourceDeploymentCode, cause: BaseException | None = None) -> SourceDeploymentError:
        return SourceDeploymentError(code, self.phase, str(self.path), self.state, cause)

    def check(self) -> None:
        if not self.active:
            raise asyncio.CancelledError()
        if asyncio.get_running_loop().time() - self.start >= self.options.timeout:
            raise self.error("timeout")

    def remaining(self) -> float:
        self.check()
        return self.options.timeout - (asyncio.get_running_loop().time() - self.start)

    def save(self, state: SourceDeploymentState) -> None:
        self.check()
        try:
            _write_state(self.path, state)
        except Exception as error:
            raise self.error("state_failed", error) from error
        self.state = state
        self.check()

    async def callback(self, callback: Callable[[Any], Any] | None, value: Any) -> None:
        self.check()
        if callback:
            result = callback(value)
            if inspect.isawaitable(result):
                task = asyncio.ensure_future(result)
                try:
                    await asyncio.shield(task)
                finally:
                    if not task.done():
                        task.cancel()
                    _retire(task)
        self.check()

    async def event(self, **detail: Any) -> None:
        assert self.state is not None
        event = SourceDeploymentProgress(
            self.state.workflow_id, self.phase, (asyncio.get_running_loop().time() - self.start) * 1000, **detail
        )
        try:
            await self.callback(self.options.on_progress, event)
        except Exception as error:
            self.check()
            raise self.error("progress_failed", error) from error

    async def work(self, directory: str | None) -> SourceDeploymentResult:
        held = False
        try:
            self.check()
            self.path = _state_path(self.path)
            with _LOCK:
                if str(self.path) in _ACTIVE:
                    raise self.error("busy")
                _ACTIVE.add(str(self.path))
                held = True
            try:
                self.state = load_source_deployment_state(self.path)
            except FileNotFoundError as error:
                if directory is None:
                    raise self.error("invalid_state", error) from error
            except (ValueError, OSError) as error:
                raise self.error("invalid_state", error) from error
            project = self.options.project
            if project is not None:
                metadata = self.metadata.to_dict() if self.metadata is not None else {}
                if (
                    any(i.severity == "error" for i in project.issues)
                    or project.app_slug is not None
                    and project.app_slug != self.app_slug
                    or directory is not None
                    and directory != os.path.abspath(project.context_root)
                    or any(metadata.get(k) != v for k, v in project.deploy_options.to_dict().items())
                    or metadata.get("runtime") != project.deploy_options.to_dict().get("runtime")
                    or metadata.get("handler") != project.deploy_options.to_dict().get("handler")
                    or bool(metadata.get("dockerfile")) != project.dockerfile
                ):
                    raise self.error("state_mismatch")
            if self.state is not None:
                if (
                    self.state.base_url != self.base
                    or self.state.app_slug != self.app_slug
                    or self.state.metadata_sha256 != self.metadata_hash
                    or self.state.preparation_sha256 != self.prep_hash
                    or directory is not None
                    and self.state.directory != directory
                ):
                    raise self.error("state_mismatch")
            else:
                assert directory is not None
                if self.path.parent.is_relative_to(Path(directory).resolve(strict=True)):
                    raise self.error("state_mismatch", ValueError("Workflow state must be outside the source context"))
                if project is not None and self.path.parent.is_relative_to(project.project_root):
                    raise self.error(
                        "state_mismatch", ValueError("Project journal must be outside the inspected project")
                    )
                self.save(
                    SourceDeploymentState(
                        1, str(uuid4()), self.base, self.app_slug, directory, self.metadata_hash, self.prep_hash
                    )
                )
            assert self.state is not None
            archive = self.path.parent / f".gregale-deploy-{self.state.workflow_id}.tar.gz"
            if self.state.archive is None:
                await self.event()
                if project is not None:
                    await validate_project_plan(project, options=ProjectInspectionOptions(timeout=self.remaining()))

                async def source_progress(event: SourcePreparationProgress) -> None:
                    await self.event(source=event)

                async def on_plan(plan: SourcePlan) -> None:
                    await self.callback(self.options.preparation.on_plan, plan)

                prep = SourcePreparationOptions(
                    source_root=self.source_root,
                    timeout=self.remaining(),
                    **{**vars(self.options.preparation), "on_plan": on_plan},
                    on_progress=source_progress,
                )
                prepared = await _prepare_source_with_files(
                    self.state.directory,
                    archive,
                    options=prep,
                    files=project_build_files(project) if project is not None else None,
                )
                if project is not None:
                    await validate_project_plan(project, options=ProjectInspectionOptions(timeout=self.remaining()))
                    for item in project.inputs:
                        path = Path(project.project_root) / item.path
                        if (
                            item.kind == "file"
                            and item.sha256 is not None
                            and path.is_relative_to(project.context_root)
                        ):
                            rel = path.relative_to(project.context_root).as_posix()
                            if not any(e.path == rel and e.included for e in prepared.plan.entries):
                                raise ProjectInspectionError(
                                    "invalid_plan", "An inspected build input is excluded from the archive"
                                )
                self.save(replace(self.state, archive=SourceDeploymentArchive(prepared.size, prepared.sha256)))

            def checkpoint(value: SourceUploadCheckpoint) -> None:
                assert self.state is not None
                self.save(replace(self.state, upload=value))

            async def upload_progress(value: SourceUploadProgress) -> None:
                await self.event(upload=value)

            def upload_options() -> SourceUploadOptions:
                return SourceUploadOptions(
                    timeout=self.remaining(),
                    retry=self.options.retry,
                    on_response=self.options.on_response,
                    max_recoveries=self.options.max_recoveries,
                    on_checkpoint=checkpoint,
                    on_progress=upload_progress,
                )

            if self.state.upload is None or self.state.upload.stage not in ("committing", "accepted"):
                self.phase = "uploading"
                await self.event()
                if self.state.upload is None:
                    assert self.state.archive is not None
                    self.save(
                        replace(
                            self.state,
                            upload=SourceUploadCheckpoint(
                                1,
                                self.base,
                                self.app_slug,
                                self.state.archive.size,
                                self.state.archive.sha256,
                                self.metadata_hash,
                                str(uuid4()),
                                str(uuid4()),
                                str(uuid4()),
                            ),
                        )
                    )
                if self.state.upload is not None and self.state.upload.stage == "new":
                    await self.event(upload=SourceUploadProgress("new", 0, self.state.upload.total_size))
                await upload_source(
                    self.owner,
                    archive,
                    self.app_slug,
                    deploy_options=self.metadata,
                    checkpoint=self.state.upload,
                    options=upload_options(),
                )
            self.phase = "committing"
            await self.event()
            assert self.state.upload is not None
            receipt = await commit_source(self.owner, self.state.upload, options=upload_options())
            self.phase = "observing"
            await self.event()

            async def release_progress(value: ReleaseTimeline, progress: Any) -> None:
                await self.event(
                    release=SourceDeploymentReleaseProgress(
                        value.phase, value.status, value.traffic_percent, progress.poll
                    )
                )

            code: WaiterFailureCode | None = None
            timeline = None
            try:
                timeline = await wait_for_deployment(
                    self.owner,
                    str(receipt.deployment_id),
                    DeploymentWaitOptions(
                        timeout=self.remaining(),
                        retry=self.options.retry,
                        on_response=self.options.on_response,
                        on_progress=release_progress,
                        **asdict(self.options.wait),
                    ),
                )
            except WaiterError as error:
                self.check()
                if error.code not in (
                    "failed",
                    "cancelled",
                    "rolled_back",
                    "superseded",
                    "blocked",
                    "not_serving",
                    "regressed",
                ):
                    raise
                code = error.code
                observed = error.last_observed
                if not isinstance(observed, ReleaseTimeline):
                    raise
                timeline = observed
            self.phase = "complete"
            await self.event()
            return SourceDeploymentResult(
                "blocked" if code == "blocked" else "failed" if code else "succeeded",
                self.state.workflow_id,
                str(self.path),
                str(receipt.deployment_id),
                copy.deepcopy(receipt),
                timeline,
                code,
            )
        except SourceDeploymentError:
            raise
        except Exception as error:
            self.check()
            cause = getattr(error, "cause", None)
            if isinstance(cause, SourceDeploymentError):
                raise cause from error
            if isinstance(error, SourceUploadError) and error.code == "acceptance_unknown":
                raise self.error("acceptance_unknown", error) from error
            if isinstance(error, SourceUploadError) and error.code == "checkpoint_failed":
                raise self.error("state_failed", error) from error
            if (
                isinstance(error, SourcePreparationError)
                and error.code == "deadline_exceeded"
                or isinstance(error, (SourceUploadError, WaiterError))
                and error.code == "timeout"
            ):
                raise self.error("timeout", error) from error
            raise self.error(
                "preparation_failed"
                if self.phase == "preparing"
                else "wait_failed"
                if self.phase == "observing"
                else "upload_failed",
                error,
            ) from error
        finally:
            if held:
                with _LOCK:
                    _ACTIVE.discard(str(self.path))
            await self.client.aclose()

    async def run(self, directory: str | None) -> SourceDeploymentResult:
        self.start = asyncio.get_running_loop().time()
        worker = asyncio.create_task(self.work(directory))
        try:
            done, _ = await asyncio.wait({worker}, timeout=self.options.timeout)
            if not done:
                raise self.error("timeout")
            return worker.result()
        except asyncio.CancelledError as error:
            raise SourceDeploymentCancelledError(self.error("cancelled", error)) from error
        finally:
            self.active = False
            if not worker.done():
                worker.cancel()
                # All user callbacks are shielded/retired separately. Drain owned cleanup
                # so a caller can resume this journal immediately after cancellation.
                with suppress(asyncio.CancelledError, Exception):
                    await worker
            _retire(worker)


def deploy_source(
    owner: FaaSClient,
    directory: str | os.PathLike[str],
    app_slug: str,
    *,
    options: SourceDeploymentOptions,
    deploy_options: UploadDeployOptions | None = None,
) -> Coroutine[Any, Any, SourceDeploymentResult]:
    workflow = _Workflow(owner, app_slug, options, deploy_options)
    return workflow.run(os.path.abspath(os.fspath(directory)))


def resume_source_deployment(
    owner: FaaSClient,
    app_slug: str,
    *,
    options: SourceDeploymentOptions,
    deploy_options: UploadDeployOptions | None = None,
) -> Coroutine[Any, Any, SourceDeploymentResult]:
    workflow = _Workflow(owner, app_slug, options, deploy_options)
    return workflow.run(None)


def deploy_project(
    owner: FaaSClient, project: ProjectPlan, *, options: SourceDeploymentOptions
) -> Coroutine[Any, Any, SourceDeploymentResult]:
    plan = ProjectPlan.from_dict(project.to_dict())
    if not plan.app_slug:
        raise ProjectInspectionError("invalid_plan", "Set app_slug when inspecting a project for deployment")
    return deploy_source(
        owner,
        plan.context_root,
        plan.app_slug,
        options=replace(options, project=plan),
        deploy_options=plan.deploy_options,
    )


def resume_project_deployment(
    owner: FaaSClient, project: ProjectPlan, *, options: SourceDeploymentOptions
) -> Coroutine[Any, Any, SourceDeploymentResult]:
    plan = ProjectPlan.from_dict(project.to_dict())
    if not plan.app_slug:
        raise ProjectInspectionError("invalid_plan", "Set app_slug when inspecting a project for deployment")
    return resume_source_deployment(
        owner, plan.app_slug, options=replace(options, project=plan), deploy_options=plan.deploy_options
    )
