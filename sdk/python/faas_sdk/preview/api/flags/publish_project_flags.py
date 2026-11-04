from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.feature_flag_version import FeatureFlagVersion
from ...models.problem import Problem
from ...models.update_feature_flags_request import UpdateFeatureFlagsRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: UpdateFeatureFlagsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/projects/{slug}/environments/{environment}/flags".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
    body: UpdateFeatureFlagsRequest,
) -> Response[FeatureFlagVersion | Problem]:
    """Publish an atomic feature flag configuration.

     Requires deploy-write scope and MFA when configured. expected_version zero creates the first
    version; conflicting writes return 409. Customers must belong to the account. Allocation seeds are
    immutable.

    Args:
        slug (str):
        environment (str):
        body (UpdateFeatureFlagsRequest): Optimistic atomic configuration publication.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FeatureFlagVersion | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
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
    body: UpdateFeatureFlagsRequest,
) -> FeatureFlagVersion | Problem | None:
    """Publish an atomic feature flag configuration.

     Requires deploy-write scope and MFA when configured. expected_version zero creates the first
    version; conflicting writes return 409. Customers must belong to the account. Allocation seeds are
    immutable.

    Args:
        slug (str):
        environment (str):
        body (UpdateFeatureFlagsRequest): Optimistic atomic configuration publication.

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
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateFeatureFlagsRequest,
) -> Response[FeatureFlagVersion | Problem]:
    """Publish an atomic feature flag configuration.

     Requires deploy-write scope and MFA when configured. expected_version zero creates the first
    version; conflicting writes return 409. Customers must belong to the account. Allocation seeds are
    immutable.

    Args:
        slug (str):
        environment (str):
        body (UpdateFeatureFlagsRequest): Optimistic atomic configuration publication.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FeatureFlagVersion | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateFeatureFlagsRequest,
) -> FeatureFlagVersion | Problem | None:
    """Publish an atomic feature flag configuration.

     Requires deploy-write scope and MFA when configured. expected_version zero creates the first
    version; conflicting writes return 409. Customers must belong to the account. Allocation seeds are
    immutable.

    Args:
        slug (str):
        environment (str):
        body (UpdateFeatureFlagsRequest): Optimistic atomic configuration publication.

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
            body=body,
        )
    ).parsed
