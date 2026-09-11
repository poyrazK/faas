from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.capabilities_response import CapabilitiesResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/capabilities",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CapabilitiesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CapabilitiesResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[CapabilitiesResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[CapabilitiesResponse | Problem]:
    """Return feature maturity and plan availability.

     Returns the canonical, account-scoped capability registry. `maturity`
    describes the product lifecycle state; `plans` lists entitled plans
    and `enabled` resolves that list for the caller. The registry is read-only and
    is suitable for CLIs, dashboards, and deployment preflight checks.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CapabilitiesResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> CapabilitiesResponse | Problem | None:
    """Return feature maturity and plan availability.

     Returns the canonical, account-scoped capability registry. `maturity`
    describes the product lifecycle state; `plans` lists entitled plans
    and `enabled` resolves that list for the caller. The registry is read-only and
    is suitable for CLIs, dashboards, and deployment preflight checks.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CapabilitiesResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[CapabilitiesResponse | Problem]:
    """Return feature maturity and plan availability.

     Returns the canonical, account-scoped capability registry. `maturity`
    describes the product lifecycle state; `plans` lists entitled plans
    and `enabled` resolves that list for the caller. The registry is read-only and
    is suitable for CLIs, dashboards, and deployment preflight checks.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CapabilitiesResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> CapabilitiesResponse | Problem | None:
    """Return feature maturity and plan availability.

     Returns the canonical, account-scoped capability registry. `maturity`
    describes the product lifecycle state; `plans` lists entitled plans
    and `enabled` resolves that list for the caller. The registry is read-only and
    is suitable for CLIs, dashboards, and deployment preflight checks.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CapabilitiesResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
