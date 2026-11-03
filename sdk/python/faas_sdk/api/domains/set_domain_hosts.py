from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.custom_domain_hosts_response import CustomDomainHostsResponse
from ...models.problem import Problem
from ...models.set_custom_domain_hosts_request import SetCustomDomainHostsRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    domain: str,
    *,
    body: SetCustomDomainHostsRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/domains/{domain}/hosts".format(
            domain=quote(str(domain), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | CustomDomainHostsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CustomDomainHostsResponse.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = cast(Any, None)
        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | CustomDomainHostsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetCustomDomainHostsRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Any | CustomDomainHostsResponse | Problem]:
    """Replace a wildcard domain's host allowlist.

     Only the listed names below the wildcard get certificates; the edge
    refuses every other server name, so random names cannot spend the
    wildcard's new-host budget. Names are the part of the host before
    the wildcard's suffix (`eu.shop` for `eu.shop.example.com`), or the
    full host. Routing is unchanged, and certificates the edge already
    holds for a removed name keep serving until their next reload or
    renewal. The domain doctor's probe host is always allowed.

    Args:
        domain (str):
        idempotency_key (str | Unset):
        body (SetCustomDomainHostsRequest): Replaces a wildcard custom domain's host allowlist;
            DELETE the allowlist to allow every name again.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | CustomDomainHostsResponse | Problem]
    """

    kwargs = _get_kwargs(
        domain=domain,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetCustomDomainHostsRequest,
    idempotency_key: str | Unset = UNSET,
) -> Any | CustomDomainHostsResponse | Problem | None:
    """Replace a wildcard domain's host allowlist.

     Only the listed names below the wildcard get certificates; the edge
    refuses every other server name, so random names cannot spend the
    wildcard's new-host budget. Names are the part of the host before
    the wildcard's suffix (`eu.shop` for `eu.shop.example.com`), or the
    full host. Routing is unchanged, and certificates the edge already
    holds for a removed name keep serving until their next reload or
    renewal. The domain doctor's probe host is always allowed.

    Args:
        domain (str):
        idempotency_key (str | Unset):
        body (SetCustomDomainHostsRequest): Replaces a wildcard custom domain's host allowlist;
            DELETE the allowlist to allow every name again.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | CustomDomainHostsResponse | Problem
    """

    return sync_detailed(
        domain=domain,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetCustomDomainHostsRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Any | CustomDomainHostsResponse | Problem]:
    """Replace a wildcard domain's host allowlist.

     Only the listed names below the wildcard get certificates; the edge
    refuses every other server name, so random names cannot spend the
    wildcard's new-host budget. Names are the part of the host before
    the wildcard's suffix (`eu.shop` for `eu.shop.example.com`), or the
    full host. Routing is unchanged, and certificates the edge already
    holds for a removed name keep serving until their next reload or
    renewal. The domain doctor's probe host is always allowed.

    Args:
        domain (str):
        idempotency_key (str | Unset):
        body (SetCustomDomainHostsRequest): Replaces a wildcard custom domain's host allowlist;
            DELETE the allowlist to allow every name again.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | CustomDomainHostsResponse | Problem]
    """

    kwargs = _get_kwargs(
        domain=domain,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetCustomDomainHostsRequest,
    idempotency_key: str | Unset = UNSET,
) -> Any | CustomDomainHostsResponse | Problem | None:
    """Replace a wildcard domain's host allowlist.

     Only the listed names below the wildcard get certificates; the edge
    refuses every other server name, so random names cannot spend the
    wildcard's new-host budget. Names are the part of the host before
    the wildcard's suffix (`eu.shop` for `eu.shop.example.com`), or the
    full host. Routing is unchanged, and certificates the edge already
    holds for a removed name keep serving until their next reload or
    renewal. The domain doctor's probe host is always allowed.

    Args:
        domain (str):
        idempotency_key (str | Unset):
        body (SetCustomDomainHostsRequest): Replaces a wildcard custom domain's host allowlist;
            DELETE the allowlist to allow every name again.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | CustomDomainHostsResponse | Problem
    """

    return (
        await asyncio_detailed(
            domain=domain,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
