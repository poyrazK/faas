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

from collections.abc import AsyncIterator, Callable, Iterator
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any
from uuid import UUID

import httpx

from ._transport import (
    RetryOptions,
    WrapperOptions,
    install_chain,
)
from .client import AuthenticatedClient
from .client import Client as _GenClient

if TYPE_CHECKING:
    from .executions import ExecutionEvent, ExecutionID
    from .models.create_execution_artifact_grant_request import CreateExecutionArtifactGrantRequest
    from .models.execution_artifact_grant_response import ExecutionArtifactGrantResponse
    from .models.execution_capabilities_response import ExecutionCapabilitiesResponse
    from .models.execution_list_response import ExecutionListResponse
    from .models.execution_response import ExecutionResponse
    from .models.execution_workflow_response import ExecutionWorkflowResponse
    from .models.problem import Problem
    from .models.revoke_execution_artifact_grant_response import RevokeExecutionArtifactGrantResponse
    from .types import Unset


@dataclass
class FaaSClientOptions:
    """Constructor kwargs for `FaaSClient`.

    `retry`, `logger`, and `httpx_args` are wrapper-only knobs.
    Every other kwarg is forwarded to the generator's `Client`.
    """

    retry: RetryOptions = field(default_factory=RetryOptions)
    logger: Any | None = None
    httpx_args: dict[str, Any] = field(default_factory=dict)


