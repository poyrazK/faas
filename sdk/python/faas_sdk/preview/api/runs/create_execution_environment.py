from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_execution_environment_request import CreateExecutionEnvironmentRequest
from ...models.execution_environment_response import ExecutionEnvironmentResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreateExecutionEnvironmentRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/execution-environments",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ExecutionEnvironmentResponse | Problem | None:
    if response.status_code == 202:
        response_202 = ExecutionEnvironmentResponse.from_dict(response.json())

        return response_202

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
) -> Response[ExecutionEnvironmentResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: CreateExecutionEnvironmentRequest,
) -> Response[ExecutionEnvironmentResponse | Problem]:
    """Build an immutable dependency environment in an isolated builder VM.

     Public registry dependencies only. Python requires exact versions and SHA-256 hashes; npm requires
    lockfile v3 integrity and disables install scripts. Matching recipes reuse the same account and key-
    family environment. Runtime base images are operator-pinned. Requires Runs write access and the
    environment preview gate.

    Args:
        body (CreateExecutionEnvironmentRequest): Immutable public language dependencies and exact
            Wolfi system package pins built inside an isolated builder VM.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExecutionEnvironmentResponse | Problem]
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
    body: CreateExecutionEnvironmentRequest,
) -> ExecutionEnvironmentResponse | Problem | None:
    """Build an immutable dependency environment in an isolated builder VM.

     Public registry dependencies only. Python requires exact versions and SHA-256 hashes; npm requires
    lockfile v3 integrity and disables install scripts. Matching recipes reuse the same account and key-
    family environment. Runtime base images are operator-pinned. Requires Runs write access and the
    environment preview gate.

    Args:
        body (CreateExecutionEnvironmentRequest): Immutable public language dependencies and exact
            Wolfi system package pins built inside an isolated builder VM.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExecutionEnvironmentResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: CreateExecutionEnvironmentRequest,
) -> Response[ExecutionEnvironmentResponse | Problem]:
    """Build an immutable dependency environment in an isolated builder VM.

     Public registry dependencies only. Python requires exact versions and SHA-256 hashes; npm requires
    lockfile v3 integrity and disables install scripts. Matching recipes reuse the same account and key-
    family environment. Runtime base images are operator-pinned. Requires Runs write access and the
    environment preview gate.

    Args:
        body (CreateExecutionEnvironmentRequest): Immutable public language dependencies and exact
            Wolfi system package pins built inside an isolated builder VM.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExecutionEnvironmentResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: CreateExecutionEnvironmentRequest,
) -> ExecutionEnvironmentResponse | Problem | None:
    """Build an immutable dependency environment in an isolated builder VM.

     Public registry dependencies only. Python requires exact versions and SHA-256 hashes; npm requires
    lockfile v3 integrity and disables install scripts. Matching recipes reuse the same account and key-
    family environment. Runtime base images are operator-pinned. Requires Runs write access and the
    environment preview gate.

    Args:
        body (CreateExecutionEnvironmentRequest): Immutable public language dependencies and exact
            Wolfi system package pins built inside an isolated builder VM.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExecutionEnvironmentResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
