import datetime
from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_run_history_status import ListRunHistoryStatus
from ...models.problem import Problem
from ...models.run_history_response import RunHistoryResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    task_name: str | Unset = UNSET,
    task_version: int | Unset = UNSET,
    customer_ref: str | Unset = UNSET,
    agent_ref: str | Unset = UNSET,
    status: ListRunHistoryStatus | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["task_name"] = task_name

    params["task_version"] = task_version

    params["customer_ref"] = customer_ref

    params["agent_ref"] = agent_ref

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/run-history",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RunHistoryResponse | None:
    if response.status_code == 200:
        response_200 = RunHistoryResponse.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[Problem | RunHistoryResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    task_name: str | Unset = UNSET,
    task_version: int | Unset = UNSET,
    customer_ref: str | Unset = UNSET,
    agent_ref: str | Unset = UNSET,
    status: ListRunHistoryStatus | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    cursor: str | Unset = UNSET,
) -> Response[Problem | RunHistoryResponse]:
    """Read metadata-only invocation history under existing account/key-family visibility.

    Args:
        task_name (str | Unset):
        task_version (int | Unset):
        customer_ref (str | Unset):
        agent_ref (str | Unset):
        status (ListRunHistoryStatus | Unset):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RunHistoryResponse]
    """

    kwargs = _get_kwargs(
        task_name=task_name,
        task_version=task_version,
        customer_ref=customer_ref,
        agent_ref=agent_ref,
        status=status,
        since=since,
        until=until,
        cursor=cursor,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    task_name: str | Unset = UNSET,
    task_version: int | Unset = UNSET,
    customer_ref: str | Unset = UNSET,
    agent_ref: str | Unset = UNSET,
    status: ListRunHistoryStatus | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    cursor: str | Unset = UNSET,
) -> Problem | RunHistoryResponse | None:
    """Read metadata-only invocation history under existing account/key-family visibility.

    Args:
        task_name (str | Unset):
        task_version (int | Unset):
        customer_ref (str | Unset):
        agent_ref (str | Unset):
        status (ListRunHistoryStatus | Unset):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RunHistoryResponse
    """

    return sync_detailed(
        client=client,
        task_name=task_name,
        task_version=task_version,
        customer_ref=customer_ref,
        agent_ref=agent_ref,
        status=status,
        since=since,
        until=until,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    task_name: str | Unset = UNSET,
    task_version: int | Unset = UNSET,
    customer_ref: str | Unset = UNSET,
    agent_ref: str | Unset = UNSET,
    status: ListRunHistoryStatus | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    cursor: str | Unset = UNSET,
) -> Response[Problem | RunHistoryResponse]:
    """Read metadata-only invocation history under existing account/key-family visibility.

    Args:
        task_name (str | Unset):
        task_version (int | Unset):
        customer_ref (str | Unset):
        agent_ref (str | Unset):
        status (ListRunHistoryStatus | Unset):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RunHistoryResponse]
    """

    kwargs = _get_kwargs(
        task_name=task_name,
        task_version=task_version,
        customer_ref=customer_ref,
        agent_ref=agent_ref,
        status=status,
        since=since,
        until=until,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    task_name: str | Unset = UNSET,
    task_version: int | Unset = UNSET,
    customer_ref: str | Unset = UNSET,
    agent_ref: str | Unset = UNSET,
    status: ListRunHistoryStatus | Unset = UNSET,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
    cursor: str | Unset = UNSET,
) -> Problem | RunHistoryResponse | None:
    """Read metadata-only invocation history under existing account/key-family visibility.

    Args:
        task_name (str | Unset):
        task_version (int | Unset):
        customer_ref (str | Unset):
        agent_ref (str | Unset):
        status (ListRunHistoryStatus | Unset):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RunHistoryResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            task_name=task_name,
            task_version=task_version,
            customer_ref=customer_ref,
            agent_ref=agent_ref,
            status=status,
            since=since,
            until=until,
            cursor=cursor,
        )
    ).parsed
