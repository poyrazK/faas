from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_task_compatibility_report_request import MCPTaskCompatibilityReportRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: MCPTaskCompatibilityReportRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/mcp-task-compatibility".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 413:
        response_413 = cast(Any, None)
        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: MCPTaskCompatibilityReportRequest,
) -> Response[Any | Problem]:
    """Publish an MCP task handler compatibility snapshot

     Replaces the app's bounded observer snapshot with aggregate waiting-task and live-worker counts by
    tool and handler version. The report contains no task IDs, owners, or payloads; timestamps are
    assigned by Gregale.

    Args:
        slug (str):
        body (MCPTaskCompatibilityReportRequest): Bounded aggregate handler inventory published by
            the task observer.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: MCPTaskCompatibilityReportRequest,
) -> Any | Problem | None:
    """Publish an MCP task handler compatibility snapshot

     Replaces the app's bounded observer snapshot with aggregate waiting-task and live-worker counts by
    tool and handler version. The report contains no task IDs, owners, or payloads; timestamps are
    assigned by Gregale.

    Args:
        slug (str):
        body (MCPTaskCompatibilityReportRequest): Bounded aggregate handler inventory published by
            the task observer.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: MCPTaskCompatibilityReportRequest,
) -> Response[Any | Problem]:
    """Publish an MCP task handler compatibility snapshot

     Replaces the app's bounded observer snapshot with aggregate waiting-task and live-worker counts by
    tool and handler version. The report contains no task IDs, owners, or payloads; timestamps are
    assigned by Gregale.

    Args:
        slug (str):
        body (MCPTaskCompatibilityReportRequest): Bounded aggregate handler inventory published by
            the task observer.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: MCPTaskCompatibilityReportRequest,
) -> Any | Problem | None:
    """Publish an MCP task handler compatibility snapshot

     Replaces the app's bounded observer snapshot with aggregate waiting-task and live-worker counts by
    tool and handler version. The report contains no task IDs, owners, or payloads; timestamps are
    assigned by Gregale.

    Args:
        slug (str):
        body (MCPTaskCompatibilityReportRequest): Bounded aggregate handler inventory published by
            the task observer.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
