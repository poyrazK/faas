from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.instance_response import InstanceResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    history: bool | Unset = False,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["history"] = history

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/instances".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[InstanceResponse] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = InstanceResponse.from_dict(response_200_item_data)

            response_200.append(response_200_item)

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
) -> Response[Problem | list[InstanceResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    history: bool | Unset = False,
) -> Response[Problem | list[InstanceResponse]]:
    """Read-only instance list for an app.

     By default this returns only resident and in-flight instances, bounded
    by the app's effective concurrency limit. Set `history=true` to return
    the newest 100 retained lifecycle rows. The history view is not a
    complete audit log: PARKED wake rows expire after the configured
    instance-retention window (30 days by default), and only the newest
    100 retained rows are returned. Snapshots are durable deployment
    artifacts with their own lifecycle and are not removed with PARKED
    instance history.

    Args:
        slug (str):
        history (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[InstanceResponse]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        history=history,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    history: bool | Unset = False,
) -> Problem | list[InstanceResponse] | None:
    """Read-only instance list for an app.

     By default this returns only resident and in-flight instances, bounded
    by the app's effective concurrency limit. Set `history=true` to return
    the newest 100 retained lifecycle rows. The history view is not a
    complete audit log: PARKED wake rows expire after the configured
    instance-retention window (30 days by default), and only the newest
    100 retained rows are returned. Snapshots are durable deployment
    artifacts with their own lifecycle and are not removed with PARKED
    instance history.

    Args:
        slug (str):
        history (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[InstanceResponse]
    """

    return sync_detailed(
        slug=slug,
        client=client,
        history=history,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    history: bool | Unset = False,
) -> Response[Problem | list[InstanceResponse]]:
    """Read-only instance list for an app.

     By default this returns only resident and in-flight instances, bounded
    by the app's effective concurrency limit. Set `history=true` to return
    the newest 100 retained lifecycle rows. The history view is not a
    complete audit log: PARKED wake rows expire after the configured
    instance-retention window (30 days by default), and only the newest
    100 retained rows are returned. Snapshots are durable deployment
    artifacts with their own lifecycle and are not removed with PARKED
    instance history.

    Args:
        slug (str):
        history (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[InstanceResponse]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        history=history,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    history: bool | Unset = False,
) -> Problem | list[InstanceResponse] | None:
    """Read-only instance list for an app.

     By default this returns only resident and in-flight instances, bounded
    by the app's effective concurrency limit. Set `history=true` to return
    the newest 100 retained lifecycle rows. The history view is not a
    complete audit log: PARKED wake rows expire after the configured
    instance-retention window (30 days by default), and only the newest
    100 retained rows are returned. Snapshots are durable deployment
    artifacts with their own lifecycle and are not removed with PARKED
    instance history.

    Args:
        slug (str):
        history (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[InstanceResponse]
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            history=history,
        )
    ).parsed
