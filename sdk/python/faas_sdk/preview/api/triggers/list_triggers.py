from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.trigger import Trigger
from ...models.trigger_kind import TriggerKind
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    app_id: str | Unset = UNSET,
    kind: TriggerKind | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["app_id"] = app_id

    json_kind: str | Unset = UNSET
    if not isinstance(kind, Unset):
        json_kind = kind

    params["kind"] = json_kind

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/triggers",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[Trigger] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = Trigger.from_dict(response_200_item_data)

            response_200.append(response_200_item)

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | list[Trigger]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: str | Unset = UNSET,
    kind: TriggerKind | Unset = UNSET,
) -> Response[Problem | list[Trigger]]:
    """List triggers on the account.

     Returns every trigger owned by the calling account, optional
    ?app_id filter to scope to one app, ?kind to scope to one
    kind. Newest-first by created_at; result is unbounded but
    the typical account has well under 200.

    Args:
        app_id (str | Unset):
        kind (TriggerKind | Unset): Discriminator for the underlying event source.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[Trigger]]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        kind=kind,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    app_id: str | Unset = UNSET,
    kind: TriggerKind | Unset = UNSET,
) -> Problem | list[Trigger] | None:
    """List triggers on the account.

     Returns every trigger owned by the calling account, optional
    ?app_id filter to scope to one app, ?kind to scope to one
    kind. Newest-first by created_at; result is unbounded but
    the typical account has well under 200.

    Args:
        app_id (str | Unset):
        kind (TriggerKind | Unset): Discriminator for the underlying event source.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[Trigger]
    """

    return sync_detailed(
        client=client,
        app_id=app_id,
        kind=kind,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    app_id: str | Unset = UNSET,
    kind: TriggerKind | Unset = UNSET,
) -> Response[Problem | list[Trigger]]:
    """List triggers on the account.

     Returns every trigger owned by the calling account, optional
    ?app_id filter to scope to one app, ?kind to scope to one
    kind. Newest-first by created_at; result is unbounded but
    the typical account has well under 200.

    Args:
        app_id (str | Unset):
        kind (TriggerKind | Unset): Discriminator for the underlying event source.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[Trigger]]
    """

    kwargs = _get_kwargs(
        app_id=app_id,
        kind=kind,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    app_id: str | Unset = UNSET,
    kind: TriggerKind | Unset = UNSET,
) -> Problem | list[Trigger] | None:
    """List triggers on the account.

     Returns every trigger owned by the calling account, optional
    ?app_id filter to scope to one app, ?kind to scope to one
    kind. Newest-first by created_at; result is unbounded but
    the typical account has well under 200.

    Args:
        app_id (str | Unset):
        kind (TriggerKind | Unset): Discriminator for the underlying event source.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[Trigger]
    """

    return (
        await asyncio_detailed(
            client=client,
            app_id=app_id,
            kind=kind,
        )
    ).parsed
