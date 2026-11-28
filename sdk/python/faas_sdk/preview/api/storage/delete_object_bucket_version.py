from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_version_delete_result import ObjectVersionDeleteResult
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    key: str,
    version_id: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["key"] = key

    params["version_id"] = version_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/buckets/{bucket}/objects/versions".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectVersionDeleteResult | Problem:
    if response.status_code == 200:
        response_200 = ObjectVersionDeleteResult.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectVersionDeleteResult | Problem]:
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
    key: str,
    version_id: str,
) -> Response[ObjectVersionDeleteResult | Problem]:
    """Permanently delete an immutable object version or delete marker

     Requires storage write scope and the bucket write grant. The public version ID must belong to this
    bucket and exact key. Retries address the same immutable version, including after restart or an
    uncertain provider acknowledgment. Deleting a marker can reveal older data. Mutable null deletion
    uses a durable single-attempt intent; retry with X-Gregale-Delete-Id or use the deletion receipt
    API. Quota is reclaimed only through verified capacity inventory.

    Args:
        slug (str):
        bucket (UUID):
        key (str):
        version_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectVersionDeleteResult | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
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
    key: str,
    version_id: str,
) -> ObjectVersionDeleteResult | Problem | None:
    """Permanently delete an immutable object version or delete marker

     Requires storage write scope and the bucket write grant. The public version ID must belong to this
    bucket and exact key. Retries address the same immutable version, including after restart or an
    uncertain provider acknowledgment. Deleting a marker can reveal older data. Mutable null deletion
    uses a durable single-attempt intent; retry with X-Gregale-Delete-Id or use the deletion receipt
    API. Quota is reclaimed only through verified capacity inventory.

    Args:
        slug (str):
        bucket (UUID):
        key (str):
        version_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectVersionDeleteResult | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        client=client,
        key=key,
        version_id=version_id,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    key: str,
    version_id: str,
) -> Response[ObjectVersionDeleteResult | Problem]:
    """Permanently delete an immutable object version or delete marker

     Requires storage write scope and the bucket write grant. The public version ID must belong to this
    bucket and exact key. Retries address the same immutable version, including after restart or an
    uncertain provider acknowledgment. Deleting a marker can reveal older data. Mutable null deletion
    uses a durable single-attempt intent; retry with X-Gregale-Delete-Id or use the deletion receipt
    API. Quota is reclaimed only through verified capacity inventory.

    Args:
        slug (str):
        bucket (UUID):
        key (str):
        version_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectVersionDeleteResult | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
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
    key: str,
    version_id: str,
) -> ObjectVersionDeleteResult | Problem | None:
    """Permanently delete an immutable object version or delete marker

     Requires storage write scope and the bucket write grant. The public version ID must belong to this
    bucket and exact key. Retries address the same immutable version, including after restart or an
    uncertain provider acknowledgment. Deleting a marker can reveal older data. Mutable null deletion
    uses a durable single-attempt intent; retry with X-Gregale-Delete-Id or use the deletion receipt
    API. Quota is reclaimed only through verified capacity inventory.

    Args:
        slug (str):
        bucket (UUID):
        key (str):
        version_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectVersionDeleteResult | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            key=key,
            version_id=version_id,
        )
    ).parsed
