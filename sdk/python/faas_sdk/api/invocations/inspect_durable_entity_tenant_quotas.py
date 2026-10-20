from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.inspect_durable_entity_tenant_quotas_response_200 import InspectDurableEntityTenantQuotasResponse200
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
        "url": "/v1/apps/{slug}/entities/quotas".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> InspectDurableEntityTenantQuotasResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = InspectDurableEntityTenantQuotasResponse200.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[InspectDurableEntityTenantQuotasResponse200 | Problem]:
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
) -> Response[InspectDurableEntityTenantQuotasResponse200 | Problem]:
    """Inspect Durable Entity Tenant Quotas.

     Inspect durable entity tenant quotas: Opt-in durable-object preview. Requires the configured
    application allowlist and applicable feature gates; production enablement awaits native GCS/KVM
    acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy
    write access for mutation. Reader grace, claims and unknown acknowledgements must be respected.
    Responses are not cacheable. See the durable entity operator guides for the bounded recovery
    contract.

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
        Response[InspectDurableEntityTenantQuotasResponse200 | Problem]
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
) -> InspectDurableEntityTenantQuotasResponse200 | Problem | None:
    """Inspect Durable Entity Tenant Quotas.

     Inspect durable entity tenant quotas: Opt-in durable-object preview. Requires the configured
    application allowlist and applicable feature gates; production enablement awaits native GCS/KVM
    acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy
    write access for mutation. Reader grace, claims and unknown acknowledgements must be respected.
    Responses are not cacheable. See the durable entity operator guides for the bounded recovery
    contract.

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
        InspectDurableEntityTenantQuotasResponse200 | Problem
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
) -> Response[InspectDurableEntityTenantQuotasResponse200 | Problem]:
    """Inspect Durable Entity Tenant Quotas.

     Inspect durable entity tenant quotas: Opt-in durable-object preview. Requires the configured
    application allowlist and applicable feature gates; production enablement awaits native GCS/KVM
    acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy
    write access for mutation. Reader grace, claims and unknown acknowledgements must be respected.
    Responses are not cacheable. See the durable entity operator guides for the bounded recovery
    contract.

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
        Response[InspectDurableEntityTenantQuotasResponse200 | Problem]
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
) -> InspectDurableEntityTenantQuotasResponse200 | Problem | None:
    """Inspect Durable Entity Tenant Quotas.

     Inspect durable entity tenant quotas: Opt-in durable-object preview. Requires the configured
    application allowlist and applicable feature gates; production enablement awaits native GCS/KVM
    acceptance. Account-owner HTTP operations require MFA and app read access for inspection or deploy
    write access for mutation. Reader grace, claims and unknown acknowledgements must be respected.
    Responses are not cacheable. See the durable entity operator guides for the bounded recovery
    contract.

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
        InspectDurableEntityTenantQuotasResponse200 | Problem
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
