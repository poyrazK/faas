from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_run_report import APISandboxRunReport
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    run: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/api-sandbox-runs/{run}/report".format(
            run=quote(str(run), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxRunReport | Problem | None:
    if response.status_code == 200:
        response_200 = APISandboxRunReport.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxRunReport | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[APISandboxRunReport | Problem]:
    """Read a finished run using its report token.

    Args:
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxRunReport | Problem]
    """

    kwargs = _get_kwargs(
        run=run,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> APISandboxRunReport | Problem | None:
    """Read a finished run using its report token.

    Args:
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxRunReport | Problem
    """

    return sync_detailed(
        run=run,
        client=client,
    ).parsed


async def asyncio_detailed(
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[APISandboxRunReport | Problem]:
    """Read a finished run using its report token.

    Args:
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxRunReport | Problem]
    """

    kwargs = _get_kwargs(
        run=run,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    run: UUID,
    *,
    client: AuthenticatedClient,
) -> APISandboxRunReport | Problem | None:
    """Read a finished run using its report token.

    Args:
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxRunReport | Problem
    """

    return (
        await asyncio_detailed(
            run=run,
            client=client,
        )
    ).parsed
