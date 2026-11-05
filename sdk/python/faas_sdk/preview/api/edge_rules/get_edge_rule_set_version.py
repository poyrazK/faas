from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.edge_rule_set_version_response import EdgeRuleSetVersionResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    version: int,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/edge-rules/versions/{version}".format(
            slug=quote(str(slug), safe=""),
            version=quote(str(version), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EdgeRuleSetVersionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EdgeRuleSetVersionResponse.from_dict(response.json())

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
) -> Response[EdgeRuleSetVersionResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    version: int,
    *,
    client: AuthenticatedClient | Client,
) -> Response[EdgeRuleSetVersionResponse | Problem]:
    """Fetch one recorded edge-rule set version with its rules.

    Args:
        slug (str):
        version (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeRuleSetVersionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        version=version,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    version: int,
    *,
    client: AuthenticatedClient | Client,
) -> EdgeRuleSetVersionResponse | Problem | None:
    """Fetch one recorded edge-rule set version with its rules.

    Args:
        slug (str):
        version (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeRuleSetVersionResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        version=version,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    version: int,
    *,
    client: AuthenticatedClient | Client,
) -> Response[EdgeRuleSetVersionResponse | Problem]:
    """Fetch one recorded edge-rule set version with its rules.

    Args:
        slug (str):
        version (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EdgeRuleSetVersionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        version=version,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    version: int,
    *,
    client: AuthenticatedClient | Client,
) -> EdgeRuleSetVersionResponse | Problem | None:
    """Fetch one recorded edge-rule set version with its rules.

    Args:
        slug (str):
        version (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EdgeRuleSetVersionResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            version=version,
            client=client,
        )
    ).parsed
