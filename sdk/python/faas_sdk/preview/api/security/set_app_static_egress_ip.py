from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_static_egress_ip_response import AppStaticEgressIPResponse
from ...models.problem import Problem
from ...models.set_app_static_egress_ip_request import SetAppStaticEgressIPRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: SetAppStaticEgressIPRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/static-egress-ip".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppStaticEgressIPResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppStaticEgressIPResponse.from_dict(response.json())

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
    body: SetAppStaticEgressIPRequest,
) -> Response[AppStaticEgressIPResponse | Problem]:
    r"""Pin an IPv4 to the app's egress traffic (Scale-only).

     Customer-supplied IPv4 from their own range. The host
    bridge aliases the IP and a per-host postrouting
    MASQUERADE sibling rewrites matching tenant source
    traffic to the customer's IP. v1 limits:

    * Plan must be Scale (Plan.StaticEgressIPAllowed).
    * IPv4-only (IPv6 is rejected at the DB CHECK).
    * Not RFC1918, link-local, multicast, or /0.
    * Per-app quota of 1 (StaticEgressIPsPerApp) — two apps
      on the same account cannot pin the same IP.

    Sending `{\"ip\": \"203.0.113.42\", \"set\": true}` upserts
    the pin. Sending `{\"ip\": \"\", \"set\": false}` clears
    it. The DELETE verb below is a convenience wrapper
    for the clear path.

    Audit event: `app.static_egress_ip_set` carries the
    account/app/ip triple.

    Args:
        slug (str):
        body (SetAppStaticEgressIPRequest): PUT /v1/apps/{slug}/static-egress-ip body (ADR-119).
            IP is
            the canonical customer-supplied IPv4 (dotted-quad string).
            The handler validates the family=4 + non-RFC1918 +
            non-link-local + non-multicast shape before the column
            write. Set=false with empty IP means "clear" — the same
            wire body covers the DELETE /keep-IP promotion path
            without a third endpoint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppStaticEgressIPResponse | Problem]
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
    body: SetAppStaticEgressIPRequest,
) -> AppStaticEgressIPResponse | Problem | None:
    r"""Pin an IPv4 to the app's egress traffic (Scale-only).

     Customer-supplied IPv4 from their own range. The host
    bridge aliases the IP and a per-host postrouting
    MASQUERADE sibling rewrites matching tenant source
    traffic to the customer's IP. v1 limits:

    * Plan must be Scale (Plan.StaticEgressIPAllowed).
    * IPv4-only (IPv6 is rejected at the DB CHECK).
    * Not RFC1918, link-local, multicast, or /0.
    * Per-app quota of 1 (StaticEgressIPsPerApp) — two apps
      on the same account cannot pin the same IP.

    Sending `{\"ip\": \"203.0.113.42\", \"set\": true}` upserts
    the pin. Sending `{\"ip\": \"\", \"set\": false}` clears
    it. The DELETE verb below is a convenience wrapper
    for the clear path.

    Audit event: `app.static_egress_ip_set` carries the
    account/app/ip triple.

    Args:
        slug (str):
        body (SetAppStaticEgressIPRequest): PUT /v1/apps/{slug}/static-egress-ip body (ADR-119).
            IP is
            the canonical customer-supplied IPv4 (dotted-quad string).
            The handler validates the family=4 + non-RFC1918 +
            non-link-local + non-multicast shape before the column
            write. Set=false with empty IP means "clear" — the same
            wire body covers the DELETE /keep-IP promotion path
            without a third endpoint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppStaticEgressIPResponse | Problem
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
    body: SetAppStaticEgressIPRequest,
) -> Response[AppStaticEgressIPResponse | Problem]:
    r"""Pin an IPv4 to the app's egress traffic (Scale-only).

     Customer-supplied IPv4 from their own range. The host
    bridge aliases the IP and a per-host postrouting
    MASQUERADE sibling rewrites matching tenant source
    traffic to the customer's IP. v1 limits:

    * Plan must be Scale (Plan.StaticEgressIPAllowed).
    * IPv4-only (IPv6 is rejected at the DB CHECK).
    * Not RFC1918, link-local, multicast, or /0.
    * Per-app quota of 1 (StaticEgressIPsPerApp) — two apps
      on the same account cannot pin the same IP.

    Sending `{\"ip\": \"203.0.113.42\", \"set\": true}` upserts
    the pin. Sending `{\"ip\": \"\", \"set\": false}` clears
    it. The DELETE verb below is a convenience wrapper
    for the clear path.

    Audit event: `app.static_egress_ip_set` carries the
    account/app/ip triple.

    Args:
        slug (str):
        body (SetAppStaticEgressIPRequest): PUT /v1/apps/{slug}/static-egress-ip body (ADR-119).
            IP is
            the canonical customer-supplied IPv4 (dotted-quad string).
            The handler validates the family=4 + non-RFC1918 +
            non-link-local + non-multicast shape before the column
            write. Set=false with empty IP means "clear" — the same
            wire body covers the DELETE /keep-IP promotion path
            without a third endpoint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppStaticEgressIPResponse | Problem]
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
    body: SetAppStaticEgressIPRequest,
) -> AppStaticEgressIPResponse | Problem | None:
    r"""Pin an IPv4 to the app's egress traffic (Scale-only).

     Customer-supplied IPv4 from their own range. The host
    bridge aliases the IP and a per-host postrouting
    MASQUERADE sibling rewrites matching tenant source
    traffic to the customer's IP. v1 limits:

    * Plan must be Scale (Plan.StaticEgressIPAllowed).
    * IPv4-only (IPv6 is rejected at the DB CHECK).
    * Not RFC1918, link-local, multicast, or /0.
    * Per-app quota of 1 (StaticEgressIPsPerApp) — two apps
      on the same account cannot pin the same IP.

    Sending `{\"ip\": \"203.0.113.42\", \"set\": true}` upserts
    the pin. Sending `{\"ip\": \"\", \"set\": false}` clears
    it. The DELETE verb below is a convenience wrapper
    for the clear path.

    Audit event: `app.static_egress_ip_set` carries the
    account/app/ip triple.

    Args:
        slug (str):
        body (SetAppStaticEgressIPRequest): PUT /v1/apps/{slug}/static-egress-ip body (ADR-119).
            IP is
            the canonical customer-supplied IPv4 (dotted-quad string).
            The handler validates the family=4 + non-RFC1918 +
            non-link-local + non-multicast shape before the column
            write. Set=false with empty IP means "clear" — the same
            wire body covers the DELETE /keep-IP promotion path
            without a third endpoint.

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
            body=body,
        )
    ).parsed
