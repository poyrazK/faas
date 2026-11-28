import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_recovery_history import WorkflowRecoveryHistory
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    workflow: str | Unset = UNSET,
    run_id: UUID | Unset = UNSET,
    policy_version: int | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["workflow"] = workflow

    json_run_id: str | Unset = UNSET
    if not isinstance(run_id, Unset):
        json_run_id = str(run_id)
    params["run_id"] = json_run_id

    params["policy_version"] = policy_version

    json_created_after: str | Unset = UNSET
    if not isinstance(created_after, Unset):
        json_created_after = created_after.isoformat()
    params["created_after"] = json_created_after

    json_created_before: str | Unset = UNSET
    if not isinstance(created_before, Unset):
        json_created_before = created_before.isoformat()
    params["created_before"] = json_created_before

    params["after"] = after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/recovery-policy/history".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowRecoveryHistory | None:
    if response.status_code == 200:
        response_200 = WorkflowRecoveryHistory.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowRecoveryHistory]:
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
    workflow: str | Unset = UNSET,
    run_id: UUID | Unset = UNSET,
    policy_version: int | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Response[Problem | WorkflowRecoveryHistory]:
    """Inspect retained recovery automation history

     Read-only timeline of retained review decisions, controls, retry events, durable policy changes,
    worker reason transitions and health incident events, alongside latest observations. Durable events
    are captured prospectively with their state mutations, retained for 30 days and capped at 10000 per
    app. Results are ordered newest first with stable identifier tie breaks. Cursors fix the time window
    and durable event sequence ceiling; later appends are excluded. Retention can remove events, and
    latest observations can change across pages. Workflow, run and policy filters omit records without
    those associations. No execution inputs or outputs are returned.

    Args:
        slug (str):
        workflow (str | Unset):
        run_id (UUID | Unset):
        policy_version (int | Unset):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryHistory]
    """

    kwargs = _get_kwargs(
        slug=slug,
        workflow=workflow,
        run_id=run_id,
        policy_version=policy_version,
        created_after=created_after,
        created_before=created_before,
        after=after,
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
    workflow: str | Unset = UNSET,
    run_id: UUID | Unset = UNSET,
    policy_version: int | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Problem | WorkflowRecoveryHistory | None:
    """Inspect retained recovery automation history

     Read-only timeline of retained review decisions, controls, retry events, durable policy changes,
    worker reason transitions and health incident events, alongside latest observations. Durable events
    are captured prospectively with their state mutations, retained for 30 days and capped at 10000 per
    app. Results are ordered newest first with stable identifier tie breaks. Cursors fix the time window
    and durable event sequence ceiling; later appends are excluded. Retention can remove events, and
    latest observations can change across pages. Workflow, run and policy filters omit records without
    those associations. No execution inputs or outputs are returned.

    Args:
        slug (str):
        workflow (str | Unset):
        run_id (UUID | Unset):
        policy_version (int | Unset):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryHistory
    """

    return sync_detailed(
        slug=slug,
        client=client,
        workflow=workflow,
        run_id=run_id,
        policy_version=policy_version,
        created_after=created_after,
        created_before=created_before,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow: str | Unset = UNSET,
    run_id: UUID | Unset = UNSET,
    policy_version: int | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Response[Problem | WorkflowRecoveryHistory]:
    """Inspect retained recovery automation history

     Read-only timeline of retained review decisions, controls, retry events, durable policy changes,
    worker reason transitions and health incident events, alongside latest observations. Durable events
    are captured prospectively with their state mutations, retained for 30 days and capped at 10000 per
    app. Results are ordered newest first with stable identifier tie breaks. Cursors fix the time window
    and durable event sequence ceiling; later appends are excluded. Retention can remove events, and
    latest observations can change across pages. Workflow, run and policy filters omit records without
    those associations. No execution inputs or outputs are returned.

    Args:
        slug (str):
        workflow (str | Unset):
        run_id (UUID | Unset):
        policy_version (int | Unset):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryHistory]
    """

    kwargs = _get_kwargs(
        slug=slug,
        workflow=workflow,
        run_id=run_id,
        policy_version=policy_version,
        created_after=created_after,
        created_before=created_before,
        after=after,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    workflow: str | Unset = UNSET,
    run_id: UUID | Unset = UNSET,
    policy_version: int | Unset = UNSET,
    created_after: datetime.datetime | Unset = UNSET,
    created_before: datetime.datetime | Unset = UNSET,
    after: str | Unset = UNSET,
    limit: int | Unset = 25,
) -> Problem | WorkflowRecoveryHistory | None:
    """Inspect retained recovery automation history

     Read-only timeline of retained review decisions, controls, retry events, durable policy changes,
    worker reason transitions and health incident events, alongside latest observations. Durable events
    are captured prospectively with their state mutations, retained for 30 days and capped at 10000 per
    app. Results are ordered newest first with stable identifier tie breaks. Cursors fix the time window
    and durable event sequence ceiling; later appends are excluded. Retention can remove events, and
    latest observations can change across pages. Workflow, run and policy filters omit records without
    those associations. No execution inputs or outputs are returned.

    Args:
        slug (str):
        workflow (str | Unset):
        run_id (UUID | Unset):
        policy_version (int | Unset):
        created_after (datetime.datetime | Unset):
        created_before (datetime.datetime | Unset):
        after (str | Unset):
        limit (int | Unset):  Default: 25.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryHistory
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            workflow=workflow,
            run_id=run_id,
            policy_version=policy_version,
            created_after=created_after,
            created_before=created_before,
            after=after,
            limit=limit,
        )
    ).parsed
