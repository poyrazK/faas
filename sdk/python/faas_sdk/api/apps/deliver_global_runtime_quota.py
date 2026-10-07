from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deliver_global_runtime_quota_request import DeliverGlobalRuntimeQuotaRequest
from ...models.problem import Problem
from ...models.regional_runtime_quota_delivery_receipt import RegionalRuntimeQuotaDeliveryReceipt
from ...types import Response


def _get_kwargs(
    slug: str,
    allocation_id: UUID,
    region: str,
    *,
    body: DeliverGlobalRuntimeQuotaRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/global-apps/{slug}/runtime-quotas/{allocation_id}/deliver/{region}".format(
            slug=quote(str(slug), safe=""),
            allocation_id=quote(str(allocation_id), safe=""),
            region=quote(str(region), safe=""),
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
    slug: str,
    allocation_id: UUID,
    region: str,
    *,
    client: AuthenticatedClient | Client,
    body: DeliverGlobalRuntimeQuotaRequest,
) -> Response[Problem | RegionalRuntimeQuotaDeliveryReceipt]:
    """Deliver original signed quota authority to one region.

     Requires verified directory personal owner write access. Uses either the request-only regional owner
    key or the original active runtime consent. The exact current selected binding, issuer, owner plan
    and original issued allocation are checked. Redirects must never be followed. A receipt records
    storage only; regional admission remains frozen and retirement still requires independent native
    acknowledgement.

    Args:
        slug (str):
        allocation_id (UUID):
        region (str):
        body (DeliverGlobalRuntimeQuotaRequest): Supply either a request-only regional account-
            admin key or the original active runtime consent UUID, never both.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RegionalRuntimeQuotaDeliveryReceipt]
    """

    kwargs = _get_kwargs(
        slug=slug,
        allocation_id=allocation_id,
        region=region,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    allocation_id: UUID,
    region: str,
    *,
    client: AuthenticatedClient | Client,
    body: DeliverGlobalRuntimeQuotaRequest,
) -> Problem | RegionalRuntimeQuotaDeliveryReceipt | None:
    """Deliver original signed quota authority to one region.

     Requires verified directory personal owner write access. Uses either the request-only regional owner
    key or the original active runtime consent. The exact current selected binding, issuer, owner plan
    and original issued allocation are checked. Redirects must never be followed. A receipt records
    storage only; regional admission remains frozen and retirement still requires independent native
    acknowledgement.

    Args:
        slug (str):
        allocation_id (UUID):
        region (str):
        body (DeliverGlobalRuntimeQuotaRequest): Supply either a request-only regional account-
            admin key or the original active runtime consent UUID, never both.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RegionalRuntimeQuotaDeliveryReceipt
    """

    return sync_detailed(
        slug=slug,
        allocation_id=allocation_id,
        region=region,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    allocation_id: UUID,
    region: str,
    *,
    client: AuthenticatedClient | Client,
    body: DeliverGlobalRuntimeQuotaRequest,
) -> Response[Problem | RegionalRuntimeQuotaDeliveryReceipt]:
    """Deliver original signed quota authority to one region.

     Requires verified directory personal owner write access. Uses either the request-only regional owner
    key or the original active runtime consent. The exact current selected binding, issuer, owner plan
    and original issued allocation are checked. Redirects must never be followed. A receipt records
    storage only; regional admission remains frozen and retirement still requires independent native
    acknowledgement.

    Args:
        slug (str):
        allocation_id (UUID):
        region (str):
        body (DeliverGlobalRuntimeQuotaRequest): Supply either a request-only regional account-
            admin key or the original active runtime consent UUID, never both.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RegionalRuntimeQuotaDeliveryReceipt]
    """

    kwargs = _get_kwargs(
        slug=slug,
        allocation_id=allocation_id,
        region=region,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    allocation_id: UUID,
    region: str,
    *,
    client: AuthenticatedClient | Client,
    body: DeliverGlobalRuntimeQuotaRequest,
) -> Problem | RegionalRuntimeQuotaDeliveryReceipt | None:
    """Deliver original signed quota authority to one region.

     Requires verified directory personal owner write access. Uses either the request-only regional owner
    key or the original active runtime consent. The exact current selected binding, issuer, owner plan
    and original issued allocation are checked. Redirects must never be followed. A receipt records
    storage only; regional admission remains frozen and retirement still requires independent native
    acknowledgement.

    Args:
        slug (str):
        allocation_id (UUID):
        region (str):
        body (DeliverGlobalRuntimeQuotaRequest): Supply either a request-only regional account-
            admin key or the original active runtime consent UUID, never both.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RegionalRuntimeQuotaDeliveryReceipt
    """

    return (
        await asyncio_detailed(
            slug=slug,
            allocation_id=allocation_id,
            region=region,
            client=client,
            body=body,
        )
    ).parsed
