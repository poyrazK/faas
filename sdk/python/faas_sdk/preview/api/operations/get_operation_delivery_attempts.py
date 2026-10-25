from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_delivery_attempts_response import OperationDeliveryAttemptsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/operations/{id}/delivery-attempts".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | OperationDeliveryAttemptsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationDeliveryAttemptsResponse.from_dict(response.json())

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
) -> Response[Any | OperationDeliveryAttemptsResponse | Problem]:
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
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[Any | OperationDeliveryAttemptsResponse | Problem]:
    """Read retained notification attempts for one owned operation.

     Requires account read scope and MFA. Business work is never restarted. Attempt history stays
    readable while admission is closed. Expired operations return 410. This endpoint pages the existing
    notification ledger and excludes raw errors, destination URLs and payloads.

    Args:
        slug (str):
        id (UUID):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | OperationDeliveryAttemptsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        limit=limit,
        cursor=cursor,
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
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Any | OperationDeliveryAttemptsResponse | Problem | None:
    """Read retained notification attempts for one owned operation.

     Requires account read scope and MFA. Business work is never restarted. Attempt history stays
    readable while admission is closed. Expired operations return 410. This endpoint pages the existing
    notification ledger and excludes raw errors, destination URLs and payloads.

    Args:
        slug (str):
        id (UUID):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | OperationDeliveryAttemptsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Response[Any | OperationDeliveryAttemptsResponse | Problem]:
    """Read retained notification attempts for one owned operation.

     Requires account read scope and MFA. Business work is never restarted. Attempt history stays
    readable while admission is closed. Expired operations return 410. This endpoint pages the existing
    notification ledger and excludes raw errors, destination URLs and payloads.

    Args:
        slug (str):
        id (UUID):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | OperationDeliveryAttemptsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        limit=limit,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
) -> Any | OperationDeliveryAttemptsResponse | Problem | None:
    """Read retained notification attempts for one owned operation.

     Requires account read scope and MFA. Business work is never restarted. Attempt history stays
    readable while admission is closed. Expired operations return 410. This endpoint pages the existing
    notification ledger and excludes raw errors, destination URLs and payloads.

    Args:
        slug (str):
        id (UUID):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | OperationDeliveryAttemptsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
