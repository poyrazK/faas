from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_rate_card_response import APIConsumerRateCardResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    rate_card_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/rate-cards/{rate_card_id}".format(
            slug=quote(str(slug), safe=""),
            rate_card_id=quote(str(rate_card_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerRateCardResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APIConsumerRateCardResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APIConsumerRateCardResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    rate_card_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[APIConsumerRateCardResponse | Problem]:
    """Fetch one API consumer rate card.

    Args:
        slug (str):
        rate_card_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerRateCardResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        rate_card_id=rate_card_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    rate_card_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> APIConsumerRateCardResponse | Problem | None:
    """Fetch one API consumer rate card.

    Args:
        slug (str):
        rate_card_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerRateCardResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        rate_card_id=rate_card_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    rate_card_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[APIConsumerRateCardResponse | Problem]:
    """Fetch one API consumer rate card.

    Args:
        slug (str):
        rate_card_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerRateCardResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        rate_card_id=rate_card_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    rate_card_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> APIConsumerRateCardResponse | Problem | None:
    """Fetch one API consumer rate card.

    Args:
        slug (str):
        rate_card_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerRateCardResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            rate_card_id=rate_card_id,
            client=client,
        )
    ).parsed
