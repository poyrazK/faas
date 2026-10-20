from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_diff_response import ProjectEnvironmentDiffResponse
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
        "url": "/v1/projects/{slug}/environments/{environment}/diff".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentDiffResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentDiffResponse.from_dict(response.json())

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
) -> Response[Problem | ProjectEnvironmentDiffResponse]:
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
) -> Response[Problem | ProjectEnvironmentDiffResponse]:
    """Compare two effective project environments.

     Compares configuration, releases, runtime variables, secret
    fingerprints and credential generations, and managed bindings. Secret
    values are never returned. A secret without a fingerprint is reported
    as unknown rather than incorrectly reported as equal.

    Args:
        slug (str):
        environment (str):
        from_ (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentDiffResponse]
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
) -> Problem | ProjectEnvironmentDiffResponse | None:
    """Compare two effective project environments.

     Compares configuration, releases, runtime variables, secret
    fingerprints and credential generations, and managed bindings. Secret
    values are never returned. A secret without a fingerprint is reported
    as unknown rather than incorrectly reported as equal.

    Args:
        slug (str):
        environment (str):
        from_ (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentDiffResponse
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
) -> Response[Problem | ProjectEnvironmentDiffResponse]:
    """Compare two effective project environments.

     Compares configuration, releases, runtime variables, secret
    fingerprints and credential generations, and managed bindings. Secret
    values are never returned. A secret without a fingerprint is reported
    as unknown rather than incorrectly reported as equal.

    Args:
        slug (str):
        environment (str):
        from_ (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentDiffResponse]
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
) -> Problem | ProjectEnvironmentDiffResponse | None:
    """Compare two effective project environments.

     Compares configuration, releases, runtime variables, secret
    fingerprints and credential generations, and managed bindings. Secret
    values are never returned. A secret without a fingerprint is reported
    as unknown rather than incorrectly reported as equal.

    Args:
        slug (str):
        environment (str):
        from_ (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentDiffResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            from_=from_,
        )
    ).parsed
