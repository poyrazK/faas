from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.support_read_request import SupportReadRequest
from ...models.support_read_result import SupportReadResult
from ...types import Response


def _get_kwargs(
    org: str,
    ticket: UUID,
    *,
    body: SupportReadRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{org}/support/tickets/{ticket}/read".format(
            org=quote(str(org), safe=""),
            ticket=quote(str(ticket), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SupportReadResult | None:
    if response.status_code == 202:
        response_202 = SupportReadResult.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[Problem | SupportReadResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    org: str,
    ticket: UUID,
    *,
    client: AuthenticatedClient,
    body: SupportReadRequest,
) -> Response[Problem | SupportReadResult]:
    """Mark organization support updates read

     Organization support. Marks only this caller's existing ticket notifications through the observed
    sequence. Later updates stay unread. Repeating the request is intrinsically idempotent; no permanent
    receipt or expected ticket revision is required.

    Args:
        org (str):
        ticket (UUID):
        body (SupportReadRequest): Observed notification boundary; future rows are never
            acknowledged.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SupportReadResult]
    """

    kwargs = _get_kwargs(
        org=org,
        ticket=ticket,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    org: str,
    ticket: UUID,
    *,
    client: AuthenticatedClient,
    body: SupportReadRequest,
) -> Problem | SupportReadResult | None:
    """Mark organization support updates read

     Organization support. Marks only this caller's existing ticket notifications through the observed
    sequence. Later updates stay unread. Repeating the request is intrinsically idempotent; no permanent
    receipt or expected ticket revision is required.

    Args:
        org (str):
        ticket (UUID):
        body (SupportReadRequest): Observed notification boundary; future rows are never
            acknowledged.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SupportReadResult
    """

    return sync_detailed(
        org=org,
        ticket=ticket,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    org: str,
    ticket: UUID,
    *,
    client: AuthenticatedClient,
    body: SupportReadRequest,
) -> Response[Problem | SupportReadResult]:
    """Mark organization support updates read

     Organization support. Marks only this caller's existing ticket notifications through the observed
    sequence. Later updates stay unread. Repeating the request is intrinsically idempotent; no permanent
    receipt or expected ticket revision is required.

    Args:
        org (str):
        ticket (UUID):
        body (SupportReadRequest): Observed notification boundary; future rows are never
            acknowledged.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SupportReadResult]
    """

    kwargs = _get_kwargs(
        org=org,
        ticket=ticket,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    org: str,
    ticket: UUID,
    *,
    client: AuthenticatedClient,
    body: SupportReadRequest,
) -> Problem | SupportReadResult | None:
    """Mark organization support updates read

     Organization support. Marks only this caller's existing ticket notifications through the observed
    sequence. Later updates stay unread. Repeating the request is intrinsically idempotent; no permanent
    receipt or expected ticket revision is required.

    Args:
        org (str):
        ticket (UUID):
        body (SupportReadRequest): Observed notification boundary; future rows are never
            acknowledged.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SupportReadResult
    """

    return (
        await asyncio_detailed(
            org=org,
            ticket=ticket,
            client=client,
            body=body,
        )
    ).parsed
