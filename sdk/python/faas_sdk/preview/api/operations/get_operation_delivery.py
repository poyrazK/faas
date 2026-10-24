from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_delivery_inspection import OperationDeliveryInspection
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/operations/{id}/delivery".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | OperationDeliveryInspection | Problem | None:
    if response.status_code == 200:
        response_200 = OperationDeliveryInspection.from_dict(response.json())

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

    if response.status_code == 410:
        response_410 = cast(Any, None)
        return response_410

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
) -> Response[Any | OperationDeliveryInspection | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | OperationDeliveryInspection | Problem]:
    """Inspect completion delivery independently of the business result.

     Requires account read scope and MFA. Business work is never restarted. Reads remain available with
    admission closed. Retention expiry returns 410. Receiver observations are local ledger snapshots,
    not connectivity probes. Raw receiver errors, target URLs and payloads are excluded.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | OperationDeliveryInspection | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | OperationDeliveryInspection | Problem | None:
    """Inspect completion delivery independently of the business result.

     Requires account read scope and MFA. Business work is never restarted. Reads remain available with
    admission closed. Retention expiry returns 410. Receiver observations are local ledger snapshots,
    not connectivity probes. Raw receiver errors, target URLs and payloads are excluded.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | OperationDeliveryInspection | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | OperationDeliveryInspection | Problem]:
    """Inspect completion delivery independently of the business result.

     Requires account read scope and MFA. Business work is never restarted. Reads remain available with
    admission closed. Retention expiry returns 410. Receiver observations are local ledger snapshots,
    not connectivity probes. Raw receiver errors, target URLs and payloads are excluded.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | OperationDeliveryInspection | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | OperationDeliveryInspection | Problem | None:
    """Inspect completion delivery independently of the business result.

     Requires account read scope and MFA. Business work is never restarted. Reads remain available with
    admission closed. Retention expiry returns 410. Receiver observations are local ledger snapshots,
    not connectivity probes. Raw receiver errors, target URLs and payloads are excluded.

    Args:
        slug (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | OperationDeliveryInspection | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
        )
    ).parsed
