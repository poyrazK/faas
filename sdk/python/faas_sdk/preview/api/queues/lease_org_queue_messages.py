from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.queue_lease_request import QueueLeaseRequest
from ...models.queue_lease_response import QueueLeaseResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    organization: str,
    slug: str,
    queue: str,
    *,
    body: QueueLeaseRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{organization}/apps/{slug}/queues/{queue}/leases".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
            queue=quote(str(queue), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | QueueLeaseResponse | None:
    if response.status_code == 200:
        response_200 = QueueLeaseResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | QueueLeaseResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    slug: str,
    queue: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueLeaseRequest | Unset = UNSET,
) -> Response[Problem | QueueLeaseResponse]:
    """Organization queues: Lease ready messages from a pull-bound queue

     Organization queues: Lease ready messages from a pull-bound queue. Owners, admins and developers can
    configure and operate queues. Requires current membership and the corresponding credential scope.
    Mutations require organization billing adoption. IDs, messages, retries and receipts survive creator
    erasure. Leases up to `max_messages` ready messages from a queue with an
    enabled pull binding on this app. Each message stays hidden for the
    visibility timeout and is redelivered with a new attempt if it is not
    acked, nacked or extended in time. Unexpired leases never exceed the
    binding's `max_concurrency`. An empty list means nothing is ready;
    poll again with back-off. Authenticate with an API key carrying
    `queues:consume`, `deploy:write` or `admin`, or with a workload
    identity token for audience `gregale:queues` from a live instance of
    this app.

    Args:
        organization (str):
        slug (str):
        queue (str):
        body (QueueLeaseRequest | Unset): ADR-1140 pull lease. Zero values lease one message for
            the default visibility timeout (30s) and return at once.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueLeaseResponse]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        queue=queue,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    slug: str,
    queue: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueLeaseRequest | Unset = UNSET,
) -> Problem | QueueLeaseResponse | None:
    """Organization queues: Lease ready messages from a pull-bound queue

     Organization queues: Lease ready messages from a pull-bound queue. Owners, admins and developers can
    configure and operate queues. Requires current membership and the corresponding credential scope.
    Mutations require organization billing adoption. IDs, messages, retries and receipts survive creator
    erasure. Leases up to `max_messages` ready messages from a queue with an
    enabled pull binding on this app. Each message stays hidden for the
    visibility timeout and is redelivered with a new attempt if it is not
    acked, nacked or extended in time. Unexpired leases never exceed the
    binding's `max_concurrency`. An empty list means nothing is ready;
    poll again with back-off. Authenticate with an API key carrying
    `queues:consume`, `deploy:write` or `admin`, or with a workload
    identity token for audience `gregale:queues` from a live instance of
    this app.

    Args:
        organization (str):
        slug (str):
        queue (str):
        body (QueueLeaseRequest | Unset): ADR-1140 pull lease. Zero values lease one message for
            the default visibility timeout (30s) and return at once.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueLeaseResponse
    """

    return sync_detailed(
        organization=organization,
        slug=slug,
        queue=queue,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    queue: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueLeaseRequest | Unset = UNSET,
) -> Response[Problem | QueueLeaseResponse]:
    """Organization queues: Lease ready messages from a pull-bound queue

     Organization queues: Lease ready messages from a pull-bound queue. Owners, admins and developers can
    configure and operate queues. Requires current membership and the corresponding credential scope.
    Mutations require organization billing adoption. IDs, messages, retries and receipts survive creator
    erasure. Leases up to `max_messages` ready messages from a queue with an
    enabled pull binding on this app. Each message stays hidden for the
    visibility timeout and is redelivered with a new attempt if it is not
    acked, nacked or extended in time. Unexpired leases never exceed the
    binding's `max_concurrency`. An empty list means nothing is ready;
    poll again with back-off. Authenticate with an API key carrying
    `queues:consume`, `deploy:write` or `admin`, or with a workload
    identity token for audience `gregale:queues` from a live instance of
    this app.

    Args:
        organization (str):
        slug (str):
        queue (str):
        body (QueueLeaseRequest | Unset): ADR-1140 pull lease. Zero values lease one message for
            the default visibility timeout (30s) and return at once.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueLeaseResponse]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        queue=queue,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    queue: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueLeaseRequest | Unset = UNSET,
) -> Problem | QueueLeaseResponse | None:
    """Organization queues: Lease ready messages from a pull-bound queue

     Organization queues: Lease ready messages from a pull-bound queue. Owners, admins and developers can
    configure and operate queues. Requires current membership and the corresponding credential scope.
    Mutations require organization billing adoption. IDs, messages, retries and receipts survive creator
    erasure. Leases up to `max_messages` ready messages from a queue with an
    enabled pull binding on this app. Each message stays hidden for the
    visibility timeout and is redelivered with a new attempt if it is not
    acked, nacked or extended in time. Unexpired leases never exceed the
    binding's `max_concurrency`. An empty list means nothing is ready;
    poll again with back-off. Authenticate with an API key carrying
    `queues:consume`, `deploy:write` or `admin`, or with a workload
    identity token for audience `gregale:queues` from a live instance of
    this app.

    Args:
        organization (str):
        slug (str):
        queue (str):
        body (QueueLeaseRequest | Unset): ADR-1140 pull lease. Zero values lease one message for
            the default visibility timeout (30s) and return at once.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueLeaseResponse
    """

    return (
        await asyncio_detailed(
            organization=organization,
            slug=slug,
            queue=queue,
            client=client,
            body=body,
        )
    ).parsed
