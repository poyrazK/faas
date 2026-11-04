from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.control_project_release_action import ControlProjectReleaseAction
from ...models.problem import Problem
from ...models.project_migration_recovery_request import ProjectMigrationRecoveryRequest
from ...models.project_release_control_request import ProjectReleaseControlRequest
from ...models.project_submission_receipt import ProjectSubmissionReceipt
from ...types import Response


def _get_kwargs(
    attempt_id: str,
    action: ControlProjectReleaseAction,
    *,
    body: ProjectMigrationRecoveryRequest | ProjectReleaseControlRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/project-submissions/{attempt_id}/{action}".format(
            attempt_id=quote(str(attempt_id), safe=""),
            action=quote(str(action), safe=""),
        ),
    }

    if isinstance(body, ProjectReleaseControlRequest):
        _kwargs["json"] = body.to_dict()
    else:
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectSubmissionReceipt | None:
    if response.status_code == 200:
        response_200 = ProjectSubmissionReceipt.from_dict(response.json())

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

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
) -> Response[Problem | ProjectSubmissionReceipt]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    attempt_id: str,
    action: ControlProjectReleaseAction,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectMigrationRecoveryRequest | ProjectReleaseControlRequest,
) -> Response[Problem | ProjectSubmissionReceipt]:
    """Control an owned progressive deployment or admit planned schema cleanup.

     Persist a generation-fenced operator decision. Before full activation abort preserves the unchanged
    baseline. At full traffic new unassigned work is blocked while fresh baseline health permits paired
    graph/config restoration. Recovery retries that bounded verification without forcing health or
    overwriting newer intent. Reusing the same request identity and generation is idempotent. Contract
    admission additionally requires fresh current health and drained retained versions and work; it
    permanently fences retired deployments before queuing the source-declared cleanup task.

    Args:
        attempt_id (str):
        action (ControlProjectReleaseAction):
        body (ProjectMigrationRecoveryRequest | ProjectReleaseControlRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectSubmissionReceipt]
    """

    kwargs = _get_kwargs(
        attempt_id=attempt_id,
        action=action,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    attempt_id: str,
    action: ControlProjectReleaseAction,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectMigrationRecoveryRequest | ProjectReleaseControlRequest,
) -> Problem | ProjectSubmissionReceipt | None:
    """Control an owned progressive deployment or admit planned schema cleanup.

     Persist a generation-fenced operator decision. Before full activation abort preserves the unchanged
    baseline. At full traffic new unassigned work is blocked while fresh baseline health permits paired
    graph/config restoration. Recovery retries that bounded verification without forcing health or
    overwriting newer intent. Reusing the same request identity and generation is idempotent. Contract
    admission additionally requires fresh current health and drained retained versions and work; it
    permanently fences retired deployments before queuing the source-declared cleanup task.

    Args:
        attempt_id (str):
        action (ControlProjectReleaseAction):
        body (ProjectMigrationRecoveryRequest | ProjectReleaseControlRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectSubmissionReceipt
    """

    return sync_detailed(
        attempt_id=attempt_id,
        action=action,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    attempt_id: str,
    action: ControlProjectReleaseAction,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectMigrationRecoveryRequest | ProjectReleaseControlRequest,
) -> Response[Problem | ProjectSubmissionReceipt]:
    """Control an owned progressive deployment or admit planned schema cleanup.

     Persist a generation-fenced operator decision. Before full activation abort preserves the unchanged
    baseline. At full traffic new unassigned work is blocked while fresh baseline health permits paired
    graph/config restoration. Recovery retries that bounded verification without forcing health or
    overwriting newer intent. Reusing the same request identity and generation is idempotent. Contract
    admission additionally requires fresh current health and drained retained versions and work; it
    permanently fences retired deployments before queuing the source-declared cleanup task.

    Args:
        attempt_id (str):
        action (ControlProjectReleaseAction):
        body (ProjectMigrationRecoveryRequest | ProjectReleaseControlRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectSubmissionReceipt]
    """

    kwargs = _get_kwargs(
        attempt_id=attempt_id,
        action=action,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    attempt_id: str,
    action: ControlProjectReleaseAction,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectMigrationRecoveryRequest | ProjectReleaseControlRequest,
) -> Problem | ProjectSubmissionReceipt | None:
    """Control an owned progressive deployment or admit planned schema cleanup.

     Persist a generation-fenced operator decision. Before full activation abort preserves the unchanged
    baseline. At full traffic new unassigned work is blocked while fresh baseline health permits paired
    graph/config restoration. Recovery retries that bounded verification without forcing health or
    overwriting newer intent. Reusing the same request identity and generation is idempotent. Contract
    admission additionally requires fresh current health and drained retained versions and work; it
    permanently fences retired deployments before queuing the source-declared cleanup task.

    Args:
        attempt_id (str):
        action (ControlProjectReleaseAction):
        body (ProjectMigrationRecoveryRequest | ProjectReleaseControlRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectSubmissionReceipt
    """

    return (
        await asyncio_detailed(
            attempt_id=attempt_id,
            action=action,
            client=client,
            body=body,
        )
    ).parsed
