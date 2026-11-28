from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.exclusive_trigger_binding_record import ExclusiveTriggerBindingRecord
from ...models.exclusive_trigger_binding_request import ExclusiveTriggerBindingRequest
from ...models.problem import Problem
from ...models.upsert_exclusive_operation_trigger_binding_source import (
    UpsertExclusiveOperationTriggerBindingSource,
)
from ...types import UNSET, Response, Unset


def _get_kwargs(
    source: UpsertExclusiveOperationTriggerBindingSource,
    id: UUID,
    *,
    body: ExclusiveTriggerBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/account/operation-trigger-bindings/{source}/{id}".format(
            source=quote(str(source), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

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
    source: UpsertExclusiveOperationTriggerBindingSource,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ExclusiveTriggerBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ExclusiveTriggerBindingRecord | Problem]:
    """Route an account-owned trigger through a managed operation policy.

     App triggers and recurring Job schedules are resolved from account-owned state. A tenant-scoped
    policy requires an active tenant-to-app link; tenant identity is not read from the business key. Job
    schedules require an account-scoped policy that explicitly includes the Job.

    Args:
        source (UpsertExclusiveOperationTriggerBindingSource):
        id (UUID):
        idempotency_key (str | Unset):
        body (ExclusiveTriggerBindingRequest): Configuration that routes one trusted trigger
            through an exclusive-operation policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExclusiveTriggerBindingRecord | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    source: UpsertExclusiveOperationTriggerBindingSource,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ExclusiveTriggerBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> ExclusiveTriggerBindingRecord | Problem | None:
    """Route an account-owned trigger through a managed operation policy.

     App triggers and recurring Job schedules are resolved from account-owned state. A tenant-scoped
    policy requires an active tenant-to-app link; tenant identity is not read from the business key. Job
    schedules require an account-scoped policy that explicitly includes the Job.

    Args:
        source (UpsertExclusiveOperationTriggerBindingSource):
        id (UUID):
        idempotency_key (str | Unset):
        body (ExclusiveTriggerBindingRequest): Configuration that routes one trusted trigger
            through an exclusive-operation policy.

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
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    source: UpsertExclusiveOperationTriggerBindingSource,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ExclusiveTriggerBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ExclusiveTriggerBindingRecord | Problem]:
    """Route an account-owned trigger through a managed operation policy.

     App triggers and recurring Job schedules are resolved from account-owned state. A tenant-scoped
    policy requires an active tenant-to-app link; tenant identity is not read from the business key. Job
    schedules require an account-scoped policy that explicitly includes the Job.

    Args:
        source (UpsertExclusiveOperationTriggerBindingSource):
        id (UUID):
        idempotency_key (str | Unset):
        body (ExclusiveTriggerBindingRequest): Configuration that routes one trusted trigger
            through an exclusive-operation policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExclusiveTriggerBindingRecord | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    source: UpsertExclusiveOperationTriggerBindingSource,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ExclusiveTriggerBindingRequest,
    idempotency_key: str | Unset = UNSET,
) -> ExclusiveTriggerBindingRecord | Problem | None:
    """Route an account-owned trigger through a managed operation policy.

     App triggers and recurring Job schedules are resolved from account-owned state. A tenant-scoped
    policy requires an active tenant-to-app link; tenant identity is not read from the business key. Job
    schedules require an account-scoped policy that explicitly includes the Job.

    Args:
        source (UpsertExclusiveOperationTriggerBindingSource):
        id (UUID):
        idempotency_key (str | Unset):
        body (ExclusiveTriggerBindingRequest): Configuration that routes one trusted trigger
            through an exclusive-operation policy.

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
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
