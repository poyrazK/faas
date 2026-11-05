from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_cron_runs_response import ListCronRunsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    before: str | Unset = UNSET,
    limit: int | Unset = 10,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["before"] = before

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/crons/{id}/runs".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListCronRunsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListCronRunsResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ListCronRunsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 10,
) -> Response[ListCronRunsResponse | Problem]:
    """List recent runs of a cron.

     Execution history for one cron, newest first. Each row reports
    when the fire started, how long it took (`duration_ms`,
    computed server-side), and a normalized `outcome` — so a
    timeout is distinguishable from a generic failure without
    parsing `error`.

    Paginated by `?before=<id>` (the LAST id of the returned
    slice). Defaults to 10 per page; capped at 100. For a wider,
    cross-source view use `GET /v1/invocations`.

    Args:
        id (str):
        before (str | Unset):
        limit (int | Unset):  Default: 10.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListCronRunsResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        before=before,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 10,
) -> ListCronRunsResponse | Problem | None:
    """List recent runs of a cron.

     Execution history for one cron, newest first. Each row reports
    when the fire started, how long it took (`duration_ms`,
    computed server-side), and a normalized `outcome` — so a
    timeout is distinguishable from a generic failure without
    parsing `error`.

    Paginated by `?before=<id>` (the LAST id of the returned
    slice). Defaults to 10 per page; capped at 100. For a wider,
    cross-source view use `GET /v1/invocations`.

    Args:
        id (str):
        before (str | Unset):
        limit (int | Unset):  Default: 10.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListCronRunsResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        before=before,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 10,
) -> Response[ListCronRunsResponse | Problem]:
    """List recent runs of a cron.

     Execution history for one cron, newest first. Each row reports
    when the fire started, how long it took (`duration_ms`,
    computed server-side), and a normalized `outcome` — so a
    timeout is distinguishable from a generic failure without
    parsing `error`.

    Paginated by `?before=<id>` (the LAST id of the returned
    slice). Defaults to 10 per page; capped at 100. For a wider,
    cross-source view use `GET /v1/invocations`.

    Args:
        id (str):
        before (str | Unset):
        limit (int | Unset):  Default: 10.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListCronRunsResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        before=before,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 10,
) -> ListCronRunsResponse | Problem | None:
    """List recent runs of a cron.

     Execution history for one cron, newest first. Each row reports
    when the fire started, how long it took (`duration_ms`,
    computed server-side), and a normalized `outcome` — so a
    timeout is distinguishable from a generic failure without
    parsing `error`.

    Paginated by `?before=<id>` (the LAST id of the returned
    slice). Defaults to 10 per page; capped at 100. For a wider,
    cross-source view use `GET /v1/invocations`.

    Args:
        id (str):
        before (str | Unset):
        limit (int | Unset):  Default: 10.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListCronRunsResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            before=before,
            limit=limit,
        )
    ).parsed
