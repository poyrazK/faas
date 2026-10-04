from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.webhook_automation_receipt_response import WebhookAutomationReceiptResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    event_id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/inbound-webhooks/{id}/automation-receipts/{event_id}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            event_id=quote(str(event_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WebhookAutomationReceiptResponse | None:
    if response.status_code == 200:
        response_200 = WebhookAutomationReceiptResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[Problem | WebhookAutomationReceiptResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    event_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | WebhookAutomationReceiptResponse]:
    """Inspect a verified provider event and its automation admission.

     Returns the captured decision and scheduler progress for a retained provider event.

    Args:
        slug (str):
        id (UUID):
        event_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WebhookAutomationReceiptResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        event_id=event_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    event_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | WebhookAutomationReceiptResponse | None:
    """Inspect a verified provider event and its automation admission.

     Returns the captured decision and scheduler progress for a retained provider event.

    Args:
        slug (str):
        id (UUID):
        event_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WebhookAutomationReceiptResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        event_id=event_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    event_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | WebhookAutomationReceiptResponse]:
    """Inspect a verified provider event and its automation admission.

     Returns the captured decision and scheduler progress for a retained provider event.

    Args:
        slug (str):
        id (UUID):
        event_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WebhookAutomationReceiptResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        event_id=event_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    event_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | WebhookAutomationReceiptResponse | None:
    """Inspect a verified provider event and its automation admission.

     Returns the captured decision and scheduler progress for a retained provider event.

    Args:
        slug (str):
        id (UUID):
        event_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WebhookAutomationReceiptResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            event_id=event_id,
            client=client,
        )
    ).parsed
