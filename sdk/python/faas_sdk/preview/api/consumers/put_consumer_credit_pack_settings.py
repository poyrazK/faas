from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.consumer_credit_pack_settings import ConsumerCreditPackSettings
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: ConsumerCreditPackSettings,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/consumer-credit-packs".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ConsumerCreditPackSettings | Problem | None:
    if response.status_code == 200:
        response_200 = ConsumerCreditPackSettings.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ConsumerCreditPackSettings | Problem]:
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
    body: ConsumerCreditPackSettings,
) -> Response[ConsumerCreditPackSettings | Problem]:
    """Set the fixed-value packs customers can buy through the app owner's Stripe.

     An empty pack list disables new purchases. Existing checkout sessions
    keep the terms saved when the purchase started.

    Args:
        slug (str):
        body (ConsumerCreditPackSettings): The packs offered by an app and the billing page where
            buyers return.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConsumerCreditPackSettings | Problem]
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
    body: ConsumerCreditPackSettings,
) -> ConsumerCreditPackSettings | Problem | None:
    """Set the fixed-value packs customers can buy through the app owner's Stripe.

     An empty pack list disables new purchases. Existing checkout sessions
    keep the terms saved when the purchase started.

    Args:
        slug (str):
        body (ConsumerCreditPackSettings): The packs offered by an app and the billing page where
            buyers return.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConsumerCreditPackSettings | Problem
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
    body: ConsumerCreditPackSettings,
) -> Response[ConsumerCreditPackSettings | Problem]:
    """Set the fixed-value packs customers can buy through the app owner's Stripe.

     An empty pack list disables new purchases. Existing checkout sessions
    keep the terms saved when the purchase started.

    Args:
        slug (str):
        body (ConsumerCreditPackSettings): The packs offered by an app and the billing page where
            buyers return.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConsumerCreditPackSettings | Problem]
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
    body: ConsumerCreditPackSettings,
) -> ConsumerCreditPackSettings | Problem | None:
    """Set the fixed-value packs customers can buy through the app owner's Stripe.

     An empty pack list disables new purchases. Existing checkout sessions
    keep the terms saved when the purchase started.

    Args:
        slug (str):
        body (ConsumerCreditPackSettings): The packs offered by an app and the billing page where
            buyers return.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConsumerCreditPackSettings | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