class FaaSClient:
    """Public sync facade for the one-box FaaS REST API.

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
        timeout: float = 30.0,
        headers: dict[str, str] | None = None,
        follow_redirects: bool = False,
        options: FaaSClientOptions | None = None,
        **httpx_args: Any,
    ) -> None:
        opts = options or FaaSClientOptions()
        merged_args = {**opts.httpx_args, **httpx_args}

        if token:
            gen_client: _GenClient = AuthenticatedClient(
                base_url=base_url,
                token=token,
                verify_ssl=verify_ssl,
                timeout=timeout,
                headers=headers or {},
                follow_redirects=follow_redirects,
                **merged_args,
            )
        else:
            gen_client = _GenClient(
                base_url=base_url,
                verify_ssl=verify_ssl,
                timeout=timeout,
                headers=headers or {},
                follow_redirects=follow_redirects,
                **merged_args,
            )

        wrapper_opts = WrapperOptions(retry=opts.retry, logger=opts.logger)
        install_chain(gen_client, options=wrapper_opts, verify_ssl=verify_ssl)

        self._gen = gen_client
        self.options = opts

    @property
    def inner(self) -> _GenClient:
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

    def watch_execution(
        self,
        execution_id: ExecutionID,
        *,
        after: int = 0,
        limit: int = 100,
        retry_initial: float = 0.1,
        retry_max: float = 2.0,
    ) -> Iterator[ExecutionEvent]:
        """Watch a disposable execution until its terminal event."""
        from .executions import watch_execution

        return watch_execution(
            self,
            execution_id,
            after=after,
            limit=limit,
            retry_initial=retry_initial,
            retry_max=retry_max,
        )

    def awatch_execution(
        self,
        execution_id: ExecutionID,
        *,
        after: int = 0,
        limit: int = 100,
        retry_initial: float = 0.1,
        retry_max: float = 2.0,
    ) -> AsyncIterator[ExecutionEvent]:
        """Async counterpart to :meth:`watch_execution`."""
        from .executions import awatch_execution

        return awatch_execution(
            self,
            execution_id,
            after=after,
            limit=limit,
            retry_initial=retry_initial,
            retry_max=retry_max,
        )

    def get_execution_capabilities(
        self,
    ) -> ExecutionCapabilitiesResponse | Problem | None:
        """Read this account's Runs admission contract and plan limits.

        The result does not indicate scheduler or runtime image readiness.
        """
        from .api.runs.get_execution_capabilities import sync

        return sync(client=self._gen)

    async def aget_execution_capabilities(
        self,
    ) -> ExecutionCapabilitiesResponse | Problem | None:
        """Async counterpart to :meth:`get_execution_capabilities`."""
        from .api.runs.get_execution_capabilities import asyncio

        return await asyncio(client=self._gen)

    def get_execution_workflow(
        self, workflow_id: str
    ) -> ExecutionWorkflowResponse | Problem | None:
        """Read lifecycle counts and terminal-run usage for a visible workflow."""
        from .api.runs.get_execution_workflow import sync

        return sync(workflow_id, client=self._gen)

    async def aget_execution_workflow(
        self, workflow_id: str
    ) -> ExecutionWorkflowResponse | Problem | None:
        """Async counterpart to :meth:`get_execution_workflow`."""
        from .api.runs.get_execution_workflow import asyncio

        return await asyncio(workflow_id, client=self._gen)

    def list_executions_for_workflow(
        self,
        workflow_id: str,
        *,
        limit: int = 50,
        offset: int = 0,
        status: str | None = None,
    ) -> ExecutionListResponse | Problem | None:
        """List visible run receipts carrying one workflow id."""
        from .api.runs.list_executions import sync

        return sync(
            client=self._gen,
            limit=limit,
            offset=offset,
            status=status,
            workflow_id=workflow_id,
        )

    async def alist_executions_for_workflow(
        self,
        workflow_id: str,
        *,
        limit: int = 50,
        offset: int = 0,
        status: str | None = None,
    ) -> ExecutionListResponse | Problem | None:
        """Async counterpart to :meth:`list_executions_for_workflow`."""
        from .api.runs.list_executions import asyncio

        return await asyncio(
            client=self._gen,
            limit=limit,
            offset=offset,
            status=status,
            workflow_id=workflow_id,
        )

    def create_execution_artifact_grant(
        self,
        execution_id: str | UUID,
        body: CreateExecutionArtifactGrantRequest,
    ) -> ExecutionArtifactGrantResponse | Problem | None:
        """Share one successful run artifact with another agent key."""
        from .api.runs.create_execution_artifact_grant import sync

        return sync(UUID(str(execution_id)), client=self._gen, body=body)

    async def acreate_execution_artifact_grant(
        self,
        execution_id: str | UUID,
        body: CreateExecutionArtifactGrantRequest,
    ) -> ExecutionArtifactGrantResponse | Problem | None:
        """Async counterpart to :meth:`create_execution_artifact_grant`."""
        from .api.runs.create_execution_artifact_grant import asyncio

        return await asyncio(UUID(str(execution_id)), client=self._gen, body=body)

    def revoke_execution_artifact_grant(
        self, grant_id: str | UUID
    ) -> RevokeExecutionArtifactGrantResponse | Problem | None:
        """Revoke a grant before an agent redeems it."""
        from .api.runs.revoke_execution_artifact_grant import sync

        return sync(UUID(str(grant_id)), client=self._gen)

    async def arevoke_execution_artifact_grant(
        self, grant_id: str | UUID
    ) -> RevokeExecutionArtifactGrantResponse | Problem | None:
        """Async counterpart to :meth:`revoke_execution_artifact_grant`."""
        from .api.runs.revoke_execution_artifact_grant import asyncio

        return await asyncio(UUID(str(grant_id)), client=self._gen)

    def run_execution(
        self,
        body: Any,
        *,
        on_event: Callable[[ExecutionEvent], Any] | None = None,
        idempotency_key: str | Unset | None = None,
        after: int = 0,
        limit: int = 100,
        retry_initial: float = 0.1,
        retry_max: float = 2.0,
    ) -> ExecutionResponse:
        """Create, stream, and return one disposable execution receipt."""
        from .executions import run_execution
        from .types import UNSET

        return run_execution(
            self,
            body,
            on_event=on_event,
            idempotency_key=UNSET if idempotency_key is None else idempotency_key,
            after=after,
            limit=limit,
            retry_initial=retry_initial,
            retry_max=retry_max,
        )

    async def arun_execution(
        self,
        body: Any,
        *,
        on_event: Callable[[ExecutionEvent], Any] | None = None,
        idempotency_key: str | Unset | None = None,
        after: int = 0,
        limit: int = 100,
        retry_initial: float = 0.1,
        retry_max: float = 2.0,
    ) -> ExecutionResponse:
        """Async counterpart to :meth:`run_execution`."""
        from .executions import arun_execution
        from .types import UNSET

        return await arun_execution(
            self,
            body,
            on_event=on_event,
            idempotency_key=UNSET if idempotency_key is None else idempotency_key,
            after=after,
            limit=limit,
            retry_initial=retry_initial,
            retry_max=retry_max,
        )

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
