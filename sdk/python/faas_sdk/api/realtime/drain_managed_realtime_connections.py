from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_drain_request import ManagedRealtimeDrainRequest
from ...models.managed_realtime_drain_response import ManagedRealtimeDrainResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: str,
    *,
    body: ManagedRealtimeDrainRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/connections/drain".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimeDrainResponse | Problem | None:
    if response.status_code == 202:
        response_202 = ManagedRealtimeDrainResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[ManagedRealtimeDrainResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeDrainRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedRealtimeDrainResponse | Problem]:
    """Close a bounded or explicitly all-matching set of live managed realtime connections.

     Selects connections from a point-in-time fleet inventory by channel,
    principal, or explicit connection IDs. `dry_run` returns the selected
    connections without closing them. A non-dry-run request fails with
    `409 conflict` when the inventory is partial unless `allow_partial`
    is true; this prevents an unavailable node from making a drain look
    complete. Set `all` to select every matching connection, up to the
    server safety cap of 10,000; `all` cannot be combined with
    `connection_ids`; any supplied `limit` is ignored in all mode, and
    the request returns `409 conflict` when the cap would be exceeded.

    Args:
        slug (str):
        id (str):
        idempotency_key (str | Unset):
        body (ManagedRealtimeDrainRequest): Bounded or explicitly all-matching auditable selection
            for closing live connections.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeDrainResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeDrainRequest,
    idempotency_key: str | Unset = UNSET,
) -> ManagedRealtimeDrainResponse | Problem | None:
    """Close a bounded or explicitly all-matching set of live managed realtime connections.

     Selects connections from a point-in-time fleet inventory by channel,
    principal, or explicit connection IDs. `dry_run` returns the selected
    connections without closing them. A non-dry-run request fails with
    `409 conflict` when the inventory is partial unless `allow_partial`
    is true; this prevents an unavailable node from making a drain look
    complete. Set `all` to select every matching connection, up to the
    server safety cap of 10,000; `all` cannot be combined with
    `connection_ids`; any supplied `limit` is ignored in all mode, and
    the request returns `409 conflict` when the cap would be exceeded.

    Args:
        slug (str):
        id (str):
        idempotency_key (str | Unset):
        body (ManagedRealtimeDrainRequest): Bounded or explicitly all-matching auditable selection
            for closing live connections.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeDrainResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeDrainRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ManagedRealtimeDrainResponse | Problem]:
    """Close a bounded or explicitly all-matching set of live managed realtime connections.

     Selects connections from a point-in-time fleet inventory by channel,
    principal, or explicit connection IDs. `dry_run` returns the selected
    connections without closing them. A non-dry-run request fails with
    `409 conflict` when the inventory is partial unless `allow_partial`
    is true; this prevents an unavailable node from making a drain look
    complete. Set `all` to select every matching connection, up to the
    server safety cap of 10,000; `all` cannot be combined with
    `connection_ids`; any supplied `limit` is ignored in all mode, and
    the request returns `409 conflict` when the cap would be exceeded.

    Args:
        slug (str):
        id (str):
        idempotency_key (str | Unset):
        body (ManagedRealtimeDrainRequest): Bounded or explicitly all-matching auditable selection
            for closing live connections.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeDrainResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeDrainRequest,
    idempotency_key: str | Unset = UNSET,
) -> ManagedRealtimeDrainResponse | Problem | None:
    """Close a bounded or explicitly all-matching set of live managed realtime connections.

     Selects connections from a point-in-time fleet inventory by channel,
    principal, or explicit connection IDs. `dry_run` returns the selected
    connections without closing them. A non-dry-run request fails with
    `409 conflict` when the inventory is partial unless `allow_partial`
    is true; this prevents an unavailable node from making a drain look
    complete. Set `all` to select every matching connection, up to the
    server safety cap of 10,000; `all` cannot be combined with
    `connection_ids`; any supplied `limit` is ignored in all mode, and
    the request returns `409 conflict` when the cap would be exceeded.

    Args:
        slug (str):
        id (str):
        idempotency_key (str | Unset):
        body (ManagedRealtimeDrainRequest): Bounded or explicitly all-matching auditable selection
            for closing live connections.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeDrainResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
