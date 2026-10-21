from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.apply_response import ApplyResponse
from ...models.problem import Problem
from ...models.project_apply_request import ProjectApplyRequest
from ...models.safe_apply_project_project_add_services import (
    SafeApplyProjectProjectAddServices,
)
from ...models.safe_apply_project_project_progressive import (
    SafeApplyProjectProjectProgressive,
)
from ...models.safe_apply_project_project_release_mode import (
    SafeApplyProjectProjectReleaseMode,
)
from ...models.safe_apply_project_project_stabilization import (
    SafeApplyProjectProjectStabilization,
)
from ...models.safe_apply_project_project_worker_health import (
    SafeApplyProjectProjectWorkerHealth,
)
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    project_add_services: SafeApplyProjectProjectAddServices | Unset = UNSET,
    project_stabilization: SafeApplyProjectProjectStabilization | Unset = UNSET,
    project_progressive: SafeApplyProjectProjectProgressive | Unset = UNSET,
    project_worker_health: SafeApplyProjectProjectWorkerHealth | Unset = UNSET,
    project_release_mode: SafeApplyProjectProjectReleaseMode | Unset = UNSET,
    deployment_attempt_id: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(project_add_services, Unset):
        headers["Project-Add-Services"] = str(project_add_services)

    if not isinstance(project_stabilization, Unset):
        headers["Project-Stabilization"] = str(project_stabilization)

    if not isinstance(project_progressive, Unset):
        headers["Project-Progressive"] = str(project_progressive)

    if not isinstance(project_worker_health, Unset):
        headers["Project-Worker-Health"] = str(project_worker_health)

    if not isinstance(project_release_mode, Unset):
        headers["Project-Release-Mode"] = str(project_release_mode)

    headers["Deployment-Attempt-ID"] = deployment_attempt_id

    params: dict[str, Any] = {}

    params["plan_token"] = plan_token

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/safe-deploy",
        "params": params,
    }

    _kwargs["files"] = body.to_multipart()

    headers["Content-Type"] = "multipart/form-data; boundary=+++"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ApplyResponse.from_dict(response.json())

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
) -> Response[ApplyResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    project_add_services: SafeApplyProjectProjectAddServices | Unset = UNSET,
    project_stabilization: SafeApplyProjectProjectStabilization | Unset = UNSET,
    project_progressive: SafeApplyProjectProjectProgressive | Unset = UNSET,
    project_worker_health: SafeApplyProjectProjectWorkerHealth | Unset = UNSET,
    project_release_mode: SafeApplyProjectProjectReleaseMode | Unset = UNSET,
    deployment_attempt_id: str,
) -> Response[ApplyResponse | Problem]:
    """Build an existing HTTP project and atomically publish its ready release.

     Requires an active production release set, the complete project, no_triggers=true, and a durable
    attempt. Candidates receive zero traffic and publish only after every candidate completes readiness.
    Project-Add-Services opts into independent HTTP source additions behind native qualification
    admission. All serving services must remain unchanged. Additions cannot combine with configuration,
    secrets, migrations, workers or progressive rollout. Verified releases probe existing critical
    routes and restore the original membership on regression; new services are checked by startup
    readiness. Failed or withdrawn additions remain quota-counted reservations without a default route
    and may be retried with identical settings.

    Args:
        plan_token (str | Unset):
        project_add_services (SafeApplyProjectProjectAddServices | Unset):
        project_stabilization (SafeApplyProjectProjectStabilization | Unset):
        project_progressive (SafeApplyProjectProjectProgressive | Unset):
        project_worker_health (SafeApplyProjectProjectWorkerHealth | Unset):
        project_release_mode (SafeApplyProjectProjectReleaseMode | Unset):
        deployment_attempt_id (str):
        body (ProjectApplyRequest): Multipart body for POST /v1/projects (apply). Same shape as
            ProjectScanRequest; the apply handler resolves AppIDs and
            inserts crons in a follow-up pass.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        plan_token=plan_token,
        project_add_services=project_add_services,
        project_stabilization=project_stabilization,
        project_progressive=project_progressive,
        project_worker_health=project_worker_health,
        project_release_mode=project_release_mode,
        deployment_attempt_id=deployment_attempt_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    project_add_services: SafeApplyProjectProjectAddServices | Unset = UNSET,
    project_stabilization: SafeApplyProjectProjectStabilization | Unset = UNSET,
    project_progressive: SafeApplyProjectProjectProgressive | Unset = UNSET,
    project_worker_health: SafeApplyProjectProjectWorkerHealth | Unset = UNSET,
    project_release_mode: SafeApplyProjectProjectReleaseMode | Unset = UNSET,
    deployment_attempt_id: str,
) -> ApplyResponse | Problem | None:
    """Build an existing HTTP project and atomically publish its ready release.

     Requires an active production release set, the complete project, no_triggers=true, and a durable
    attempt. Candidates receive zero traffic and publish only after every candidate completes readiness.
    Project-Add-Services opts into independent HTTP source additions behind native qualification
    admission. All serving services must remain unchanged. Additions cannot combine with configuration,
    secrets, migrations, workers or progressive rollout. Verified releases probe existing critical
    routes and restore the original membership on regression; new services are checked by startup
    readiness. Failed or withdrawn additions remain quota-counted reservations without a default route
    and may be retried with identical settings.

    Args:
        plan_token (str | Unset):
        project_add_services (SafeApplyProjectProjectAddServices | Unset):
        project_stabilization (SafeApplyProjectProjectStabilization | Unset):
        project_progressive (SafeApplyProjectProjectProgressive | Unset):
        project_worker_health (SafeApplyProjectProjectWorkerHealth | Unset):
        project_release_mode (SafeApplyProjectProjectReleaseMode | Unset):
        deployment_attempt_id (str):
        body (ProjectApplyRequest): Multipart body for POST /v1/projects (apply). Same shape as
            ProjectScanRequest; the apply handler resolves AppIDs and
            inserts crons in a follow-up pass.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        plan_token=plan_token,
        project_add_services=project_add_services,
        project_stabilization=project_stabilization,
        project_progressive=project_progressive,
        project_worker_health=project_worker_health,
        project_release_mode=project_release_mode,
        deployment_attempt_id=deployment_attempt_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    project_add_services: SafeApplyProjectProjectAddServices | Unset = UNSET,
    project_stabilization: SafeApplyProjectProjectStabilization | Unset = UNSET,
    project_progressive: SafeApplyProjectProjectProgressive | Unset = UNSET,
    project_worker_health: SafeApplyProjectProjectWorkerHealth | Unset = UNSET,
    project_release_mode: SafeApplyProjectProjectReleaseMode | Unset = UNSET,
    deployment_attempt_id: str,
) -> Response[ApplyResponse | Problem]:
    """Build an existing HTTP project and atomically publish its ready release.

     Requires an active production release set, the complete project, no_triggers=true, and a durable
    attempt. Candidates receive zero traffic and publish only after every candidate completes readiness.
    Project-Add-Services opts into independent HTTP source additions behind native qualification
    admission. All serving services must remain unchanged. Additions cannot combine with configuration,
    secrets, migrations, workers or progressive rollout. Verified releases probe existing critical
    routes and restore the original membership on regression; new services are checked by startup
    readiness. Failed or withdrawn additions remain quota-counted reservations without a default route
    and may be retried with identical settings.

    Args:
        plan_token (str | Unset):
        project_add_services (SafeApplyProjectProjectAddServices | Unset):
        project_stabilization (SafeApplyProjectProjectStabilization | Unset):
        project_progressive (SafeApplyProjectProjectProgressive | Unset):
        project_worker_health (SafeApplyProjectProjectWorkerHealth | Unset):
        project_release_mode (SafeApplyProjectProjectReleaseMode | Unset):
        deployment_attempt_id (str):
        body (ProjectApplyRequest): Multipart body for POST /v1/projects (apply). Same shape as
            ProjectScanRequest; the apply handler resolves AppIDs and
            inserts crons in a follow-up pass.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        plan_token=plan_token,
        project_add_services=project_add_services,
        project_stabilization=project_stabilization,
        project_progressive=project_progressive,
        project_worker_health=project_worker_health,
        project_release_mode=project_release_mode,
        deployment_attempt_id=deployment_attempt_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    project_add_services: SafeApplyProjectProjectAddServices | Unset = UNSET,
    project_stabilization: SafeApplyProjectProjectStabilization | Unset = UNSET,
    project_progressive: SafeApplyProjectProjectProgressive | Unset = UNSET,
    project_worker_health: SafeApplyProjectProjectWorkerHealth | Unset = UNSET,
    project_release_mode: SafeApplyProjectProjectReleaseMode | Unset = UNSET,
    deployment_attempt_id: str,
) -> ApplyResponse | Problem | None:
    """Build an existing HTTP project and atomically publish its ready release.

     Requires an active production release set, the complete project, no_triggers=true, and a durable
    attempt. Candidates receive zero traffic and publish only after every candidate completes readiness.
    Project-Add-Services opts into independent HTTP source additions behind native qualification
    admission. All serving services must remain unchanged. Additions cannot combine with configuration,
    secrets, migrations, workers or progressive rollout. Verified releases probe existing critical
    routes and restore the original membership on regression; new services are checked by startup
    readiness. Failed or withdrawn additions remain quota-counted reservations without a default route
    and may be retried with identical settings.

    Args:
        plan_token (str | Unset):
        project_add_services (SafeApplyProjectProjectAddServices | Unset):
        project_stabilization (SafeApplyProjectProjectStabilization | Unset):
        project_progressive (SafeApplyProjectProjectProgressive | Unset):
        project_worker_health (SafeApplyProjectProjectWorkerHealth | Unset):
        project_release_mode (SafeApplyProjectProjectReleaseMode | Unset):
        deployment_attempt_id (str):
        body (ProjectApplyRequest): Multipart body for POST /v1/projects (apply). Same shape as
            ProjectScanRequest; the apply handler resolves AppIDs and
            inserts crons in a follow-up pass.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            plan_token=plan_token,
            project_add_services=project_add_services,
            project_stabilization=project_stabilization,
            project_progressive=project_progressive,
            project_worker_health=project_worker_health,
            project_release_mode=project_release_mode,
            deployment_attempt_id=deployment_attempt_id,
        )
    ).parsed
