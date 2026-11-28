from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.queue_binding_response import QueueBindingResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    include_retired: bool | Unset = False,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["include_retired"] = include_retired

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/queue-bindings".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[QueueBindingResponse] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = QueueBindingResponse.from_dict(response_200_item_data)

            response_200.append(response_200_item)

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
) -> Response[Problem | list[QueueBindingResponse]]:
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
    include_retired: bool | Unset = False,
) -> Response[Problem | list[QueueBindingResponse]]:
    """List first-class queue bindings for an app.

     Returns the durable mappings between logical queues and worker/job
    workloads. Bindings are the configuration source for push consumers
    and queue-depth autoscaling; queue messages remain under /queues/*.
    Active bindings are returned by default. Set include_retired=true to
    retrieve retained identities and retirement timestamps for reviewed recovery.

    Args:
        slug (str):
        include_retired (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[QueueBindingResponse]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        include_retired=include_retired,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    include_retired: bool | Unset = False,
) -> Problem | list[QueueBindingResponse] | None:
    """List first-class queue bindings for an app.

     Returns the durable mappings between logical queues and worker/job
    workloads. Bindings are the configuration source for push consumers
    and queue-depth autoscaling; queue messages remain under /queues/*.
    Active bindings are returned by default. Set include_retired=true to
    retrieve retained identities and retirement timestamps for reviewed recovery.

    Args:
        slug (str):
        include_retired (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[QueueBindingResponse]
    """

    return sync_detailed(
        slug=slug,
        client=client,
        include_retired=include_retired,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    include_retired: bool | Unset = False,
) -> Response[Problem | list[QueueBindingResponse]]:
    """List first-class queue bindings for an app.

     Returns the durable mappings between logical queues and worker/job
    workloads. Bindings are the configuration source for push consumers
    and queue-depth autoscaling; queue messages remain under /queues/*.
    Active bindings are returned by default. Set include_retired=true to
    retrieve retained identities and retirement timestamps for reviewed recovery.

    Args:
        slug (str):
        include_retired (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[QueueBindingResponse]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        include_retired=include_retired,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    include_retired: bool | Unset = False,
) -> Problem | list[QueueBindingResponse] | None:
    """List first-class queue bindings for an app.

     Returns the durable mappings between logical queues and worker/job
    workloads. Bindings are the configuration source for push consumers
    and queue-depth autoscaling; queue messages remain under /queues/*.
    Active bindings are returned by default. Set include_retired=true to
    retrieve retained identities and retirement timestamps for reviewed recovery.

    Args:
        slug (str):
        include_retired (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[QueueBindingResponse]
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            include_retired=include_retired,
        )
    ).parsed
