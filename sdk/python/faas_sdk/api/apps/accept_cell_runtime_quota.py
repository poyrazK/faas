from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.regional_runtime_quota_delivery import RegionalRuntimeQuotaDelivery
from ...models.regional_runtime_quota_delivery_receipt import RegionalRuntimeQuotaDeliveryReceipt
from ...types import Response


def _get_kwargs(
    cell_id: UUID,
    app_id: UUID,
    allocation_id: UUID,
    *,
    body: RegionalRuntimeQuotaDelivery,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/cells/{cell_id}/apps/{app_id}/runtime-quotas/{allocation_id}".format(
            cell_id=quote(str(cell_id), safe=""),
            app_id=quote(str(app_id), safe=""),
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
) -> Problem | RegionalRuntimeQuotaDeliveryReceipt | None:
    if response.status_code == 200:
        response_200 = RegionalRuntimeQuotaDeliveryReceipt.from_dict(response.json())

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

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RegionalRuntimeQuotaDeliveryReceipt]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    cell_id: UUID,
    app_id: UUID,
    allocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RegionalRuntimeQuotaDelivery,
) -> Response[Problem | RegionalRuntimeQuotaDeliveryReceipt]:
    """Store an original signed regional quota grant.

     Requires a bound regional database, pinned directory trust, the exact configured management origin
    and an authenticated regional personal owner admin key. Canonical signed authority must match the
    original path and binding. Direct delivery rejects consent_id. Receipt storage leaves admission
    frozen and grants no native execution authority. Redirects must never be followed.

    Args:
        cell_id (UUID):
        app_id (UUID):
        allocation_id (UUID):
        body (RegionalRuntimeQuotaDelivery): Canonical delivery body bounded to 20480 bytes. The
            direct grant and retirement routes reject consent_id; consent-based delivery uses the
            delegated receiver.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RegionalRuntimeQuotaDeliveryReceipt]
    """

    kwargs = _get_kwargs(
        cell_id=cell_id,
        app_id=app_id,
        allocation_id=allocation_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    cell_id: UUID,
    app_id: UUID,
    allocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RegionalRuntimeQuotaDelivery,
) -> Problem | RegionalRuntimeQuotaDeliveryReceipt | None:
    """Store an original signed regional quota grant.

     Requires a bound regional database, pinned directory trust, the exact configured management origin
    and an authenticated regional personal owner admin key. Canonical signed authority must match the
    original path and binding. Direct delivery rejects consent_id. Receipt storage leaves admission
    frozen and grants no native execution authority. Redirects must never be followed.

    Args:
        cell_id (UUID):
        app_id (UUID):
        allocation_id (UUID):
        body (RegionalRuntimeQuotaDelivery): Canonical delivery body bounded to 20480 bytes. The
            direct grant and retirement routes reject consent_id; consent-based delivery uses the
            delegated receiver.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RegionalRuntimeQuotaDeliveryReceipt
    """

    return sync_detailed(
        cell_id=cell_id,
        app_id=app_id,
        allocation_id=allocation_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    cell_id: UUID,
    app_id: UUID,
    allocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RegionalRuntimeQuotaDelivery,
) -> Response[Problem | RegionalRuntimeQuotaDeliveryReceipt]:
    """Store an original signed regional quota grant.

     Requires a bound regional database, pinned directory trust, the exact configured management origin
    and an authenticated regional personal owner admin key. Canonical signed authority must match the
    original path and binding. Direct delivery rejects consent_id. Receipt storage leaves admission
    frozen and grants no native execution authority. Redirects must never be followed.

    Args:
        cell_id (UUID):
        app_id (UUID):
        allocation_id (UUID):
        body (RegionalRuntimeQuotaDelivery): Canonical delivery body bounded to 20480 bytes. The
            direct grant and retirement routes reject consent_id; consent-based delivery uses the
            delegated receiver.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RegionalRuntimeQuotaDeliveryReceipt]
    """

    kwargs = _get_kwargs(
        cell_id=cell_id,
        app_id=app_id,
        allocation_id=allocation_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    cell_id: UUID,
    app_id: UUID,
    allocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: RegionalRuntimeQuotaDelivery,
) -> Problem | RegionalRuntimeQuotaDeliveryReceipt | None:
    """Store an original signed regional quota grant.

     Requires a bound regional database, pinned directory trust, the exact configured management origin
    and an authenticated regional personal owner admin key. Canonical signed authority must match the
    original path and binding. Direct delivery rejects consent_id. Receipt storage leaves admission
    frozen and grants no native execution authority. Redirects must never be followed.

    Args:
        cell_id (UUID):
        app_id (UUID):
        allocation_id (UUID):
        body (RegionalRuntimeQuotaDelivery): Canonical delivery body bounded to 20480 bytes. The
            direct grant and retirement routes reject consent_id; consent-based delivery uses the
            delegated receiver.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RegionalRuntimeQuotaDeliveryReceipt
    """

    return (
        await asyncio_detailed(
            cell_id=cell_id,
            app_id=app_id,
            allocation_id=allocation_id,
            client=client,
            body=body,
        )
    ).parsed
