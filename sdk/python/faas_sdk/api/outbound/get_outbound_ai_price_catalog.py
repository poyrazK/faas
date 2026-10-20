from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_outbound_ai_price_catalog_provider import (
    GetOutboundAIPriceCatalogProvider,
)
from ...models.outbound_ai_price_catalog_response import OutboundAIPriceCatalogResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    provider: GetOutboundAIPriceCatalogProvider,
    model: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_provider: str = provider
    params["provider"] = json_provider

    params["model"] = model

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/outbound/ai-price-catalog",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundAIPriceCatalogResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OutboundAIPriceCatalogResponse.from_dict(response.json())

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

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OutboundAIPriceCatalogResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    provider: GetOutboundAIPriceCatalogProvider,
    model: str | Unset = UNSET,
) -> Response[OutboundAIPriceCatalogResponse | Problem]:
    """Read the built-in AI price catalog of one provider.

     Requires MFA and read scope (ADR-994). The catalog prices the models an integration's own price list
    does not name. Prices are the providers' list prices in US dollars, rounded up (ADR 20261019123822).

    Args:
        provider (GetOutboundAIPriceCatalogProvider):
        model (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIPriceCatalogResponse | Problem]
    """

    kwargs = _get_kwargs(
        provider=provider,
        model=model,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    provider: GetOutboundAIPriceCatalogProvider,
    model: str | Unset = UNSET,
) -> OutboundAIPriceCatalogResponse | Problem | None:
    """Read the built-in AI price catalog of one provider.

     Requires MFA and read scope (ADR-994). The catalog prices the models an integration's own price list
    does not name. Prices are the providers' list prices in US dollars, rounded up (ADR 20261019123822).

    Args:
        provider (GetOutboundAIPriceCatalogProvider):
        model (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIPriceCatalogResponse | Problem
    """

    return sync_detailed(
        client=client,
        provider=provider,
        model=model,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    provider: GetOutboundAIPriceCatalogProvider,
    model: str | Unset = UNSET,
) -> Response[OutboundAIPriceCatalogResponse | Problem]:
    """Read the built-in AI price catalog of one provider.

     Requires MFA and read scope (ADR-994). The catalog prices the models an integration's own price list
    does not name. Prices are the providers' list prices in US dollars, rounded up (ADR 20261019123822).

    Args:
        provider (GetOutboundAIPriceCatalogProvider):
        model (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIPriceCatalogResponse | Problem]
    """

    kwargs = _get_kwargs(
        provider=provider,
        model=model,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    provider: GetOutboundAIPriceCatalogProvider,
    model: str | Unset = UNSET,
) -> OutboundAIPriceCatalogResponse | Problem | None:
    """Read the built-in AI price catalog of one provider.

     Requires MFA and read scope (ADR-994). The catalog prices the models an integration's own price list
    does not name. Prices are the providers' list prices in US dollars, rounded up (ADR 20261019123822).

    Args:
        provider (GetOutboundAIPriceCatalogProvider):
        model (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIPriceCatalogResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            provider=provider,
            model=model,
        )
    ).parsed
