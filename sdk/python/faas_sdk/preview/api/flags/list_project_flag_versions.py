from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.feature_flag_version import FeatureFlagVersion
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    before_version: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["before_version"] = before_version

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/flags/versions".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[FeatureFlagVersion] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = FeatureFlagVersion.from_dict(response_200_item_data)

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | list[FeatureFlagVersion]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    before_version: int | Unset = UNSET,
) -> Response[Problem | list[FeatureFlagVersion]]:
    """List up to 100 immutable configuration versions.

     Returns publication audit metadata in descending order; use before_version to continue history.

    Args:
        slug (str):
        environment (str):
        before_version (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[FeatureFlagVersion]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        before_version=before_version,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    before_version: int | Unset = UNSET,
) -> Problem | list[FeatureFlagVersion] | None:
    """List up to 100 immutable configuration versions.

     Returns publication audit metadata in descending order; use before_version to continue history.

    Args:
        slug (str):
        environment (str):
        before_version (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[FeatureFlagVersion]
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        before_version=before_version,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    before_version: int | Unset = UNSET,
) -> Response[Problem | list[FeatureFlagVersion]]:
    """List up to 100 immutable configuration versions.

     Returns publication audit metadata in descending order; use before_version to continue history.

    Args:
        slug (str):
        environment (str):
        before_version (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[FeatureFlagVersion]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        before_version=before_version,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    before_version: int | Unset = UNSET,
) -> Problem | list[FeatureFlagVersion] | None:
    """List up to 100 immutable configuration versions.

     Returns publication audit metadata in descending order; use before_version to continue history.

    Args:
        slug (str):
        environment (str):
        before_version (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[FeatureFlagVersion]
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            before_version=before_version,
        )
    ).parsed
