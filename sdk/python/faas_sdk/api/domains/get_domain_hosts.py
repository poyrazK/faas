from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.custom_domain_hosts_response import CustomDomainHostsResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    domain: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/domains/{domain}/hosts".format(
            domain=quote(str(domain), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | CustomDomainHostsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CustomDomainHostsResponse.from_dict(response.json())

        return response_200

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
) -> Response[Any | CustomDomainHostsResponse | Problem]:
    """Read a wildcard domain's host allowlist.

     Returns the names below the wildcard that may hold an on-demand
    certificate. `restricted: false` means no
    allowlist is set and every name below the wildcard may.

    Args:
        domain (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | CustomDomainHostsResponse | Problem]
    """

    kwargs = _get_kwargs(
        domain=domain,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | CustomDomainHostsResponse | Problem | None:
    """Read a wildcard domain's host allowlist.

     Returns the names below the wildcard that may hold an on-demand
    certificate. `restricted: false` means no
    allowlist is set and every name below the wildcard may.

    Args:
        domain (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | CustomDomainHostsResponse | Problem
    """

    return sync_detailed(
        domain=domain,
        client=client,
    ).parsed


async def asyncio_detailed(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | CustomDomainHostsResponse | Problem]:
    """Read a wildcard domain's host allowlist.

     Returns the names below the wildcard that may hold an on-demand
    certificate. `restricted: false` means no
    allowlist is set and every name below the wildcard may.

    Args:
        domain (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | CustomDomainHostsResponse | Problem]
    """

    kwargs = _get_kwargs(
        domain=domain,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | CustomDomainHostsResponse | Problem | None:
    """Read a wildcard domain's host allowlist.

     Returns the names below the wildcard that may hold an on-demand
    certificate. `restricted: false` means no
    allowlist is set and every name below the wildcard may.

    Args:
        domain (str):

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
        )
    ).parsed
