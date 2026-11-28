from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_readiness_inbox_response import OperationWorkflowReadinessInboxResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    scope: str,
    app_id: UUID,
    after_sequence: int | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["after_sequence"] = after_sequence

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/platform-tenant-self/workflow-readiness/subscriptions/{id}/notifications".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowReadinessInboxResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowReadinessInboxResponse.from_dict(response.json())

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
) -> Response[OperationWorkflowReadinessInboxResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    scope: str,
    app_id: UUID,
    after_sequence: int | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[OperationWorkflowReadinessInboxResponse | Problem]:
    """Customer get WorkflowReadinessInbox

     Customer durable readiness subscription get operation. Observations are bounded and advisory;
    execution remains application-owned. Requires platform-tenant Operations read scope.

    Args:
        id (UUID):
        scope (str):
        app_id (UUID):
        after_sequence (int | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessInboxResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        scope=scope,
        app_id=app_id,
        after_sequence=after_sequence,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,
    scope: str,
    app_id: UUID,
    after_sequence: int | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> OperationWorkflowReadinessInboxResponse | Problem | None:
    """Customer get WorkflowReadinessInbox

     Customer durable readiness subscription get operation. Observations are bounded and advisory;
    execution remains application-owned. Requires platform-tenant Operations read scope.

    Args:
        id (UUID):
        scope (str):
        app_id (UUID):
        after_sequence (int | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessInboxResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        scope=scope,
        app_id=app_id,
        after_sequence=after_sequence,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    scope: str,
    app_id: UUID,
    after_sequence: int | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[OperationWorkflowReadinessInboxResponse | Problem]:
    """Customer get WorkflowReadinessInbox

     Customer durable readiness subscription get operation. Observations are bounded and advisory;
    execution remains application-owned. Requires platform-tenant Operations read scope.

    Args:
        id (UUID):
        scope (str):
        app_id (UUID):
        after_sequence (int | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessInboxResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        scope=scope,
        app_id=app_id,
        after_sequence=after_sequence,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,
    scope: str,
    app_id: UUID,
    after_sequence: int | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> OperationWorkflowReadinessInboxResponse | Problem | None:
    """Customer get WorkflowReadinessInbox

     Customer durable readiness subscription get operation. Observations are bounded and advisory;
    execution remains application-owned. Requires platform-tenant Operations read scope.

    Args:
        id (UUID):
        scope (str):
        app_id (UUID):
        after_sequence (int | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessInboxResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            scope=scope,
            app_id=app_id,
            after_sequence=after_sequence,
            limit=limit,
        )
    ).parsed
