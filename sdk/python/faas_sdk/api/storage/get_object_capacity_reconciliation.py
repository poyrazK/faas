from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_capacity_reconciliation import ObjectCapacityReconciliation
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    reconciliation: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/buckets/{bucket}/capacity-reconciliations/{reconciliation}".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            reconciliation=quote(str(reconciliation), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectCapacityReconciliation | Problem:
    if response.status_code == 200:
        response_200 = ObjectCapacityReconciliation.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectCapacityReconciliation | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    bucket: UUID,
    reconciliation: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectCapacityReconciliation | Problem]:
    """Inspect capacity reconciliation

     Returns progress, pending-write count and reclaimed capacity under storage write scope and the
    bucket write grant. Reads remain available with storage disabled or spent budgets. Billing and
    monthly authorization counts are unchanged.

    Args:
        slug (str):
        bucket (UUID):
        reconciliation (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectCapacityReconciliation | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        reconciliation=reconciliation,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    reconciliation: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectCapacityReconciliation | Problem | None:
    """Inspect capacity reconciliation

     Returns progress, pending-write count and reclaimed capacity under storage write scope and the
    bucket write grant. Reads remain available with storage disabled or spent budgets. Billing and
    monthly authorization counts are unchanged.

    Args:
        slug (str):
        bucket (UUID):
        reconciliation (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectCapacityReconciliation | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        reconciliation=reconciliation,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    reconciliation: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectCapacityReconciliation | Problem]:
    """Inspect capacity reconciliation

     Returns progress, pending-write count and reclaimed capacity under storage write scope and the
    bucket write grant. Reads remain available with storage disabled or spent budgets. Billing and
    monthly authorization counts are unchanged.

    Args:
        slug (str):
        bucket (UUID):
        reconciliation (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectCapacityReconciliation | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        reconciliation=reconciliation,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    reconciliation: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectCapacityReconciliation | Problem | None:
    """Inspect capacity reconciliation

     Returns progress, pending-write count and reclaimed capacity under storage write scope and the
    bucket write grant. Reads remain available with storage disabled or spent budgets. Billing and
    monthly authorization counts are unchanged.

    Args:
        slug (str):
        bucket (UUID):
        reconciliation (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectCapacityReconciliation | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            reconciliation=reconciliation,
            client=client,
        )
    ).parsed
