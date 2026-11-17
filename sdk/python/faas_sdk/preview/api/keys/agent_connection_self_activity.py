from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.agent_activity_response import AgentActivityResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    before: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["before"] = before

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/agent-connections/self/activity",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AgentActivityResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AgentActivityResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AgentActivityResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[AgentActivityResponse | Problem]:
    """Read the calling agent's own activity.

     The write requests (POST, PUT, PATCH, DELETE) the calling agent key
    made in the last 7 days, newest first. Only an agent key can call it;
    a person reads an agent's log by its connection id.

    Args:
        before (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentActivityResponse | Problem]
    """

    kwargs = _get_kwargs(
        before=before,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> AgentActivityResponse | Problem | None:
    """Read the calling agent's own activity.

     The write requests (POST, PUT, PATCH, DELETE) the calling agent key
    made in the last 7 days, newest first. Only an agent key can call it;
    a person reads an agent's log by its connection id.

    Args:
        before (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentActivityResponse | Problem
    """

    return sync_detailed(
        client=client,
        before=before,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[AgentActivityResponse | Problem]:
    """Read the calling agent's own activity.

     The write requests (POST, PUT, PATCH, DELETE) the calling agent key
    made in the last 7 days, newest first. Only an agent key can call it;
    a person reads an agent's log by its connection id.

    Args:
        before (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentActivityResponse | Problem]
    """

    kwargs = _get_kwargs(
        before=before,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> AgentActivityResponse | Problem | None:
    """Read the calling agent's own activity.

     The write requests (POST, PUT, PATCH, DELETE) the calling agent key
    made in the last 7 days, newest first. Only an agent key can call it;
    a person reads an agent's log by its connection id.

    Args:
        before (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentActivityResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            before=before,
            limit=limit,
        )
    ).parsed
