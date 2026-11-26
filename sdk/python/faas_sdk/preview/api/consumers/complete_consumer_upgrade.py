from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.complete_consumer_upgrade_request import CompleteConsumerUpgradeRequest
from ...models.consumer_upgrade_result import ConsumerUpgradeResult
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CompleteConsumerUpgradeRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/consumer-signup/{slug}/upgrade/complete".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ConsumerUpgradeResult | Problem | None:
    if response.status_code == 200:
        response_200 = ConsumerUpgradeResult.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ConsumerUpgradeResult | Problem]:
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
    body: CompleteConsumerUpgradeRequest,
) -> Response[ConsumerUpgradeResult | Problem]:
    """Finish an upgrade once Checkout saved the card.

     Needs no credential: the upgrade link's token and the complete token
    from the started upgrade prove it. Makes the new Stripe customer the
    consumer's external_ref and changes the plan from the next minute.

    Args:
        slug (str):
        body (CompleteConsumerUpgradeRequest): Finishes an upgrade after Checkout saved the card.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConsumerUpgradeResult | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CompleteConsumerUpgradeRequest,
) -> ConsumerUpgradeResult | Problem | None:
    """Finish an upgrade once Checkout saved the card.

     Needs no credential: the upgrade link's token and the complete token
    from the started upgrade prove it. Makes the new Stripe customer the
    consumer's external_ref and changes the plan from the next minute.

    Args:
        slug (str):
        body (CompleteConsumerUpgradeRequest): Finishes an upgrade after Checkout saved the card.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConsumerUpgradeResult | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CompleteConsumerUpgradeRequest,
) -> Response[ConsumerUpgradeResult | Problem]:
    """Finish an upgrade once Checkout saved the card.

     Needs no credential: the upgrade link's token and the complete token
    from the started upgrade prove it. Makes the new Stripe customer the
    consumer's external_ref and changes the plan from the next minute.

    Args:
        slug (str):
        body (CompleteConsumerUpgradeRequest): Finishes an upgrade after Checkout saved the card.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConsumerUpgradeResult | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CompleteConsumerUpgradeRequest,
) -> ConsumerUpgradeResult | Problem | None:
    """Finish an upgrade once Checkout saved the card.

     Needs no credential: the upgrade link's token and the complete token
    from the started upgrade prove it. Makes the new Stripe customer the
    consumer's external_ref and changes the plan from the next minute.

    Args:
        slug (str):
        body (CompleteConsumerUpgradeRequest): Finishes an upgrade after Checkout saved the card.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConsumerUpgradeResult | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
