from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.git_hub_installation_summary import GitHubInstallationSummary
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/github/installations".format(
            organization=quote(str(organization), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[GitHubInstallationSummary] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = GitHubInstallationSummary.from_dict(response_200_item_data)

            response_200.append(response_200_item)

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
) -> Response[Problem | list[GitHubInstallationSummary]]:
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
) -> Response[Problem | list[GitHubInstallationSummary]]:
    """List the GitHub installations a company organization owns.

     Company projects link repositories only through these installations. An owner or admin connects one
    from Connect GitHub with the organization selected. Members' personal installations are not listed;
    tokens are never returned.

    Args:
        organization (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[GitHubInstallationSummary]]
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
) -> Problem | list[GitHubInstallationSummary] | None:
    """List the GitHub installations a company organization owns.

     Company projects link repositories only through these installations. An owner or admin connects one
    from Connect GitHub with the organization selected. Members' personal installations are not listed;
    tokens are never returned.

    Args:
        organization (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[GitHubInstallationSummary]
    """

    return sync_detailed(
        organization=organization,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | list[GitHubInstallationSummary]]:
    """List the GitHub installations a company organization owns.

     Company projects link repositories only through these installations. An owner or admin connects one
    from Connect GitHub with the organization selected. Members' personal installations are not listed;
    tokens are never returned.

    Args:
        organization (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[GitHubInstallationSummary]]
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
) -> Problem | list[GitHubInstallationSummary] | None:
    """List the GitHub installations a company organization owns.

     Company projects link repositories only through these installations. An owner or admin connects one
    from Connect GitHub with the organization selected. Members' personal installations are not listed;
    tokens are never returned.

    Args:
        organization (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[GitHubInstallationSummary]
    """

    return (
        await asyncio_detailed(
            organization=organization,
            client=client,
        )
    ).parsed
