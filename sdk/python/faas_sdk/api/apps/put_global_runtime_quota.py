from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.global_runtime_quota_response import GlobalRuntimeQuotaResponse
from ...models.problem import Problem
from ...models.put_global_runtime_quota_request import PutGlobalRuntimeQuotaRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    allocation_id: UUID,
    *,
    body: PutGlobalRuntimeQuotaRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/global-apps/{slug}/runtime-quotas/{allocation_id}".format(
            slug=quote(str(slug), safe=""),
            allocation_id=quote(str(allocation_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    _kwargs["follow_redirects"] = False
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GlobalRuntimeQuotaResponse | Problem | None:
    if response.status_code == 200:
        response_200 = GlobalRuntimeQuotaResponse.from_dict(response.json())

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
) -> Response[GlobalRuntimeQuotaResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    allocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutGlobalRuntimeQuotaRequest,
) -> Response[GlobalRuntimeQuotaResponse | Problem]:
    """Reserve global concurrency shares for every selected region.

     Requires verified directory tenant deploy-write access; organizations
    additionally require current owner/admin consent. Uses the current
    tenant plan, independent of the creator's personal plan. Positive shares
    must cover every selected verified region. The rollout grant is assigned
    to rollout_region, defaulting to the first region in sorted order.
    Steady and peak capacity include all unreleased allocations. Matching
    UUID/input retries retain the original generation, snapshot and tombstone.
    This endpoint reserves directory capacity; regional admission and runtime
    readiness are separate. No regional request or scheduler call is made.
    Bodies are bounded to 4096 bytes and 32 regional shares.

    Args:
        slug (str):
        allocation_id (UUID):
        body (PutGlobalRuntimeQuotaRequest): Immutable regional share allocation and optional
            placement of rollout headroom.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalRuntimeQuotaResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        allocation_id=allocation_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    allocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutGlobalRuntimeQuotaRequest,
) -> GlobalRuntimeQuotaResponse | Problem | None:
    """Reserve global concurrency shares for every selected region.

     Requires verified directory tenant deploy-write access; organizations
    additionally require current owner/admin consent. Uses the current
    tenant plan, independent of the creator's personal plan. Positive shares
    must cover every selected verified region. The rollout grant is assigned
    to rollout_region, defaulting to the first region in sorted order.
    Steady and peak capacity include all unreleased allocations. Matching
    UUID/input retries retain the original generation, snapshot and tombstone.
    This endpoint reserves directory capacity; regional admission and runtime
    readiness are separate. No regional request or scheduler call is made.
    Bodies are bounded to 4096 bytes and 32 regional shares.

    Args:
        slug (str):
        allocation_id (UUID):
        body (PutGlobalRuntimeQuotaRequest): Immutable regional share allocation and optional
            placement of rollout headroom.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalRuntimeQuotaResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        allocation_id=allocation_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    allocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutGlobalRuntimeQuotaRequest,
) -> Response[GlobalRuntimeQuotaResponse | Problem]:
    """Reserve global concurrency shares for every selected region.

     Requires verified directory tenant deploy-write access; organizations
    additionally require current owner/admin consent. Uses the current
    tenant plan, independent of the creator's personal plan. Positive shares
    must cover every selected verified region. The rollout grant is assigned
    to rollout_region, defaulting to the first region in sorted order.
    Steady and peak capacity include all unreleased allocations. Matching
    UUID/input retries retain the original generation, snapshot and tombstone.
    This endpoint reserves directory capacity; regional admission and runtime
    readiness are separate. No regional request or scheduler call is made.
    Bodies are bounded to 4096 bytes and 32 regional shares.

    Args:
        slug (str):
        allocation_id (UUID):
        body (PutGlobalRuntimeQuotaRequest): Immutable regional share allocation and optional
            placement of rollout headroom.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalRuntimeQuotaResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        allocation_id=allocation_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    allocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutGlobalRuntimeQuotaRequest,
) -> GlobalRuntimeQuotaResponse | Problem | None:
    """Reserve global concurrency shares for every selected region.

     Requires verified directory tenant deploy-write access; organizations
    additionally require current owner/admin consent. Uses the current
    tenant plan, independent of the creator's personal plan. Positive shares
    must cover every selected verified region. The rollout grant is assigned
    to rollout_region, defaulting to the first region in sorted order.
    Steady and peak capacity include all unreleased allocations. Matching
    UUID/input retries retain the original generation, snapshot and tombstone.
    This endpoint reserves directory capacity; regional admission and runtime
    readiness are separate. No regional request or scheduler call is made.
    Bodies are bounded to 4096 bytes and 32 regional shares.

    Args:
        slug (str):
        allocation_id (UUID):
        body (PutGlobalRuntimeQuotaRequest): Immutable regional share allocation and optional
            placement of rollout headroom.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalRuntimeQuotaResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            allocation_id=allocation_id,
            client=client,
            body=body,
        )
    ).parsed
