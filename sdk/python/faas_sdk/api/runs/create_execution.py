from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_execution_request import CreateExecutionRequest
from ...models.execution_response import ExecutionResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: CreateExecutionRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/executions",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ExecutionResponse | Problem | None:
    if response.status_code == 202:
        response_202 = ExecutionResponse.from_dict(response.json())

        return response_202

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ExecutionResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: CreateExecutionRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ExecutionResponse | Problem]:
    """Execute source in an isolated disposable microVM.

     Queues one bounded Node.js or Python source execution. Source and
    input are sealed by the control plane before dispatch; the guest has
    loopback-only networking and an internal ephemeral scratch filesystem.
    The VM is always destroyed before a terminal result is persisted.
    This endpoint is an explicit opt-in on the control plane and may return
    501 while the host isolation gate is disabled.

    Args:
        idempotency_key (str | Unset):
        body (CreateExecutionRequest): Source and JSON input for one disposable execution. v1
            supports only
            the listed interpreter runtimes and `network.mode=none`; dependencies,
            secrets, environment injection, and persistent disks are not part of
            this contract.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExecutionResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: CreateExecutionRequest,
    idempotency_key: str | Unset = UNSET,
) -> ExecutionResponse | Problem | None:
    """Execute source in an isolated disposable microVM.

     Queues one bounded Node.js or Python source execution. Source and
    input are sealed by the control plane before dispatch; the guest has
    loopback-only networking and an internal ephemeral scratch filesystem.
    The VM is always destroyed before a terminal result is persisted.
    This endpoint is an explicit opt-in on the control plane and may return
    501 while the host isolation gate is disabled.

    Args:
        idempotency_key (str | Unset):
        body (CreateExecutionRequest): Source and JSON input for one disposable execution. v1
            supports only
            the listed interpreter runtimes and `network.mode=none`; dependencies,
            secrets, environment injection, and persistent disks are not part of
            this contract.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExecutionResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: CreateExecutionRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ExecutionResponse | Problem]:
    """Execute source in an isolated disposable microVM.

     Queues one bounded Node.js or Python source execution. Source and
    input are sealed by the control plane before dispatch; the guest has
    loopback-only networking and an internal ephemeral scratch filesystem.
    The VM is always destroyed before a terminal result is persisted.
    This endpoint is an explicit opt-in on the control plane and may return
    501 while the host isolation gate is disabled.

    Args:
        idempotency_key (str | Unset):
        body (CreateExecutionRequest): Source and JSON input for one disposable execution. v1
            supports only
            the listed interpreter runtimes and `network.mode=none`; dependencies,
            secrets, environment injection, and persistent disks are not part of
            this contract.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExecutionResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: CreateExecutionRequest,
    idempotency_key: str | Unset = UNSET,
) -> ExecutionResponse | Problem | None:
    """Execute source in an isolated disposable microVM.

     Queues one bounded Node.js or Python source execution. Source and
    input are sealed by the control plane before dispatch; the guest has
    loopback-only networking and an internal ephemeral scratch filesystem.
    The VM is always destroyed before a terminal result is persisted.
    This endpoint is an explicit opt-in on the control plane and may return
    501 while the host isolation gate is disabled.

    Args:
        idempotency_key (str | Unset):
        body (CreateExecutionRequest): Source and JSON input for one disposable execution. v1
            supports only
            the listed interpreter runtimes and `network.mode=none`; dependencies,
            secrets, environment injection, and persistent disks are not part of
            this contract.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExecutionResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
