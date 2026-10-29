from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deleted_app_list_response import DeletedAppListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    limit: int | Unset = 25,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deleted-apps",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeletedAppListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DeletedAppListResponse.from_dict(response.json())

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
) -> Response[DeletedAppListResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 25,
    cursor: str | Unset = UNSET,
) -> Response[DeletedAppListResponse | Problem]:
    """List account-owned retained deleted apps.

     Bounded lifecycle inventory; never includes another account's tombstones.
    Eligibility does not reserve quota or capacity. Use the returned deadline
    and evaluation time, not a deadline inferred from the browser clock.

    Args:
        limit (int | Unset):  Default: 25.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeletedAppListResponse | Problem]
    """

    kwargs = _get_kwargs(
        limit=limit,
        cursor=cursor,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 25,
    cursor: str | Unset = UNSET,
) -> DeletedAppListResponse | Problem | None:
    """List account-owned retained deleted apps.

     Bounded lifecycle inventory; never includes another account's tombstones.
    Eligibility does not reserve quota or capacity. Use the returned deadline
    and evaluation time, not a deadline inferred from the browser clock.

    Args:
        limit (int | Unset):  Default: 25.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeletedAppListResponse | Problem
    """

    return sync_detailed(
        client=client,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 25,
    cursor: str | Unset = UNSET,
) -> Response[DeletedAppListResponse | Problem]:
    """List account-owned retained deleted apps.

     Bounded lifecycle inventory; never includes another account's tombstones.
    Eligibility does not reserve quota or capacity. Use the returned deadline
    and evaluation time, not a deadline inferred from the browser clock.

    Args:
        limit (int | Unset):  Default: 25.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeletedAppListResponse | Problem]
    """

    kwargs = _get_kwargs(
        limit=limit,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 25,
    cursor: str | Unset = UNSET,
) -> DeletedAppListResponse | Problem | None:
    """List account-owned retained deleted apps.

     Bounded lifecycle inventory; never includes another account's tombstones.
    Eligibility does not reserve quota or capacity. Use the returned deadline
    and evaluation time, not a deadline inferred from the browser clock.

    Args:
        limit (int | Unset):  Default: 25.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeletedAppListResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
