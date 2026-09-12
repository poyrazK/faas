from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.execution_list_response import ExecutionListResponse
from ...models.list_executions_status import ListExecutionsStatus
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
    status: ListExecutionsStatus | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    params["offset"] = offset

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/executions",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ExecutionListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ExecutionListResponse.from_dict(response.json())

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

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ExecutionListResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
    status: ListExecutionsStatus | Unset = UNSET,
) -> Response[ExecutionListResponse | Problem]:
    """List disposable executions.

     Returns the caller's newest disposable execution receipts. Results are
    account-scoped and ordered by creation time descending. Use `status`
    to narrow the page before applying offset pagination; source and input
    are never returned.

    Args:
        limit (int | Unset):  Default: 50.
        offset (int | Unset):  Default: 0.
        status (ListExecutionsStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExecutionListResponse | Problem]
    """

    kwargs = _get_kwargs(
        limit=limit,
        offset=offset,
        status=status,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
    status: ListExecutionsStatus | Unset = UNSET,
) -> ExecutionListResponse | Problem | None:
    """List disposable executions.

     Returns the caller's newest disposable execution receipts. Results are
    account-scoped and ordered by creation time descending. Use `status`
    to narrow the page before applying offset pagination; source and input
    are never returned.

    Args:
        limit (int | Unset):  Default: 50.
        offset (int | Unset):  Default: 0.
        status (ListExecutionsStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExecutionListResponse | Problem
    """

    return sync_detailed(
        client=client,
        limit=limit,
        offset=offset,
        status=status,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
    status: ListExecutionsStatus | Unset = UNSET,
) -> Response[ExecutionListResponse | Problem]:
    """List disposable executions.

     Returns the caller's newest disposable execution receipts. Results are
    account-scoped and ordered by creation time descending. Use `status`
    to narrow the page before applying offset pagination; source and input
    are never returned.

    Args:
        limit (int | Unset):  Default: 50.
        offset (int | Unset):  Default: 0.
        status (ListExecutionsStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExecutionListResponse | Problem]
    """

    kwargs = _get_kwargs(
        limit=limit,
        offset=offset,
        status=status,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
    status: ListExecutionsStatus | Unset = UNSET,
) -> ExecutionListResponse | Problem | None:
    """List disposable executions.

     Returns the caller's newest disposable execution receipts. Results are
    account-scoped and ordered by creation time descending. Use `status`
    to narrow the page before applying offset pagination; source and input
    are never returned.

    Args:
        limit (int | Unset):  Default: 50.
        offset (int | Unset):  Default: 0.
        status (ListExecutionsStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExecutionListResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            limit=limit,
            offset=offset,
            status=status,
        )
    ).parsed
