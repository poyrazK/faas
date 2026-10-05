from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_version_protection import ObjectVersionProtection
from ...models.object_version_retention_request import ObjectVersionRetentionRequest
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    body: ObjectVersionRetentionRequest,
    key: str,
    version_id: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["key"] = key

    params["version_id"] = version_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/buckets/{bucket}/objects/protection/retention".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectVersionProtection | Problem:
    if response.status_code == 200:
        response_200 = ObjectVersionProtection.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = ObjectVersionProtection.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectVersionProtection | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectVersionRetentionRequest,
    key: str,
    version_id: str,
) -> Response[ObjectVersionProtection | Problem]:
    """Request a durable exact version retention change

     Accept durable retention intent with storage manage scope, a bucket write grant and backend
    enrollment. Recovery verifies readback without repeating a dispatched PUT. Fixed dates round upward
    to milliseconds. Event hold changes and governance bypass are unsupported.

    Args:
        slug (str):
        bucket (UUID):
        key (str):
        version_id (str):
        body (ObjectVersionRetentionRequest): Stable identity and fixed retention intent for an
            owned version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectVersionProtection | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        body=body,
        key=key,
        version_id=version_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectVersionRetentionRequest,
    key: str,
    version_id: str,
) -> ObjectVersionProtection | Problem | None:
    """Request a durable exact version retention change

     Accept durable retention intent with storage manage scope, a bucket write grant and backend
    enrollment. Recovery verifies readback without repeating a dispatched PUT. Fixed dates round upward
    to milliseconds. Event hold changes and governance bypass are unsupported.

    Args:
        slug (str):
        bucket (UUID):
        key (str):
        version_id (str):
        body (ObjectVersionRetentionRequest): Stable identity and fixed retention intent for an
            owned version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectVersionProtection | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        client=client,
        body=body,
        key=key,
        version_id=version_id,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectVersionRetentionRequest,
    key: str,
    version_id: str,
) -> Response[ObjectVersionProtection | Problem]:
    """Request a durable exact version retention change

     Accept durable retention intent with storage manage scope, a bucket write grant and backend
    enrollment. Recovery verifies readback without repeating a dispatched PUT. Fixed dates round upward
    to milliseconds. Event hold changes and governance bypass are unsupported.

    Args:
        slug (str):
        bucket (UUID):
        key (str):
        version_id (str):
        body (ObjectVersionRetentionRequest): Stable identity and fixed retention intent for an
            owned version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectVersionProtection | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        body=body,
        key=key,
        version_id=version_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectVersionRetentionRequest,
    key: str,
    version_id: str,
) -> ObjectVersionProtection | Problem | None:
    """Request a durable exact version retention change

     Accept durable retention intent with storage manage scope, a bucket write grant and backend
    enrollment. Recovery verifies readback without repeating a dispatched PUT. Fixed dates round upward
    to milliseconds. Event hold changes and governance bypass are unsupported.

    Args:
        slug (str):
        bucket (UUID):
        key (str):
        version_id (str):
        body (ObjectVersionRetentionRequest): Stable identity and fixed retention intent for an
            owned version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectVersionProtection | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            body=body,
            key=key,
            version_id=version_id,
        )
    ).parsed
