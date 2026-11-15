"""faas_sdk._wrapper - `FaaSClient` constructor.

Thin convenience class that builds the generator's `Client` and
installs the wrapper BaseTransport chain on top of its inner
`httpx.Client`. Service calls go through the inner client::

    from faas_sdk.api.apps import list_apps
    apps = list_apps.sync(client=client.inner)

`client.inner` is the wrapped generator `Client`; `client.httpx_client`
is the chain-bearing `httpx.Client` for streaming and SSE.
"""

from __future__ import annotations

from collections.abc import Callable, Coroutine
from dataclasses import dataclass, field
from os import PathLike
from typing import TYPE_CHECKING, Any, TypeVar

import httpx

from ._async_request import AsyncRequestTransport, RequestOptions, ResponseObserver, run_request
from ._pagination import AsyncPagination, PaginationOptions, PaginationPage, _builds, _deployments
from ._project_inspection import ProjectPlan
from ._project_verification import (
    ProjectVerificationOptions,
    ProjectVerificationReport,
    ProjectVerificationTarget,
    verify_project_application,
)
from ._project_workflows import (
    ProjectApplicationResult,
    ProjectApplyOptions,
    ProjectPreviewOptions,
    ReviewedProjectPlan,
    apply_project_plan,
    preview_project,
)
from ._source_deployment import (
    SourceDeploymentOptions,
    SourceDeploymentResult,
    deploy_project,
    deploy_source,
    resume_project_deployment,
    resume_source_deployment,
)
from ._source_upload import SourceUploadCheckpoint, SourceUploadOptions, commit_source, upload_source
from ._transport import (
    RetryOptions,
    WrapperOptions,
    install_chain,
)
from ._typed_jobs import TypedJob
from ._waiters import BuildWaitOptions, DeploymentWaitOptions, wait_for_build, wait_for_deployment
from .client import AuthenticatedClient
from .client import Client as _GenClient
from .job_runtime import JobContract, JobStore
from .types import UNSET, Unset

if TYPE_CHECKING:
    from .models.build_response import BuildResponse
    from .models.deployment_response import DeploymentResponse
    from .models.deployment_submission_receipt import DeploymentSubmissionReceipt
    from .models.get_builds_status import GetBuildsStatus
    from .models.release_timeline import ReleaseTimeline
    from .models.upload_deploy_options import UploadDeployOptions

_T = TypeVar("_T")
_JobInput = TypeVar("_JobInput")
_JobOutput = TypeVar("_JobOutput")


@dataclass
class FaaSClientOptions:
    """Constructor kwargs for `FaaSClient`.

    `retry`, `logger`, and `httpx_args` are wrapper-only knobs.
    Every other kwarg is forwarded to the generator's `Client`.
    """

    retry: RetryOptions = field(default_factory=RetryOptions)
    logger: Any | None = None
    httpx_args: dict[str, Any] = field(default_factory=dict)
    # Async HTTP responses only. Existing synchronous callers are unchanged.
    on_response: ResponseObserver | None = None


