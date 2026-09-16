from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.debug_regression_action_request import DebugRegressionActionRequest
from ...models.debug_regression_action_response import DebugRegressionActionResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    req_id: UUID,
    *,
    body: DebugRegressionActionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/apps/{slug}/debug/requests/{req_id}/evidence".format(
            slug=quote(str(slug), safe=""),
            req_id=quote(str(req_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DebugRegressionActionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DebugRegressionActionResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

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
) -> Response[DebugRegressionActionResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    req_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DebugRegressionActionRequest,
) -> Response[DebugRegressionActionResponse | Problem]:
    """Update regression triage state.

     Acknowledge, temporarily dismiss, resolve, or reopen one
    deployment/route regression observation. This changes debugger
    workflow metadata only; it never changes deployment traffic.
    `dismissed_until` is required only for dismiss and defaults to 24h
    when omitted. The server caps dismissals at 30 days.

    Args:
        slug (str):
        req_id (UUID):
        body (DebugRegressionActionRequest): Debugger-only workflow action for one
            deployment/route observation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugRegressionActionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        req_id=req_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    req_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DebugRegressionActionRequest,
) -> DebugRegressionActionResponse | Problem | None:
    """Update regression triage state.

     Acknowledge, temporarily dismiss, resolve, or reopen one
    deployment/route regression observation. This changes debugger
    workflow metadata only; it never changes deployment traffic.
    `dismissed_until` is required only for dismiss and defaults to 24h
    when omitted. The server caps dismissals at 30 days.

    Args:
        slug (str):
        req_id (UUID):
        body (DebugRegressionActionRequest): Debugger-only workflow action for one
            deployment/route observation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugRegressionActionResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        req_id=req_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    req_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DebugRegressionActionRequest,
) -> Response[DebugRegressionActionResponse | Problem]:
    """Update regression triage state.

     Acknowledge, temporarily dismiss, resolve, or reopen one
    deployment/route regression observation. This changes debugger
    workflow metadata only; it never changes deployment traffic.
    `dismissed_until` is required only for dismiss and defaults to 24h
    when omitted. The server caps dismissals at 30 days.

    Args:
        slug (str):
        req_id (UUID):
        body (DebugRegressionActionRequest): Debugger-only workflow action for one
            deployment/route observation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DebugRegressionActionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        req_id=req_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    req_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DebugRegressionActionRequest,
) -> DebugRegressionActionResponse | Problem | None:
    """Update regression triage state.

     Acknowledge, temporarily dismiss, resolve, or reopen one
    deployment/route regression observation. This changes debugger
    workflow metadata only; it never changes deployment traffic.
    `dismissed_until` is required only for dismiss and defaults to 24h
    when omitted. The server caps dismissals at 30 days.

    Args:
        slug (str):
        req_id (UUID):
        body (DebugRegressionActionRequest): Debugger-only workflow action for one
            deployment/route observation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DebugRegressionActionResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            req_id=req_id,
            client=client,
            body=body,
        )
    ).parsed
