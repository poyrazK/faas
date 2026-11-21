from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_execution_workflow_response import ManagedExecutionWorkflowResponse
from ...models.problem import Problem
from ...models.rerun_execution_pipeline_request import RerunExecutionPipelineRequest
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: RerunExecutionPipelineRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v2/execution-pipelines/{id}/reruns".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedExecutionWorkflowResponse | Problem | None:
    if response.status_code == 202:
        response_202 = ManagedExecutionWorkflowResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedExecutionWorkflowResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: RerunExecutionPipelineRequest,
) -> Response[ManagedExecutionWorkflowResponse | Problem]:
    """Create a new execution of failed branches and affected descendants.

     Requires the original manifest and key family. Every previously started affected step must be
    acknowledged in repeat_steps. Healthy independent outputs are reused only with intact fingerprints,
    contracts and owned unexpired artifacts. The original receipt stays immutable. New attempts use
    normal budgets. An exact child workflow_id replay returns its receipt even with writers off or
    outputs subsequently unavailable. Agent callback pipelines require a new delegated invocation.

    Args:
        id (UUID):
        body (RerunExecutionPipelineRequest): New workflow identity and acknowledgement of
            previously started steps that may repeat.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedExecutionWorkflowResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: RerunExecutionPipelineRequest,
) -> ManagedExecutionWorkflowResponse | Problem | None:
    """Create a new execution of failed branches and affected descendants.

     Requires the original manifest and key family. Every previously started affected step must be
    acknowledged in repeat_steps. Healthy independent outputs are reused only with intact fingerprints,
    contracts and owned unexpired artifacts. The original receipt stays immutable. New attempts use
    normal budgets. An exact child workflow_id replay returns its receipt even with writers off or
    outputs subsequently unavailable. Agent callback pipelines require a new delegated invocation.

    Args:
        id (UUID):
        body (RerunExecutionPipelineRequest): New workflow identity and acknowledgement of
            previously started steps that may repeat.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedExecutionWorkflowResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: RerunExecutionPipelineRequest,
) -> Response[ManagedExecutionWorkflowResponse | Problem]:
    """Create a new execution of failed branches and affected descendants.

     Requires the original manifest and key family. Every previously started affected step must be
    acknowledged in repeat_steps. Healthy independent outputs are reused only with intact fingerprints,
    contracts and owned unexpired artifacts. The original receipt stays immutable. New attempts use
    normal budgets. An exact child workflow_id replay returns its receipt even with writers off or
    outputs subsequently unavailable. Agent callback pipelines require a new delegated invocation.

    Args:
        id (UUID):
        body (RerunExecutionPipelineRequest): New workflow identity and acknowledgement of
            previously started steps that may repeat.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedExecutionWorkflowResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: RerunExecutionPipelineRequest,
) -> ManagedExecutionWorkflowResponse | Problem | None:
    """Create a new execution of failed branches and affected descendants.

     Requires the original manifest and key family. Every previously started affected step must be
    acknowledged in repeat_steps. Healthy independent outputs are reused only with intact fingerprints,
    contracts and owned unexpired artifacts. The original receipt stays immutable. New attempts use
    normal budgets. An exact child workflow_id replay returns its receipt even with writers off or
    outputs subsequently unavailable. Agent callback pipelines require a new delegated invocation.

    Args:
        id (UUID):
        body (RerunExecutionPipelineRequest): New workflow identity and acknowledgement of
            previously started steps that may repeat.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedExecutionWorkflowResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
