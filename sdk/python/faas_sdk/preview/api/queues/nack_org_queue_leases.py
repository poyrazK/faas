from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.queue_lease_receipts_request import QueueLeaseReceiptsRequest
from ...models.queue_lease_receipts_response import QueueLeaseReceiptsResponse
from ...types import Response


def _get_kwargs(
    organization: str,
    slug: str,
    queue: str,
    *,
    body: QueueLeaseReceiptsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{organization}/apps/{slug}/queues/{queue}/leases:nack".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
            queue=quote(str(queue), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | QueueLeaseReceiptsResponse | None:
    if response.status_code == 200:
        response_200 = QueueLeaseReceiptsResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | QueueLeaseReceiptsResponse]:
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
    body: QueueLeaseReceiptsRequest,
) -> Response[Problem | QueueLeaseReceiptsResponse]:
    """Organization queues: Return leased messages for a retry

     Organization queues: Return leased messages for a retry. Owners, admins and developers can configure
    and operate queues. Requires current membership and the corresponding credential scope. Mutations
    require organization billing adoption. IDs, messages, retries and receipts survive creator erasure.
    Applies the message's retry policy and the plan's attempt budget:
    the message returns to the queue after the back-off (or after
    `delay_seconds` if later), or moves to the dead-letter queue once the
    budget is spent. `error` is recorded as the attempt's last error.

    Args:
        organization (str):
        slug (str):
        queue (str):
        body (QueueLeaseReceiptsRequest): Receipts for ack, nack or extend. delay_seconds and
            error apply to nack; visibility_timeout_seconds to extend.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueLeaseReceiptsResponse]
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
    body: QueueLeaseReceiptsRequest,
) -> Problem | QueueLeaseReceiptsResponse | None:
    """Organization queues: Return leased messages for a retry

     Organization queues: Return leased messages for a retry. Owners, admins and developers can configure
    and operate queues. Requires current membership and the corresponding credential scope. Mutations
    require organization billing adoption. IDs, messages, retries and receipts survive creator erasure.
    Applies the message's retry policy and the plan's attempt budget:
    the message returns to the queue after the back-off (or after
    `delay_seconds` if later), or moves to the dead-letter queue once the
    budget is spent. `error` is recorded as the attempt's last error.

    Args:
        organization (str):
        slug (str):
        queue (str):
        body (QueueLeaseReceiptsRequest): Receipts for ack, nack or extend. delay_seconds and
            error apply to nack; visibility_timeout_seconds to extend.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueLeaseReceiptsResponse
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
    body: QueueLeaseReceiptsRequest,
) -> Response[Problem | QueueLeaseReceiptsResponse]:
    """Organization queues: Return leased messages for a retry

     Organization queues: Return leased messages for a retry. Owners, admins and developers can configure
    and operate queues. Requires current membership and the corresponding credential scope. Mutations
    require organization billing adoption. IDs, messages, retries and receipts survive creator erasure.
    Applies the message's retry policy and the plan's attempt budget:
    the message returns to the queue after the back-off (or after
    `delay_seconds` if later), or moves to the dead-letter queue once the
    budget is spent. `error` is recorded as the attempt's last error.

    Args:
        organization (str):
        slug (str):
        queue (str):
        body (QueueLeaseReceiptsRequest): Receipts for ack, nack or extend. delay_seconds and
            error apply to nack; visibility_timeout_seconds to extend.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueLeaseReceiptsResponse]
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
    body: QueueLeaseReceiptsRequest,
) -> Problem | QueueLeaseReceiptsResponse | None:
    """Organization queues: Return leased messages for a retry

     Organization queues: Return leased messages for a retry. Owners, admins and developers can configure
    and operate queues. Requires current membership and the corresponding credential scope. Mutations
    require organization billing adoption. IDs, messages, retries and receipts survive creator erasure.
    Applies the message's retry policy and the plan's attempt budget:
    the message returns to the queue after the back-off (or after
    `delay_seconds` if later), or moves to the dead-letter queue once the
    budget is spent. `error` is recorded as the attempt's last error.

    Args:
        organization (str):
        slug (str):
        queue (str):
        body (QueueLeaseReceiptsRequest): Receipts for ack, nack or extend. delay_seconds and
            error apply to nack; visibility_timeout_seconds to extend.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueLeaseReceiptsResponse
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
