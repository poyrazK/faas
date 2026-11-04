from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_milestones_response import OperationMilestonesResponse
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
        "url": "/v1/apps/{slug}/operations/{id}/milestones".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationMilestonesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationMilestonesResponse.from_dict(response.json())

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
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperationMilestonesResponse | Problem]:
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
) -> Response[OperationMilestonesResponse | Problem]:
    """Read retained business milestones for an account-owned Operation.

     Requires account read scope and MFA. The app and Operation bind ownership; customer credentials
    cannot access this operator feed.

    Args:
        slug (str):
        id (UUID):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationMilestonesResponse | Problem]
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
) -> OperationMilestonesResponse | Problem | None:
    """Read retained business milestones for an account-owned Operation.

     Requires account read scope and MFA. The app and Operation bind ownership; customer credentials
    cannot access this operator feed.

    Args:
        slug (str):
        id (UUID):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationMilestonesResponse | Problem
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
) -> Response[OperationMilestonesResponse | Problem]:
    """Read retained business milestones for an account-owned Operation.

     Requires account read scope and MFA. The app and Operation bind ownership; customer credentials
    cannot access this operator feed.

    Args:
        slug (str):
        id (UUID):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationMilestonesResponse | Problem]
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
) -> OperationMilestonesResponse | Problem | None:
    """Read retained business milestones for an account-owned Operation.

     Requires account read scope and MFA. The app and Operation bind ownership; customer credentials
    cannot access this operator feed.

    Args:
        slug (str):
        id (UUID):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationMilestonesResponse | Problem
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
