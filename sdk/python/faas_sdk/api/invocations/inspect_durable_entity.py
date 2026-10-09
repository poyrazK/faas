from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.durable_entity_inspect_response import DurableEntityInspectResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: UUID | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["namespace"] = namespace

    params["key"] = key

    params["environment"] = environment

    json_platform_tenant_id: str | Unset = UNSET
    if not isinstance(platform_tenant_id, Unset):
        json_platform_tenant_id = str(platform_tenant_id)
    params["platform_tenant_id"] = json_platform_tenant_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/entities/inspect".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DurableEntityInspectResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DurableEntityInspectResponse.from_dict(response.json())

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
) -> Response[DurableEntityInspectResponse | Problem]:
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
    platform_tenant_id: UUID | Unset = UNSET,
) -> Response[DurableEntityInspectResponse | Problem]:
    """Inspect one durable entity's state and recovery metadata.

     Account-owner read preview, requiring apps:read or admin, MFA where
    applicable, and the explicit durable entity app allowlist. Resolves the
    current immutable environment and same-account optional customer. Owner
    inspection permits suspended customers and plan downgrades for diagnosis;
    it does not authorize execution. Customer self-service tokens are excluded.
    Returns state version, alarm and pending-outbox recovery metadata only.
    No business data, receipts, payloads, credentials, claim tokens or bucket
    paths are exposed. Inspection acquires no ownership, writes no objects,
    repairs no indexes and runs no guest code. A missing entity returns 404
    without creating it; corrupt committed state fails closed with 503.
    Head delivery status is a separate, later observation of retained transport
    history. Unknown includes absent/pruned history or read failures and never
    proves a message was not accepted. An empty pending queue does not prove
    receiver completion. These observations are not atomic across stores.
    Inspection does not retry work. Use its recovery_revision for the separate
    owner-only retry endpoint. Responses are not cacheable.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityInspectResponse | Problem]
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
    platform_tenant_id: UUID | Unset = UNSET,
) -> DurableEntityInspectResponse | Problem | None:
    """Inspect one durable entity's state and recovery metadata.

     Account-owner read preview, requiring apps:read or admin, MFA where
    applicable, and the explicit durable entity app allowlist. Resolves the
    current immutable environment and same-account optional customer. Owner
    inspection permits suspended customers and plan downgrades for diagnosis;
    it does not authorize execution. Customer self-service tokens are excluded.
    Returns state version, alarm and pending-outbox recovery metadata only.
    No business data, receipts, payloads, credentials, claim tokens or bucket
    paths are exposed. Inspection acquires no ownership, writes no objects,
    repairs no indexes and runs no guest code. A missing entity returns 404
    without creating it; corrupt committed state fails closed with 503.
    Head delivery status is a separate, later observation of retained transport
    history. Unknown includes absent/pruned history or read failures and never
    proves a message was not accepted. An empty pending queue does not prove
    receiver completion. These observations are not atomic across stores.
    Inspection does not retry work. Use its recovery_revision for the separate
    owner-only retry endpoint. Responses are not cacheable.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityInspectResponse | Problem
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
    platform_tenant_id: UUID | Unset = UNSET,
) -> Response[DurableEntityInspectResponse | Problem]:
    """Inspect one durable entity's state and recovery metadata.

     Account-owner read preview, requiring apps:read or admin, MFA where
    applicable, and the explicit durable entity app allowlist. Resolves the
    current immutable environment and same-account optional customer. Owner
    inspection permits suspended customers and plan downgrades for diagnosis;
    it does not authorize execution. Customer self-service tokens are excluded.
    Returns state version, alarm and pending-outbox recovery metadata only.
    No business data, receipts, payloads, credentials, claim tokens or bucket
    paths are exposed. Inspection acquires no ownership, writes no objects,
    repairs no indexes and runs no guest code. A missing entity returns 404
    without creating it; corrupt committed state fails closed with 503.
    Head delivery status is a separate, later observation of retained transport
    history. Unknown includes absent/pruned history or read failures and never
    proves a message was not accepted. An empty pending queue does not prove
    receiver completion. These observations are not atomic across stores.
    Inspection does not retry work. Use its recovery_revision for the separate
    owner-only retry endpoint. Responses are not cacheable.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityInspectResponse | Problem]
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
    platform_tenant_id: UUID | Unset = UNSET,
) -> DurableEntityInspectResponse | Problem | None:
    """Inspect one durable entity's state and recovery metadata.

     Account-owner read preview, requiring apps:read or admin, MFA where
    applicable, and the explicit durable entity app allowlist. Resolves the
    current immutable environment and same-account optional customer. Owner
    inspection permits suspended customers and plan downgrades for diagnosis;
    it does not authorize execution. Customer self-service tokens are excluded.
    Returns state version, alarm and pending-outbox recovery metadata only.
    No business data, receipts, payloads, credentials, claim tokens or bucket
    paths are exposed. Inspection acquires no ownership, writes no objects,
    repairs no indexes and runs no guest code. A missing entity returns 404
    without creating it; corrupt committed state fails closed with 503.
    Head delivery status is a separate, later observation of retained transport
    history. Unknown includes absent/pruned history or read failures and never
    proves a message was not accepted. An empty pending queue does not prove
    receiver completion. These observations are not atomic across stores.
    Inspection does not retry work. Use its recovery_revision for the separate
    owner-only retry endpoint. Responses are not cacheable.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityInspectResponse | Problem
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
