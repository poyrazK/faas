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
    version: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["version"] = version

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/flags".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FeatureFlagVersion | Problem | None:
    if response.status_code == 200:
        response_200 = FeatureFlagVersion.from_dict(response.json())

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
) -> Response[FeatureFlagVersion | Problem]:
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
    version: int | Unset = UNSET,
) -> Response[FeatureFlagVersion | Problem]:
    """Read current or historical feature flag configuration.

     Requires operator enablement via FAAS_FLAGS_ENABLED. Account-scoped read credentials only; app
    deploy tokens cannot read project-wide targeting lists.

    Args:
        slug (str):
        environment (str):
        version (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FeatureFlagVersion | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        version=version,
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
    version: int | Unset = UNSET,
) -> FeatureFlagVersion | Problem | None:
    """Read current or historical feature flag configuration.

     Requires operator enablement via FAAS_FLAGS_ENABLED. Account-scoped read credentials only; app
    deploy tokens cannot read project-wide targeting lists.

    Args:
        slug (str):
        environment (str):
        version (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FeatureFlagVersion | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        version=version,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    version: int | Unset = UNSET,
) -> Response[FeatureFlagVersion | Problem]:
    """Read current or historical feature flag configuration.

     Requires operator enablement via FAAS_FLAGS_ENABLED. Account-scoped read credentials only; app
    deploy tokens cannot read project-wide targeting lists.

    Args:
        slug (str):
        environment (str):
        version (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FeatureFlagVersion | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        version=version,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    version: int | Unset = UNSET,
) -> FeatureFlagVersion | Problem | None:
    """Read current or historical feature flag configuration.

     Requires operator enablement via FAAS_FLAGS_ENABLED. Account-scoped read credentials only; app
    deploy tokens cannot read project-wide targeting lists.

    Args:
        slug (str):
        environment (str):
        version (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FeatureFlagVersion | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            version=version,
        )
    ).parsed
