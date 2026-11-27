from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_execution_file_request import CreateExecutionFileRequest
from ...models.execution_file_response import ExecutionFileResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreateExecutionFileRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/execution-files",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ExecutionFileResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ExecutionFileResponse.from_dict(response.json())

        return response_200

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
        response_409 = cast(Any, None)
        return response_409

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
) -> Response[Any | ExecutionFileResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: CreateExecutionFileRequest,
) -> Response[Any | ExecutionFileResponse | Problem]:
    """Prepare a stable immutable upload identity without reserving quota.

     File IDs are scoped to account and Runs key family plus checksum and size. Check Run admission
    replay before uploading missing bytes. Preparing a file does not admit a Run or reserve storage.

    Args:
        body (CreateExecutionFileRequest): Declared byte length and checksum for a prepared
            immutable Run input file.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ExecutionFileResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: CreateExecutionFileRequest,
) -> Any | ExecutionFileResponse | Problem | None:
    """Prepare a stable immutable upload identity without reserving quota.

     File IDs are scoped to account and Runs key family plus checksum and size. Check Run admission
    replay before uploading missing bytes. Preparing a file does not admit a Run or reserve storage.

    Args:
        body (CreateExecutionFileRequest): Declared byte length and checksum for a prepared
            immutable Run input file.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ExecutionFileResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: CreateExecutionFileRequest,
) -> Response[Any | ExecutionFileResponse | Problem]:
    """Prepare a stable immutable upload identity without reserving quota.

     File IDs are scoped to account and Runs key family plus checksum and size. Check Run admission
    replay before uploading missing bytes. Preparing a file does not admit a Run or reserve storage.

    Args:
        body (CreateExecutionFileRequest): Declared byte length and checksum for a prepared
            immutable Run input file.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ExecutionFileResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: CreateExecutionFileRequest,
) -> Any | ExecutionFileResponse | Problem | None:
    """Prepare a stable immutable upload identity without reserving quota.

     File IDs are scoped to account and Runs key family plus checksum and size. Check Run admission
    replay before uploading missing bytes. Preparing a file does not admit a Run or reserve storage.

    Args:
        body (CreateExecutionFileRequest): Declared byte length and checksum for a prepared
            immutable Run input file.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ExecutionFileResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
