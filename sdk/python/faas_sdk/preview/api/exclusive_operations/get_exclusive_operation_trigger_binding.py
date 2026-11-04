from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.exclusive_trigger_binding_record import ExclusiveTriggerBindingRecord
from ...models.get_exclusive_operation_trigger_binding_source import (
    GetExclusiveOperationTriggerBindingSource,
)
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    source: GetExclusiveOperationTriggerBindingSource,
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/operation-trigger-bindings/{source}/{id}".format(
            source=quote(str(source), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ExclusiveTriggerBindingRecord | Problem | None:
    if response.status_code == 200:
        response_200 = ExclusiveTriggerBindingRecord.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ExclusiveTriggerBindingRecord | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    source: GetExclusiveOperationTriggerBindingSource,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ExclusiveTriggerBindingRecord | Problem]:
    """Inspect the operation policy attached to an account-owned trigger.

    Args:
        source (GetExclusiveOperationTriggerBindingSource):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExclusiveTriggerBindingRecord | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    source: GetExclusiveOperationTriggerBindingSource,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ExclusiveTriggerBindingRecord | Problem | None:
    """Inspect the operation policy attached to an account-owned trigger.

    Args:
        source (GetExclusiveOperationTriggerBindingSource):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExclusiveTriggerBindingRecord | Problem
    """

    return sync_detailed(
        source=source,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    source: GetExclusiveOperationTriggerBindingSource,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ExclusiveTriggerBindingRecord | Problem]:
    """Inspect the operation policy attached to an account-owned trigger.

    Args:
        source (GetExclusiveOperationTriggerBindingSource):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExclusiveTriggerBindingRecord | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    source: GetExclusiveOperationTriggerBindingSource,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ExclusiveTriggerBindingRecord | Problem | None:
    """Inspect the operation policy attached to an account-owned trigger.

    Args:
        source (GetExclusiveOperationTriggerBindingSource):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExclusiveTriggerBindingRecord | Problem
    """

    return (
        await asyncio_detailed(
            source=source,
            id=id,
            client=client,
        )
    ).parsed
