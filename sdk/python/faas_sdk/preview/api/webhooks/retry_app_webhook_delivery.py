from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_webhook_retry_delivery_response import AppWebhookRetryDeliveryResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    did: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/webhooks/{id}/deliveries/{did}/retry".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            did=quote(str(did), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppWebhookRetryDeliveryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppWebhookRetryDeliveryResponse.from_dict(response.json())

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
) -> Response[AppWebhookRetryDeliveryResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    did: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppWebhookRetryDeliveryResponse | Problem]:
    """Re-arm a dead delivery.

     Resets the row to `pending` with attempt=0 +
    next_attempt_at=now() so the dispatcher picks it up at the
    next tick. Only valid on rows with status='dead'; returns
    400 app_webhook_invalid otherwise.

    Args:
        slug (str):
        id (str):
        did (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppWebhookRetryDeliveryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        did=did,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    did: str,
    *,
    client: AuthenticatedClient | Client,
) -> AppWebhookRetryDeliveryResponse | Problem | None:
    """Re-arm a dead delivery.

     Resets the row to `pending` with attempt=0 +
    next_attempt_at=now() so the dispatcher picks it up at the
    next tick. Only valid on rows with status='dead'; returns
    400 app_webhook_invalid otherwise.

    Args:
        slug (str):
        id (str):
        did (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppWebhookRetryDeliveryResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        did=did,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    did: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppWebhookRetryDeliveryResponse | Problem]:
    """Re-arm a dead delivery.

     Resets the row to `pending` with attempt=0 +
    next_attempt_at=now() so the dispatcher picks it up at the
    next tick. Only valid on rows with status='dead'; returns
    400 app_webhook_invalid otherwise.

    Args:
        slug (str):
        id (str):
        did (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppWebhookRetryDeliveryResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        did=did,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    did: str,
    *,
    client: AuthenticatedClient | Client,
) -> AppWebhookRetryDeliveryResponse | Problem | None:
    """Re-arm a dead delivery.

     Resets the row to `pending` with attempt=0 +
    next_attempt_at=now() so the dispatcher picks it up at the
    next tick. Only valid on rows with status='dead'; returns
    400 app_webhook_invalid otherwise.

    Args:
        slug (str):
        id (str):
        did (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppWebhookRetryDeliveryResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            did=did,
            client=client,
        )
    ).parsed
