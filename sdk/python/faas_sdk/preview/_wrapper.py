"""faas_sdk.preview._wrapper - the preview `FaaSClient` (ADR-20261023115741).

`faas_sdk.FaaSClient` covers generally available and beta capabilities. This
subclass, exported as `FaaSClient` from `faas_sdk.preview`, adds the
disposable Runs helpers, a preview capability apid serves only to the
accounts it is released to.
"""

from __future__ import annotations

from collections.abc import AsyncIterator, Callable, Iterator
from typing import TYPE_CHECKING, Any
from uuid import UUID

from .._wrapper import FaaSClient as _BaseFaaSClient

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


class FaaSClient(_BaseFaaSClient):
    """`faas_sdk.FaaSClient` plus the preview disposable Runs helpers."""

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