class FaaSClient:
    """Public sync and async facade for the Gregale API.

    Construct once at process start, reuse across requests. Reachable
    service namespaces are accessed through the inner generator client::

        from faas_sdk.api.apps import list_apps
        from faas_sdk.api.account import get_v1_account

        client = FaaSClient(
            base_url="https://faas.example.com",
            token="...",  # optional; omit for anonymous endpoints
        )
        me = get_v1_account.sync(client=client.inner)
        apps = list_apps.sync(client=client.inner)
    """

    def __init__(
        self,
        *,
        base_url: str,
        token: str | None = None,
        verify_ssl: bool | str = True,
        timeout: float | None = 30.0,
        headers: dict[str, str] | None = None,
        follow_redirects: bool = False,
        options: FaaSClientOptions | None = None,
        **httpx_args: Any,
    ) -> None:
        opts = options or FaaSClientOptions()
        merged_args = {**opts.httpx_args, **httpx_args}

        if token:
            gen_client: AuthenticatedClient | _GenClient = AuthenticatedClient(
                base_url=base_url,
                token=token,
                verify_ssl=verify_ssl,
                timeout=httpx.Timeout(timeout),
                headers=headers or {},
                follow_redirects=follow_redirects,
                **merged_args,
            )
        else:
            gen_client = _GenClient(
                base_url=base_url,
                verify_ssl=verify_ssl,
                timeout=httpx.Timeout(timeout),
                headers=headers or {},
                follow_redirects=follow_redirects,
                **merged_args,
            )

        wrapper_opts = WrapperOptions(retry=opts.retry, logger=opts.logger, on_response=opts.on_response)
        install_chain(gen_client, options=wrapper_opts, verify_ssl=verify_ssl)

        self._gen = gen_client
        self.options = opts
        self._request_timeout = 0.0 if timeout is None else timeout

    def arequest(
        self, operation: Callable[[], Coroutine[Any, Any, _T]], *, options: RequestOptions | None = None,
    ) -> Coroutine[Any, Any, _T]:
        """Run a fresh generated async operation with isolated request policy.

        Example: await client.arequest(lambda: get_app.asyncio("api", client=client.inner),
                                      options=RequestOptions(timeout=3, retry=False))
        Generated result types and existing sync/async entry points are preserved.
        """
        transport = self.async_httpx_client._transport
        if not isinstance(transport, AsyncRequestTransport):
            raise TypeError("arequest requires the SDK async transport chain")
        return run_request(transport.owner, operation, options or RequestOptions(), self._request_timeout)

    def aiter_app_deployments(
        self, slug: str, *, limit: int = 50, before: str | None = None,
        options: PaginationOptions | None = None,
    ) -> AsyncPagination[DeploymentResponse]:
        """Lazily yield typed app deployments; before is an opaque cursor."""
        return _deployments(self, slug, lambda page: iter(page.items), limit=limit, before=before, options=options)

    def aiter_app_deployment_pages(
        self, slug: str, *, limit: int = 50, before: str | None = None,
        options: PaginationOptions | None = None,
    ) -> AsyncPagination[PaginationPage[DeploymentResponse]]:
        """Lazily yield app pages with exact, resumable cursor text."""
        return _deployments(self, slug, lambda page: iter((page,)), limit=limit, before=before, options=options)

    def aiter_deployments(
        self, *, limit: int = 50, before: str | None = None, options: PaginationOptions | None = None,
    ) -> AsyncPagination[DeploymentResponse]:
        """Lazily yield typed account deployments."""
        return _deployments(self, None, lambda page: iter(page.items), limit=limit, before=before, options=options)

    def aiter_deployment_pages(
        self, *, limit: int = 50, before: str | None = None, options: PaginationOptions | None = None,
    ) -> AsyncPagination[PaginationPage[DeploymentResponse]]:
        """Lazily yield account deployment pages with exact cursor text."""
        return _deployments(self, None, lambda page: iter((page,)), limit=limit, before=before, options=options)

    def aiter_builds(
        self, *, app: str | Unset = UNSET, status: GetBuildsStatus | Unset = UNSET,
        limit: int = 50, before: str | None = None, options: PaginationOptions | None = None,
    ) -> AsyncPagination[BuildResponse]:
        """Lazily yield typed builds, retaining app and status filters."""
        return _builds(self, lambda page: iter(page.items), app=app, status=status,
                       limit=limit, before=before, options=options)

    def aiter_build_pages(
        self, *, app: str | Unset = UNSET, status: GetBuildsStatus | Unset = UNSET,
        limit: int = 50, before: str | None = None, options: PaginationOptions | None = None,
    ) -> AsyncPagination[PaginationPage[BuildResponse]]:
        """Lazily yield build pages with exact cursor text."""
        return _builds(self, lambda page: iter((page,)), app=app, status=status,
                       limit=limit, before=before, options=options)

    def wait_for_build(
        self, id: str, *, options: BuildWaitOptions,
    ) -> Coroutine[Any, Any, BuildResponse]:
        """Observe an existing build ID until success, a typed stop, or cancellation."""
        return wait_for_build(self, id, options)

    def wait_for_deployment(
        self, id: str, *, options: DeploymentWaitOptions,
    ) -> Coroutine[Any, Any, ReleaseTimeline]:
        """Observe the release timeline; resume by calling again with the same ID."""
        return wait_for_deployment(self, id, options)

    def upload_source(
        self, path: str | PathLike[str], app_slug: str, *, options: SourceUploadOptions,
        checkpoint: SourceUploadCheckpoint | None = None, deploy_options: UploadDeployOptions | None = None,
    ) -> Coroutine[Any, Any, SourceUploadCheckpoint]:
        """Upload a prebuilt archive, saving checkpoints before writes. Does not commit."""
        return upload_source(self, path, app_slug, options=options, checkpoint=checkpoint, deploy_options=deploy_options)

    def commit_source(
        self, checkpoint: SourceUploadCheckpoint, *, options: SourceUploadOptions,
    ) -> Coroutine[Any, Any, DeploymentSubmissionReceipt]:
        """Commit once or recover an existing acceptance receipt; never resubmit unknown work."""
        return commit_source(self, checkpoint, options=options)

    def deploy_source(
        self, directory: str | PathLike[str], app_slug: str, *, options: SourceDeploymentOptions,
        deploy_options: UploadDeployOptions | None = None,
    ) -> Coroutine[Any, Any, SourceDeploymentResult]:
        """Prepare, transfer, commit and observe an existing app with a durable local journal."""
        return deploy_source(self, directory, app_slug, options=options, deploy_options=deploy_options)

    def resume_source_deployment(
        self, app_slug: str, *, options: SourceDeploymentOptions,
        deploy_options: UploadDeployOptions | None = None,
    ) -> Coroutine[Any, Any, SourceDeploymentResult]:
        """Resume the saved workflow; uncertain acceptance never starts another attempt."""
        return resume_source_deployment(self, app_slug, options=options, deploy_options=deploy_options)

    def deploy_project(self, project: ProjectPlan, *, options: SourceDeploymentOptions) -> Coroutine[Any, Any, SourceDeploymentResult]:
        """Deploy the inspected project intent with a bound configuration fingerprint."""
        return deploy_project(self, project, options=options)

    def resume_project_deployment(self, project: ProjectPlan, *, options: SourceDeploymentOptions) -> Coroutine[Any, Any, SourceDeploymentResult]:
        """Resume the same inspected project; prepared source need not still exist."""
        return resume_project_deployment(self, project, options=options)

    def define_job(self, contract: JobContract[_JobInput, _JobOutput], *, store: JobStore | None = None) -> TypedJob[_JobInput, _JobOutput]:
        return TypedJob(self, contract, store)

    def verify_project_application(self, input: ProjectVerificationTarget | ProjectApplicationResult, *, options: ProjectVerificationOptions) -> Coroutine[Any, Any, ProjectVerificationReport]:
        return verify_project_application(self, input, options=options)

    def preview_project(self, directory: str | PathLike[str], project_slug: str, *, options: ProjectPreviewOptions, environment: str | None = None, only: tuple[str, ...] = (), no_triggers: bool | None = None, app_slugs: dict[str, str] | None = None) -> Coroutine[Any, Any, ReviewedProjectPlan]:
        return preview_project(self, directory, project_slug, options=options, environment=environment, only=only, no_triggers=no_triggers, app_slugs=app_slugs)

    def apply_project_plan(self, plan: ReviewedProjectPlan, *, options: ProjectApplyOptions) -> Coroutine[Any, Any, ProjectApplicationResult]:
        return apply_project_plan(self, plan, options=options)

    def resume_project_application(self, plan: ReviewedProjectPlan, *, options: ProjectApplyOptions) -> Coroutine[Any, Any, ProjectApplicationResult]:
        return apply_project_plan(self, plan, options=options, resume=True)

    @property
    def inner(self) -> AuthenticatedClient | _GenClient:
        """The wrapped generator `Client`. Use this for service
        calls::

            apps = client.apps.list_apps.sync(client=client.inner)
        """
        return self._gen

    @property
    def httpx_client(self) -> httpx.Client:
        """The chain-bearing underlying `httpx.Client`. Use this
        for `stream()` and SSE (see `faas_sdk.iter_sse`)."""
        return self._gen.get_httpx_client()

    @property
    def async_httpx_client(self) -> httpx.AsyncClient:
        """The chain-bearing underlying `httpx.AsyncClient`.

        Use this for async streaming helpers such as
        :func:`faas_sdk.awatch_execution`.
        """
        return self._gen.get_async_httpx_client()

    def close(self) -> None:
        self._gen.get_httpx_client().close()

    async def aclose(self) -> None:
        """Close the async transport chain, if it has been opened."""
        await self._gen.get_async_httpx_client().aclose()

    def __enter__(self) -> FaaSClient:
        self._gen.__enter__()
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self._gen.__exit__(exc_type, exc, tb)

    async def __aenter__(self) -> FaaSClient:
        await self._gen.__aenter__()
        return self

    async def __aexit__(self, exc_type, exc, tb) -> None:
        await self._gen.__aexit__(exc_type, exc, tb)


__all__ = [
    "FaaSClient",
    "FaaSClientOptions",
]
