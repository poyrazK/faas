from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.execution_response import ExecutionResponse
from ...models.invoke_execution_task_request import InvokeExecutionTaskRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: InvokeExecutionTaskRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/run-tasks/invoke",
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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

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
    body: InvokeExecutionTaskRequest,
    idempotency_key: str,
) -> Response[ExecutionResponse | Problem]:
    """Invoke an explicit task version with key-family-scoped inputs and receipt. Requires a stable
    Idempotency-Key.

    Args:
        idempotency_key (str):
        body (InvokeExecutionTaskRequest): Task selection, invocation input and optional file or
            completion configuration.

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
    body: InvokeExecutionTaskRequest,
    idempotency_key: str,
) -> ExecutionResponse | Problem | None:
    """Invoke an explicit task version with key-family-scoped inputs and receipt. Requires a stable
    Idempotency-Key.

    Args:
        idempotency_key (str):
        body (InvokeExecutionTaskRequest): Task selection, invocation input and optional file or
            completion configuration.

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
    body: InvokeExecutionTaskRequest,
    idempotency_key: str,
) -> Response[ExecutionResponse | Problem]:
    """Invoke an explicit task version with key-family-scoped inputs and receipt. Requires a stable
    Idempotency-Key.

    Args:
        idempotency_key (str):
        body (InvokeExecutionTaskRequest): Task selection, invocation input and optional file or
            completion configuration.

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
    body: InvokeExecutionTaskRequest,
    idempotency_key: str,
) -> ExecutionResponse | Problem | None:
    """Invoke an explicit task version with key-family-scoped inputs and receipt. Requires a stable
    Idempotency-Key.

    Args:
        idempotency_key (str):
        body (InvokeExecutionTaskRequest): Task selection, invocation input and optional file or
            completion configuration.

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
