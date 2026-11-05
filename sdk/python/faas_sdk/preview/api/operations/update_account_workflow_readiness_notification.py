from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_readiness_coordination_request import OperationWorkflowReadinessCoordinationRequest
from ...models.operation_workflow_readiness_notification import OperationWorkflowReadinessNotification
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    sequence: int,
    *,
    body: OperationWorkflowReadinessCoordinationRequest,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["scope"] = scope

    json_tenant_id: str | Unset = UNSET
    if not isinstance(tenant_id, Unset):
        json_tenant_id = str(tenant_id)
    params["tenant_id"] = json_tenant_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/apps/{slug}/workflow-readiness/subscriptions/{id}/notifications/{sequence}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            sequence=quote(str(sequence), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowReadinessNotification | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowReadinessNotification.from_dict(response.json())

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
) -> Response[OperationWorkflowReadinessNotification | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    sequence: int,
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowReadinessCoordinationRequest,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
) -> Response[OperationWorkflowReadinessNotification | Problem]:
    """Update Account readiness notification

     UpdateAccountWorkflowReadinessNotification. Replace shared inbox coordination with a matching
    revision. Requires account deploy-write and MFA or customer operations manage. Actor derives from
    authentication. Revision zero addresses an untouched notification.

    Args:
        slug (str):
        id (UUID):
        sequence (int):
        scope (str):
        tenant_id (UUID | Unset):
        body (OperationWorkflowReadinessCoordinationRequest):
            OperationWorkflowReadinessCoordinationRequest. Shared inbox coordination with optimistic
            revision checks; omission of a follow-up time clears it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessNotification | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        sequence=sequence,
        body=body,
        scope=scope,
        tenant_id=tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    sequence: int,
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowReadinessCoordinationRequest,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
) -> OperationWorkflowReadinessNotification | Problem | None:
    """Update Account readiness notification

     UpdateAccountWorkflowReadinessNotification. Replace shared inbox coordination with a matching
    revision. Requires account deploy-write and MFA or customer operations manage. Actor derives from
    authentication. Revision zero addresses an untouched notification.

    Args:
        slug (str):
        id (UUID):
        sequence (int):
        scope (str):
        tenant_id (UUID | Unset):
        body (OperationWorkflowReadinessCoordinationRequest):
            OperationWorkflowReadinessCoordinationRequest. Shared inbox coordination with optimistic
            revision checks; omission of a follow-up time clears it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessNotification | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        sequence=sequence,
        client=client,
        body=body,
        scope=scope,
        tenant_id=tenant_id,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    sequence: int,
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowReadinessCoordinationRequest,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
) -> Response[OperationWorkflowReadinessNotification | Problem]:
    """Update Account readiness notification

     UpdateAccountWorkflowReadinessNotification. Replace shared inbox coordination with a matching
    revision. Requires account deploy-write and MFA or customer operations manage. Actor derives from
    authentication. Revision zero addresses an untouched notification.

    Args:
        slug (str):
        id (UUID):
        sequence (int):
        scope (str):
        tenant_id (UUID | Unset):
        body (OperationWorkflowReadinessCoordinationRequest):
            OperationWorkflowReadinessCoordinationRequest. Shared inbox coordination with optimistic
            revision checks; omission of a follow-up time clears it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowReadinessNotification | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        sequence=sequence,
        body=body,
        scope=scope,
        tenant_id=tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    sequence: int,
    *,
    client: AuthenticatedClient,
    body: OperationWorkflowReadinessCoordinationRequest,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
) -> OperationWorkflowReadinessNotification | Problem | None:
    """Update Account readiness notification

     UpdateAccountWorkflowReadinessNotification. Replace shared inbox coordination with a matching
    revision. Requires account deploy-write and MFA or customer operations manage. Actor derives from
    authentication. Revision zero addresses an untouched notification.

    Args:
        slug (str):
        id (UUID):
        sequence (int):
        scope (str):
        tenant_id (UUID | Unset):
        body (OperationWorkflowReadinessCoordinationRequest):
            OperationWorkflowReadinessCoordinationRequest. Shared inbox coordination with optimistic
            revision checks; omission of a follow-up time clears it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowReadinessNotification | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            sequence=sequence,
            client=client,
            body=body,
            scope=scope,
            tenant_id=tenant_id,
        )
    ).parsed
