from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.configure_managed_realtime_push_provider_provider import (
    ConfigureManagedRealtimePushProviderProvider,
)
from ...models.configure_managed_realtime_push_provider_response_200 import (
    ConfigureManagedRealtimePushProviderResponse200,
)
from ...models.managed_realtime_push_provider_request import ManagedRealtimePushProviderRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    provider: ConfigureManagedRealtimePushProviderProvider,
    *,
    body: ManagedRealtimePushProviderRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/push/providers/{provider}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            provider=quote(str(provider), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ConfigureManagedRealtimePushProviderResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = ConfigureManagedRealtimePushProviderResponse200.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ConfigureManagedRealtimePushProviderResponse200 | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    provider: ConfigureManagedRealtimePushProviderProvider,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimePushProviderRequest,
) -> Response[ConfigureManagedRealtimePushProviderResponse200 | Problem]:
    """Configure or disable a push provider.

     Provider credentials are sealed at rest. Disabling cancels queued deliveries; re-enabling does not
    replay them. The retained-history preview gate is required.

    Args:
        slug (str):
        id (str):
        provider (ConfigureManagedRealtimePushProviderProvider):
        body (ManagedRealtimePushProviderRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConfigureManagedRealtimePushProviderResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        provider=provider,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    provider: ConfigureManagedRealtimePushProviderProvider,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimePushProviderRequest,
) -> ConfigureManagedRealtimePushProviderResponse200 | Problem | None:
    """Configure or disable a push provider.

     Provider credentials are sealed at rest. Disabling cancels queued deliveries; re-enabling does not
    replay them. The retained-history preview gate is required.

    Args:
        slug (str):
        id (str):
        provider (ConfigureManagedRealtimePushProviderProvider):
        body (ManagedRealtimePushProviderRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConfigureManagedRealtimePushProviderResponse200 | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        provider=provider,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    provider: ConfigureManagedRealtimePushProviderProvider,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimePushProviderRequest,
) -> Response[ConfigureManagedRealtimePushProviderResponse200 | Problem]:
    """Configure or disable a push provider.

     Provider credentials are sealed at rest. Disabling cancels queued deliveries; re-enabling does not
    replay them. The retained-history preview gate is required.

    Args:
        slug (str):
        id (str):
        provider (ConfigureManagedRealtimePushProviderProvider):
        body (ManagedRealtimePushProviderRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConfigureManagedRealtimePushProviderResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        provider=provider,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    provider: ConfigureManagedRealtimePushProviderProvider,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimePushProviderRequest,
) -> ConfigureManagedRealtimePushProviderResponse200 | Problem | None:
    """Configure or disable a push provider.

     Provider credentials are sealed at rest. Disabling cancels queued deliveries; re-enabling does not
    replay them. The retained-history preview gate is required.

    Args:
        slug (str):
        id (str):
        provider (ConfigureManagedRealtimePushProviderProvider):
        body (ManagedRealtimePushProviderRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConfigureManagedRealtimePushProviderResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            provider=provider,
            client=client,
            body=body,
        )
    ).parsed
