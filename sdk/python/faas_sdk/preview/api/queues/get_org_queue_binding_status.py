from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.queue_binding_status_response import QueueBindingStatusResponse
from ...types import Response


def _get_kwargs(
    organization: str,
    slug: str,
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/apps/{slug}/queue-bindings/{id}/status".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | QueueBindingStatusResponse | None:
    if response.status_code == 200:
        response_200 = QueueBindingStatusResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | QueueBindingStatusResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | QueueBindingStatusResponse]:
    """Organization queues: Read queue binding consumer state and queue counters

     Organization queues: Read queue binding consumer state and queue counters. Owners, admins,
    developers and viewers can read metadata. Requires current membership and the corresponding
    credential scope. Mutations require organization billing adoption. IDs, messages, retries and
    receipts survive creator erasure. Returns the durable push-consumer projection and binding-scoped
    queue counters. `consumer_state` is control-plane state
    (`active`, `paused`, `not_configured`, or `external`), while
    `consumer_liveness` and the poll timestamps expose the scheduler's
    last-known health. A push snapshot is stale after 30 seconds without
    a completed poll; pull bindings report `external` liveness.

    Args:
        organization (str):
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueBindingStatusResponse]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | QueueBindingStatusResponse | None:
    """Organization queues: Read queue binding consumer state and queue counters

     Organization queues: Read queue binding consumer state and queue counters. Owners, admins,
    developers and viewers can read metadata. Requires current membership and the corresponding
    credential scope. Mutations require organization billing adoption. IDs, messages, retries and
    receipts survive creator erasure. Returns the durable push-consumer projection and binding-scoped
    queue counters. `consumer_state` is control-plane state
    (`active`, `paused`, `not_configured`, or `external`), while
    `consumer_liveness` and the poll timestamps expose the scheduler's
    last-known health. A push snapshot is stale after 30 seconds without
    a completed poll; pull bindings report `external` liveness.

    Args:
        organization (str):
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueBindingStatusResponse
    """

    return sync_detailed(
        organization=organization,
        slug=slug,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | QueueBindingStatusResponse]:
    """Organization queues: Read queue binding consumer state and queue counters

     Organization queues: Read queue binding consumer state and queue counters. Owners, admins,
    developers and viewers can read metadata. Requires current membership and the corresponding
    credential scope. Mutations require organization billing adoption. IDs, messages, retries and
    receipts survive creator erasure. Returns the durable push-consumer projection and binding-scoped
    queue counters. `consumer_state` is control-plane state
    (`active`, `paused`, `not_configured`, or `external`), while
    `consumer_liveness` and the poll timestamps expose the scheduler's
    last-known health. A push snapshot is stale after 30 seconds without
    a completed poll; pull bindings report `external` liveness.

    Args:
        organization (str):
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueBindingStatusResponse]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | QueueBindingStatusResponse | None:
    """Organization queues: Read queue binding consumer state and queue counters

     Organization queues: Read queue binding consumer state and queue counters. Owners, admins,
    developers and viewers can read metadata. Requires current membership and the corresponding
    credential scope. Mutations require organization billing adoption. IDs, messages, retries and
    receipts survive creator erasure. Returns the durable push-consumer projection and binding-scoped
    queue counters. `consumer_state` is control-plane state
    (`active`, `paused`, `not_configured`, or `external`), while
    `consumer_liveness` and the poll timestamps expose the scheduler's
    last-known health. A push snapshot is stale after 30 seconds without
    a completed poll; pull bindings report `external` liveness.

    Args:
        organization (str):
        slug (str):
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueBindingStatusResponse
    """

    return (
        await asyncio_detailed(
            organization=organization,
            slug=slug,
            id=id,
            client=client,
        )
    ).parsed
