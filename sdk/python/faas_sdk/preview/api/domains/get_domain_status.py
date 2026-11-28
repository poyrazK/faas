from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.resource_status import ResourceStatus
from ...types import Response


def _get_kwargs(
    domain: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/domains/{domain}/status".format(
            domain=quote(str(domain), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | ResourceStatus:
    if response.status_code == 200:
        response_200 = ResourceStatus.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ResourceStatus]:
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
) -> Response[Problem | ResourceStatus]:
    """Read a custom domain's conditions.

     Preview (resource-conditions capability). The domain's Ready,
    Verified and CertificateReady conditions, from the domain's recorded
    verification and certificate state and never stored. Unlike
    GET /v1/domains/{domain} it never connects to the domain, so it is
    safe to poll. None of its conditions is terminal: DNS can change
    outside Gregale. Cache-Control no-store.

    Args:
        domain (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ResourceStatus]
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
) -> Problem | ResourceStatus | None:
    """Read a custom domain's conditions.

     Preview (resource-conditions capability). The domain's Ready,
    Verified and CertificateReady conditions, from the domain's recorded
    verification and certificate state and never stored. Unlike
    GET /v1/domains/{domain} it never connects to the domain, so it is
    safe to poll. None of its conditions is terminal: DNS can change
    outside Gregale. Cache-Control no-store.

    Args:
        domain (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ResourceStatus
    """

    return sync_detailed(
        domain=domain,
        client=client,
    ).parsed


async def asyncio_detailed(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ResourceStatus]:
    """Read a custom domain's conditions.

     Preview (resource-conditions capability). The domain's Ready,
    Verified and CertificateReady conditions, from the domain's recorded
    verification and certificate state and never stored. Unlike
    GET /v1/domains/{domain} it never connects to the domain, so it is
    safe to poll. None of its conditions is terminal: DNS can change
    outside Gregale. Cache-Control no-store.

    Args:
        domain (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ResourceStatus]
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
) -> Problem | ResourceStatus | None:
    """Read a custom domain's conditions.

     Preview (resource-conditions capability). The domain's Ready,
    Verified and CertificateReady conditions, from the domain's recorded
    verification and certificate state and never stored. Unlike
    GET /v1/domains/{domain} it never connects to the domain, so it is
    safe to poll. None of its conditions is terminal: DNS can change
    outside Gregale. Cache-Control no-store.

    Args:
        domain (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ResourceStatus
    """

    return (
        await asyncio_detailed(
            domain=domain,
            client=client,
        )
    ).parsed
