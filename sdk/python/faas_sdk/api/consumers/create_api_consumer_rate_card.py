from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_rate_card_response import APIConsumerRateCardResponse
from ...models.create_api_consumer_rate_card_request import CreateAPIConsumerRateCardRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: CreateAPIConsumerRateCardRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/rate-cards".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerRateCardResponse | Problem | None:
    if response.status_code == 201:
        response_201 = APIConsumerRateCardResponse.from_dict(response.json())

        return response_201

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
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerRateCardRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[APIConsumerRateCardResponse | Problem]:
    """Publish a new immutable API consumer rate card.

     Publishes an app-level request price. Rate cards are append-only and
    must use one currency per app. If effective_from is omitted, the card
    starts at the next UTC minute.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateAPIConsumerRateCardRequest): Immutable app-level request price. Currency
            defaults to EUR and effective_from defaults to the next UTC minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerRateCardResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerRateCardRequest,
    idempotency_key: str | Unset = UNSET,
) -> APIConsumerRateCardResponse | Problem | None:
    """Publish a new immutable API consumer rate card.

     Publishes an app-level request price. Rate cards are append-only and
    must use one currency per app. If effective_from is omitted, the card
    starts at the next UTC minute.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateAPIConsumerRateCardRequest): Immutable app-level request price. Currency
            defaults to EUR and effective_from defaults to the next UTC minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerRateCardResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerRateCardRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[APIConsumerRateCardResponse | Problem]:
    """Publish a new immutable API consumer rate card.

     Publishes an app-level request price. Rate cards are append-only and
    must use one currency per app. If effective_from is omitted, the card
    starts at the next UTC minute.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateAPIConsumerRateCardRequest): Immutable app-level request price. Currency
            defaults to EUR and effective_from defaults to the next UTC minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerRateCardResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerRateCardRequest,
    idempotency_key: str | Unset = UNSET,
) -> APIConsumerRateCardResponse | Problem | None:
    """Publish a new immutable API consumer rate card.

     Publishes an app-level request price. Rate cards are append-only and
    must use one currency per app. If effective_from is omitted, the card
    starts at the next UTC minute.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateAPIConsumerRateCardRequest): Immutable app-level request price. Currency
            defaults to EUR and effective_from defaults to the next UTC minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerRateCardResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
