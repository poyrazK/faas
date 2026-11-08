from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.queue_send_batch_request import QueueSendBatchRequest
from ...models.queue_send_batch_response import QueueSendBatchResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    organization: str,
    slug: str,
    *,
    body: QueueSendBatchRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{organization}/apps/{slug}/queues/send-batch".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | QueueSendBatchResponse | None:
    if response.status_code == 200:
        response_200 = QueueSendBatchResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | QueueSendBatchResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueSendBatchRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | QueueSendBatchResponse]:
    """Organization queues: Send up to 100 messages to one queue as one request

     Organization queues: Send up to 100 messages to one queue as one request. Owners, admins and
    developers can configure and operate queues. Requires current membership and the corresponding
    credential scope. Mutations require organization billing adoption. The first refused message ends
    the batch; resend from the first message that was not accepted. API keys require `queues:send`,
    `deploy:write`, or `admin`.

    Args:
        organization (str):
        slug (str):
        idempotency_key (str | Unset):
        body (QueueSendBatchRequest): Body for POST /v1/apps/{slug}/queues/send-batch: up to 100
            messages for one queue, sent in order. The body is at most 8 MiB; each message meets its
            plan's message size.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueSendBatchResponse]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueSendBatchRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | QueueSendBatchResponse | None:
    """Organization queues: Send up to 100 messages to one queue as one request

     Organization queues: Send up to 100 messages to one queue as one request. Owners, admins and
    developers can configure and operate queues. Requires current membership and the corresponding
    credential scope. Mutations require organization billing adoption. The first refused message ends
    the batch; resend from the first message that was not accepted. API keys require `queues:send`,
    `deploy:write`, or `admin`.

    Args:
        organization (str):
        slug (str):
        idempotency_key (str | Unset):
        body (QueueSendBatchRequest): Body for POST /v1/apps/{slug}/queues/send-batch: up to 100
            messages for one queue, sent in order. The body is at most 8 MiB; each message meets its
            plan's message size.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueSendBatchResponse
    """

    return sync_detailed(
        organization=organization,
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueSendBatchRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | QueueSendBatchResponse]:
    """Organization queues: Send up to 100 messages to one queue as one request

     Organization queues: Send up to 100 messages to one queue as one request. Owners, admins and
    developers can configure and operate queues. Requires current membership and the corresponding
    credential scope. Mutations require organization billing adoption. The first refused message ends
    the batch; resend from the first message that was not accepted. API keys require `queues:send`,
    `deploy:write`, or `admin`.

    Args:
        organization (str):
        slug (str):
        idempotency_key (str | Unset):
        body (QueueSendBatchRequest): Body for POST /v1/apps/{slug}/queues/send-batch: up to 100
            messages for one queue, sent in order. The body is at most 8 MiB; each message meets its
            plan's message size.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueSendBatchResponse]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueSendBatchRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | QueueSendBatchResponse | None:
    """Organization queues: Send up to 100 messages to one queue as one request

     Organization queues: Send up to 100 messages to one queue as one request. Owners, admins and
    developers can configure and operate queues. Requires current membership and the corresponding
    credential scope. Mutations require organization billing adoption. The first refused message ends
    the batch; resend from the first message that was not accepted. API keys require `queues:send`,
    `deploy:write`, or `admin`.

    Args:
        organization (str):
        slug (str):
        idempotency_key (str | Unset):
        body (QueueSendBatchRequest): Body for POST /v1/apps/{slug}/queues/send-batch: up to 100
            messages for one queue, sent in order. The body is at most 8 MiB; each message meets its
            plan's message size.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueSendBatchResponse
    """

    return (
        await asyncio_detailed(
            organization=organization,
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
