from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.consumer_upgrade_response import ConsumerUpgradeResponse
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    *,
    token: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["token"] = token

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/consumer-signup/{slug}/upgrade/plans".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ConsumerUpgradeResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ConsumerUpgradeResponse.from_dict(response.json())

        return response_200

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ConsumerUpgradeResponse | Problem]:
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
    token: str,
) -> Response[ConsumerUpgradeResponse | Problem]:
    """Read the plans an upgrade link's customer can move to.

     Needs no credential: the upgrade link's token
    names the customer. A plan-limit `429` carries the link in
    `upgrade_url`.

    Args:
        slug (str):
        token (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConsumerUpgradeResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        token=token,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    token: str,
) -> ConsumerUpgradeResponse | Problem | None:
    """Read the plans an upgrade link's customer can move to.

     Needs no credential: the upgrade link's token
    names the customer. A plan-limit `429` carries the link in
    `upgrade_url`.

    Args:
        slug (str):
        token (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConsumerUpgradeResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        token=token,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    token: str,
) -> Response[ConsumerUpgradeResponse | Problem]:
    """Read the plans an upgrade link's customer can move to.

     Needs no credential: the upgrade link's token
    names the customer. A plan-limit `429` carries the link in
    `upgrade_url`.

    Args:
        slug (str):
        token (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConsumerUpgradeResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        token=token,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    token: str,
) -> ConsumerUpgradeResponse | Problem | None:
    """Read the plans an upgrade link's customer can move to.

     Needs no credential: the upgrade link's token
    names the customer. A plan-limit `429` carries the link in
    `upgrade_url`.

    Args:
        slug (str):
        token (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConsumerUpgradeResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            token=token,
        )
    ).parsed
