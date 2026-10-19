from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.org_queue_usage_response import OrgQueueUsageResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/queue-usage".format(
            organization=quote(str(organization), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OrgQueueUsageResponse | Problem:
    if response.status_code == 200:
        response_200 = OrgQueueUsageResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OrgQueueUsageResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OrgQueueUsageResponse | Problem]:
    """Read aggregate organization queue usage

     Read aggregate organization queue usage. Owners, admins, developers, viewers and billing seats can
    read counts with usage:read or admin. Requires billing adoption. Includes company queues across
    retained creator namespaces and excludes personal queues. Reserved async counts include all
    invocation sources sharing the organization cap. Pull leases use binding concurrency and do not
    reserve async slots.

    Args:
        organization (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgQueueUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
) -> OrgQueueUsageResponse | Problem | None:
    """Read aggregate organization queue usage

     Read aggregate organization queue usage. Owners, admins, developers, viewers and billing seats can
    read counts with usage:read or admin. Requires billing adoption. Includes company queues across
    retained creator namespaces and excludes personal queues. Reserved async counts include all
    invocation sources sharing the organization cap. Pull leases use binding concurrency and do not
    reserve async slots.

    Args:
        organization (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgQueueUsageResponse | Problem
    """

    return sync_detailed(
        organization=organization,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OrgQueueUsageResponse | Problem]:
    """Read aggregate organization queue usage

     Read aggregate organization queue usage. Owners, admins, developers, viewers and billing seats can
    read counts with usage:read or admin. Requires billing adoption. Includes company queues across
    retained creator namespaces and excludes personal queues. Reserved async counts include all
    invocation sources sharing the organization cap. Pull leases use binding concurrency and do not
    reserve async slots.

    Args:
        organization (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgQueueUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
) -> OrgQueueUsageResponse | Problem | None:
    """Read aggregate organization queue usage

     Read aggregate organization queue usage. Owners, admins, developers, viewers and billing seats can
    read counts with usage:read or admin. Requires billing adoption. Includes company queues across
    retained creator namespaces and excludes personal queues. Reserved async counts include all
    invocation sources sharing the organization cap. Pull leases use binding concurrency and do not
    reserve async slots.

    Args:
        organization (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgQueueUsageResponse | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            client=client,
        )
    ).parsed
