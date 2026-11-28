from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_config_diff_response import ProjectEnvironmentConfigDiffResponse
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    from_: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["from"] = from_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/config/diff".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentConfigDiffResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentConfigDiffResponse.from_dict(response.json())

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
) -> Response[Problem | ProjectEnvironmentConfigDiffResponse]:
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
    from_: str,
) -> Response[Problem | ProjectEnvironmentConfigDiffResponse]:
    """Compare two environment configuration snapshots.

    Args:
        slug (str):
        environment (str):
        from_ (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentConfigDiffResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        from_=from_,
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
    from_: str,
) -> Problem | ProjectEnvironmentConfigDiffResponse | None:
    """Compare two environment configuration snapshots.

    Args:
        slug (str):
        environment (str):
        from_ (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentConfigDiffResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        from_=from_,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    from_: str,
) -> Response[Problem | ProjectEnvironmentConfigDiffResponse]:
    """Compare two environment configuration snapshots.

    Args:
        slug (str):
        environment (str):
        from_ (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentConfigDiffResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        from_=from_,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    from_: str,
) -> Problem | ProjectEnvironmentConfigDiffResponse | None:
    """Compare two environment configuration snapshots.

    Args:
        slug (str):
        environment (str):
        from_ (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentConfigDiffResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            from_=from_,
        )
    ).parsed
