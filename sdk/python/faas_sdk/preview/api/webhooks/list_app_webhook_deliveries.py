from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_webhook_delivery_list_response import AppWebhookDeliveryListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: str,
    *,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["page_size"] = page_size

    params["page_token"] = page_token

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/webhooks/{id}/deliveries".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppWebhookDeliveryListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppWebhookDeliveryListResponse.from_dict(response.json())

        return response_200

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
) -> Response[AppWebhookDeliveryListResponse | Problem]:
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
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
) -> Response[AppWebhookDeliveryListResponse | Problem]:
    """List recent deliveries for this webhook.

     Cursor-paginated; most-recent-first. The delivery status
    follows the closed vocabulary `pending | in_flight |
    succeeded | failed | dead`. Dead rows can be re-armed with
    POST /webhooks/{id}/deliveries/{did}/retry.

    Args:
        slug (str):
        id (str):
        page_size (int | Unset):  Default: 50.
        page_token (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppWebhookDeliveryListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        page_size=page_size,
        page_token=page_token,
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
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
) -> AppWebhookDeliveryListResponse | Problem | None:
    """List recent deliveries for this webhook.

     Cursor-paginated; most-recent-first. The delivery status
    follows the closed vocabulary `pending | in_flight |
    succeeded | failed | dead`. Dead rows can be re-armed with
    POST /webhooks/{id}/deliveries/{did}/retry.

    Args:
        slug (str):
        id (str):
        page_size (int | Unset):  Default: 50.
        page_token (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppWebhookDeliveryListResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        page_size=page_size,
        page_token=page_token,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
) -> Response[AppWebhookDeliveryListResponse | Problem]:
    """List recent deliveries for this webhook.

     Cursor-paginated; most-recent-first. The delivery status
    follows the closed vocabulary `pending | in_flight |
    succeeded | failed | dead`. Dead rows can be re-armed with
    POST /webhooks/{id}/deliveries/{did}/retry.

    Args:
        slug (str):
        id (str):
        page_size (int | Unset):  Default: 50.
        page_token (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppWebhookDeliveryListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        page_size=page_size,
        page_token=page_token,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
) -> AppWebhookDeliveryListResponse | Problem | None:
    """List recent deliveries for this webhook.

     Cursor-paginated; most-recent-first. The delivery status
    follows the closed vocabulary `pending | in_flight |
    succeeded | failed | dead`. Dead rows can be re-armed with
    POST /webhooks/{id}/deliveries/{did}/retry.

    Args:
        slug (str):
        id (str):
        page_size (int | Unset):  Default: 50.
        page_token (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppWebhookDeliveryListResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            page_size=page_size,
            page_token=page_token,
        )
    ).parsed
