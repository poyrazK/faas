from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_static_egress_ip_response import AppStaticEgressIPResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/static-egress-ip".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppStaticEgressIPResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppStaticEgressIPResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppStaticEgressIPResponse | Problem]:
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
) -> Response[AppStaticEgressIPResponse | Problem]:
    """Read the per-app static egress IP pin (ADR-119).

     Returns the customer's pinned IPv4 + the audit timestamp +
    the per-app quota cap (StaticEgressIPsPerApp, 1 in v1). A
    Scale customer with no pin yet sees `ip=null`,
    `set_at=null`, `plan_cap=1`, `plan_allowed=true`. Free /
    Hobby / Pro return `plan_allowed=false` so the CLI can
    render the upsell without a separate plan lookup.

    Mounted with the standard auth chain (no MFA, no admin
    scope — the customer owns the pin).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppStaticEgressIPResponse | Problem]
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
) -> AppStaticEgressIPResponse | Problem | None:
    """Read the per-app static egress IP pin (ADR-119).

     Returns the customer's pinned IPv4 + the audit timestamp +
    the per-app quota cap (StaticEgressIPsPerApp, 1 in v1). A
    Scale customer with no pin yet sees `ip=null`,
    `set_at=null`, `plan_cap=1`, `plan_allowed=true`. Free /
    Hobby / Pro return `plan_allowed=false` so the CLI can
    render the upsell without a separate plan lookup.

    Mounted with the standard auth chain (no MFA, no admin
    scope — the customer owns the pin).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppStaticEgressIPResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppStaticEgressIPResponse | Problem]:
    """Read the per-app static egress IP pin (ADR-119).

     Returns the customer's pinned IPv4 + the audit timestamp +
    the per-app quota cap (StaticEgressIPsPerApp, 1 in v1). A
    Scale customer with no pin yet sees `ip=null`,
    `set_at=null`, `plan_cap=1`, `plan_allowed=true`. Free /
    Hobby / Pro return `plan_allowed=false` so the CLI can
    render the upsell without a separate plan lookup.

    Mounted with the standard auth chain (no MFA, no admin
    scope — the customer owns the pin).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppStaticEgressIPResponse | Problem]
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
) -> AppStaticEgressIPResponse | Problem | None:
    """Read the per-app static egress IP pin (ADR-119).

     Returns the customer's pinned IPv4 + the audit timestamp +
    the per-app quota cap (StaticEgressIPsPerApp, 1 in v1). A
    Scale customer with no pin yet sees `ip=null`,
    `set_at=null`, `plan_cap=1`, `plan_allowed=true`. Free /
    Hobby / Pro return `plan_allowed=false` so the CLI can
    render the upsell without a separate plan lookup.

    Mounted with the standard auth chain (no MFA, no admin
    scope — the customer owns the pin).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppStaticEgressIPResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
