from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    name: str,
    id: UUID,
    idx: int,
    *,
    rows: int | Unset = UNSET,
    cols: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["rows"] = rows

    params["cols"] = cols

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/jobs/{name}/runs/{id}/tasks/{idx}/attach".format(
            name=quote(str(name), safe=""),
            id=quote(str(id), safe=""),
            idx=quote(str(idx), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 101:
        response_101 = cast(Any, None)
        return response_101

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    name: str,
    id: UUID,
    idx: int,
    *,
    client: AuthenticatedClient,
    rows: int | Unset = UNSET,
    cols: int | Unset = UNSET,
) -> Response[Any | Problem]:
    """Attach to an interactive image run's task (WebSocket).

     ADR-1330. Served by the compute gateway, not apid. The session uses
    the interactive app-task wire contract (ADR-1037): the account
    credential, deploy-write scope, the `X-Gregale-Attach-Token` from the
    image run's create response, and the `gregale-app-task-attach-v1`
    WebSocket subprotocol. Optional `rows`/`cols` set the initial terminal
    size. The run's VM holds the command until the first successful
    attach, which consumes the token.

    Args:
        name (str):
        id (UUID):
        idx (int):
        rows (int | Unset):
        cols (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
        idx=idx,
        rows=rows,
        cols=cols,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    name: str,
    id: UUID,
    idx: int,
    *,
    client: AuthenticatedClient,
    rows: int | Unset = UNSET,
    cols: int | Unset = UNSET,
) -> Any | Problem | None:
    """Attach to an interactive image run's task (WebSocket).

     ADR-1330. Served by the compute gateway, not apid. The session uses
    the interactive app-task wire contract (ADR-1037): the account
    credential, deploy-write scope, the `X-Gregale-Attach-Token` from the
    image run's create response, and the `gregale-app-task-attach-v1`
    WebSocket subprotocol. Optional `rows`/`cols` set the initial terminal
    size. The run's VM holds the command until the first successful
    attach, which consumes the token.

    Args:
        name (str):
        id (UUID):
        idx (int):
        rows (int | Unset):
        cols (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        name=name,
        id=id,
        idx=idx,
        client=client,
        rows=rows,
        cols=cols,
    ).parsed


async def asyncio_detailed(
    name: str,
    id: UUID,
    idx: int,
    *,
    client: AuthenticatedClient,
    rows: int | Unset = UNSET,
    cols: int | Unset = UNSET,
) -> Response[Any | Problem]:
    """Attach to an interactive image run's task (WebSocket).

     ADR-1330. Served by the compute gateway, not apid. The session uses
    the interactive app-task wire contract (ADR-1037): the account
    credential, deploy-write scope, the `X-Gregale-Attach-Token` from the
    image run's create response, and the `gregale-app-task-attach-v1`
    WebSocket subprotocol. Optional `rows`/`cols` set the initial terminal
    size. The run's VM holds the command until the first successful
    attach, which consumes the token.

    Args:
        name (str):
        id (UUID):
        idx (int):
        rows (int | Unset):
        cols (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
        idx=idx,
        rows=rows,
        cols=cols,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    id: UUID,
    idx: int,
    *,
    client: AuthenticatedClient,
    rows: int | Unset = UNSET,
    cols: int | Unset = UNSET,
) -> Any | Problem | None:
    """Attach to an interactive image run's task (WebSocket).

     ADR-1330. Served by the compute gateway, not apid. The session uses
    the interactive app-task wire contract (ADR-1037): the account
    credential, deploy-write scope, the `X-Gregale-Attach-Token` from the
    image run's create response, and the `gregale-app-task-attach-v1`
    WebSocket subprotocol. Optional `rows`/`cols` set the initial terminal
    size. The run's VM holds the command until the first successful
    attach, which consumes the token.

    Args:
        name (str):
        id (UUID):
        idx (int):
        rows (int | Unset):
        cols (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            name=name,
            id=id,
            idx=idx,
            client=client,
            rows=rows,
            cols=cols,
        )
    ).parsed
