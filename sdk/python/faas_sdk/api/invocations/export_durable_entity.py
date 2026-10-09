from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.durable_entity_state_export import DurableEntityStateExport
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["namespace"] = namespace

    params["key"] = key

    params["environment"] = environment

    params["platform_tenant_id"] = platform_tenant_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/entities/export".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DurableEntityStateExport | Problem | None:
    if response.status_code == 200:
        response_200 = DurableEntityStateExport.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DurableEntityStateExport | Problem]:
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
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
) -> Response[DurableEntityStateExport | Problem]:
    """Export committed application state from one durable entity.

     Owner-only preview requiring apps:read or admin and MFA where applicable.
    Resolves immutable account, app, environment and tenant identity. Requires
    app enablement. Diagnostic reads permit held/suspended scopes and plan
    downgrades. Performs no writes or guest invocation. Contains sensitive
    application JSON; responses are private, no-store. Excludes alarms,
    receipts, outbox and ownership. Checksum detects corruption, not authority.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityStateExport | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        namespace=namespace,
        key=key,
        environment=environment,
        platform_tenant_id=platform_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
) -> DurableEntityStateExport | Problem | None:
    """Export committed application state from one durable entity.

     Owner-only preview requiring apps:read or admin and MFA where applicable.
    Resolves immutable account, app, environment and tenant identity. Requires
    app enablement. Diagnostic reads permit held/suspended scopes and plan
    downgrades. Performs no writes or guest invocation. Contains sensitive
    application JSON; responses are private, no-store. Excludes alarms,
    receipts, outbox and ownership. Checksum detects corruption, not authority.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityStateExport | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        namespace=namespace,
        key=key,
        environment=environment,
        platform_tenant_id=platform_tenant_id,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
) -> Response[DurableEntityStateExport | Problem]:
    """Export committed application state from one durable entity.

     Owner-only preview requiring apps:read or admin and MFA where applicable.
    Resolves immutable account, app, environment and tenant identity. Requires
    app enablement. Diagnostic reads permit held/suspended scopes and plan
    downgrades. Performs no writes or guest invocation. Contains sensitive
    application JSON; responses are private, no-store. Excludes alarms,
    receipts, outbox and ownership. Checksum detects corruption, not authority.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityStateExport | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        namespace=namespace,
        key=key,
        environment=environment,
        platform_tenant_id=platform_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
) -> DurableEntityStateExport | Problem | None:
    """Export committed application state from one durable entity.

     Owner-only preview requiring apps:read or admin and MFA where applicable.
    Resolves immutable account, app, environment and tenant identity. Requires
    app enablement. Diagnostic reads permit held/suspended scopes and plan
    downgrades. Performs no writes or guest invocation. Contains sensitive
    application JSON; responses are private, no-store. Excludes alarms,
    receipts, outbox and ownership. Checksum detects corruption, not authority.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityStateExport | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            namespace=namespace,
            key=key,
            environment=environment,
            platform_tenant_id=platform_tenant_id,
        )
    ).parsed
