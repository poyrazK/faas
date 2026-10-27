from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_signal_receipt import OperationSignalReceipt
from ...models.operation_signal_request import OperationSignalRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: OperationSignalRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/operations/{id}/signals".format(
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
) -> OperationSignalReceipt | Problem | None:
    if response.status_code == 200:
        response_200 = OperationSignalReceipt.from_dict(response.json())

        return response_200

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[OperationSignalReceipt | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: OperationSignalRequest,
) -> Response[OperationSignalReceipt | Problem]:
    """Deliver a named event to an operation that waits for it.

     Requires deploy:write and MFA. The event must be one the operation's workflow waits for. signal_id
    makes delivery idempotent per operation; a repeat with the same event and payload returns the
    original receipt, a different event or payload under it conflicts. A signal that arrives before its
    wait is held until the wait starts. Settled operations take no new signals.

    Args:
        slug (str):
        id (UUID):
        body (OperationSignalRequest): One named event for an operation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationSignalReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: OperationSignalRequest,
) -> OperationSignalReceipt | Problem | None:
    """Deliver a named event to an operation that waits for it.

     Requires deploy:write and MFA. The event must be one the operation's workflow waits for. signal_id
    makes delivery idempotent per operation; a repeat with the same event and payload returns the
    original receipt, a different event or payload under it conflicts. A signal that arrives before its
    wait is held until the wait starts. Settled operations take no new signals.

    Args:
        slug (str):
        id (UUID):
        body (OperationSignalRequest): One named event for an operation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationSignalReceipt | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: OperationSignalRequest,
) -> Response[OperationSignalReceipt | Problem]:
    """Deliver a named event to an operation that waits for it.

     Requires deploy:write and MFA. The event must be one the operation's workflow waits for. signal_id
    makes delivery idempotent per operation; a repeat with the same event and payload returns the
    original receipt, a different event or payload under it conflicts. A signal that arrives before its
    wait is held until the wait starts. Settled operations take no new signals.

    Args:
        slug (str):
        id (UUID):
        body (OperationSignalRequest): One named event for an operation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationSignalReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: OperationSignalRequest,
) -> OperationSignalReceipt | Problem | None:
    """Deliver a named event to an operation that waits for it.

     Requires deploy:write and MFA. The event must be one the operation's workflow waits for. signal_id
    makes delivery idempotent per operation; a repeat with the same event and payload returns the
    original receipt, a different event or payload under it conflicts. A signal that arrives before its
    wait is held until the wait starts. Settled operations take no new signals.

    Args:
        slug (str):
        id (UUID):
        body (OperationSignalRequest): One named event for an operation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationSignalReceipt | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
