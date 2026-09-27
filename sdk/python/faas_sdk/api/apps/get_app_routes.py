from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_routes_response import AppRoutesResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/routes".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppRoutesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppRoutesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[AppRoutesResponse | Problem]:
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
) -> Response[AppRoutesResponse | Problem]:
    r"""Per-route breakdown for opt-in apps (ADR-093).

     Returns the `routes` array of the per-app metrics surface.
    Production reads the fleet Prometheus aggregate; single-box
    development may use the gatewayd-internal loopback listener.
    The array is empty when `route_metrics_enabled` is false
    on the app (the gatewayd handler returns 200 + empty
    rows rather than 404 — the customer-facing \"feature off\"
    state is not a 404). Labels use declared templates when available,
    otherwise common numeric/UUID/long-hex segments become `{id}`.
    Unrecognized slug segments remain literal, and `__route_other__`
    marks the per-app metrics cardinality overflow.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppRoutesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> AppRoutesResponse | Problem | None:
    r"""Per-route breakdown for opt-in apps (ADR-093).

     Returns the `routes` array of the per-app metrics surface.
    Production reads the fleet Prometheus aggregate; single-box
    development may use the gatewayd-internal loopback listener.
    The array is empty when `route_metrics_enabled` is false
    on the app (the gatewayd handler returns 200 + empty
    rows rather than 404 — the customer-facing \"feature off\"
    state is not a 404). Labels use declared templates when available,
    otherwise common numeric/UUID/long-hex segments become `{id}`.
    Unrecognized slug segments remain literal, and `__route_other__`
    marks the per-app metrics cardinality overflow.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppRoutesResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppRoutesResponse | Problem]:
    r"""Per-route breakdown for opt-in apps (ADR-093).

     Returns the `routes` array of the per-app metrics surface.
    Production reads the fleet Prometheus aggregate; single-box
    development may use the gatewayd-internal loopback listener.
    The array is empty when `route_metrics_enabled` is false
    on the app (the gatewayd handler returns 200 + empty
    rows rather than 404 — the customer-facing \"feature off\"
    state is not a 404). Labels use declared templates when available,
    otherwise common numeric/UUID/long-hex segments become `{id}`.
    Unrecognized slug segments remain literal, and `__route_other__`
    marks the per-app metrics cardinality overflow.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppRoutesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> AppRoutesResponse | Problem | None:
    r"""Per-route breakdown for opt-in apps (ADR-093).

     Returns the `routes` array of the per-app metrics surface.
    Production reads the fleet Prometheus aggregate; single-box
    development may use the gatewayd-internal loopback listener.
    The array is empty when `route_metrics_enabled` is false
    on the app (the gatewayd handler returns 200 + empty
    rows rather than 404 — the customer-facing \"feature off\"
    state is not a 404). Labels use declared templates when available,
    otherwise common numeric/UUID/long-hex segments become `{id}`.
    Unrecognized slug segments remain literal, and `__route_other__`
    marks the per-app metrics cardinality overflow.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppRoutesResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
