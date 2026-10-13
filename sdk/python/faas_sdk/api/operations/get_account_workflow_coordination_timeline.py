from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_coordination_timeline import OperationWorkflowCoordinationTimeline
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    json_tenant_id: str | Unset = UNSET
    if not isinstance(tenant_id, Unset):
        json_tenant_id = str(tenant_id)
    params["tenant_id"] = json_tenant_id

    params["subject_type"] = subject_type

    params["subject_id"] = subject_id

    params["workflow"] = workflow

    params["instance_id"] = instance_id

    params["offset"] = offset

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/workflow-readiness/timeline".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationWorkflowCoordinationTimeline | Problem | None:
    if response.status_code == 200:
        response_200 = OperationWorkflowCoordinationTimeline.from_dict(response.json())

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
) -> Response[OperationWorkflowCoordinationTimeline | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
) -> Response[OperationWorkflowCoordinationTimeline | Problem]:
    """Read account workflow coordination timeline

     GetAccountWorkflowCoordinationTimeline. Read a bounded chronological projection from current
    retained readiness inboxes for an exact subject and workflow instance. Observation clocks are server
    times; latest snapshots do not reconstruct every past edit. This is not a complete audit trail and
    does not authorize execution. Sources are read independently and live offset pages may move. Account
    access requires read scope and MFA; customer access requires Operations read.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        offset (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowCoordinationTimeline | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        tenant_id=tenant_id,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        instance_id=instance_id,
        offset=offset,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
) -> OperationWorkflowCoordinationTimeline | Problem | None:
    """Read account workflow coordination timeline

     GetAccountWorkflowCoordinationTimeline. Read a bounded chronological projection from current
    retained readiness inboxes for an exact subject and workflow instance. Observation clocks are server
    times; latest snapshots do not reconstruct every past edit. This is not a complete audit trail and
    does not authorize execution. Sources are read independently and live offset pages may move. Account
    access requires read scope and MFA; customer access requires Operations read.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        offset (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowCoordinationTimeline | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        tenant_id=tenant_id,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        instance_id=instance_id,
        offset=offset,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
) -> Response[OperationWorkflowCoordinationTimeline | Problem]:
    """Read account workflow coordination timeline

     GetAccountWorkflowCoordinationTimeline. Read a bounded chronological projection from current
    retained readiness inboxes for an exact subject and workflow instance. Observation clocks are server
    times; latest snapshots do not reconstruct every past edit. This is not a complete audit trail and
    does not authorize execution. Sources are read independently and live offset pages may move. Account
    access requires read scope and MFA; customer access requires Operations read.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        offset (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationWorkflowCoordinationTimeline | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        tenant_id=tenant_id,
        subject_type=subject_type,
        subject_id=subject_id,
        workflow=workflow,
        instance_id=instance_id,
        offset=offset,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    scope: str,
    tenant_id: UUID | Unset = UNSET,
    subject_type: str,
    subject_id: str,
    workflow: str,
    instance_id: str,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
) -> OperationWorkflowCoordinationTimeline | Problem | None:
    """Read account workflow coordination timeline

     GetAccountWorkflowCoordinationTimeline. Read a bounded chronological projection from current
    retained readiness inboxes for an exact subject and workflow instance. Observation clocks are server
    times; latest snapshots do not reconstruct every past edit. This is not a complete audit trail and
    does not authorize execution. Sources are read independently and live offset pages may move. Account
    access requires read scope and MFA; customer access requires Operations read.

    Args:
        slug (str):
        scope (str):
        tenant_id (UUID | Unset):
        subject_type (str):
        subject_id (str):
        workflow (str):
        instance_id (str):
        offset (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationWorkflowCoordinationTimeline | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
            tenant_id=tenant_id,
            subject_type=subject_type,
            subject_id=subject_id,
            workflow=workflow,
            instance_id=instance_id,
            offset=offset,
            limit=limit,
        )
    ).parsed
