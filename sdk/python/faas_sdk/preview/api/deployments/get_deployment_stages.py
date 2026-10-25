from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_stage_state import DeploymentStageState
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/{id}/stages".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentStageState | Problem | None:
    if response.status_code == 200:
        response_200 = DeploymentStageState.from_dict(response.json())

        return response_200

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
) -> Response[DeploymentStageState | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeploymentStageState | Problem]:
    """Get per-deploy closed-stage summary (ADR-117 follow-up).

     Returns the closed 6-stage summary for a deployment. Companion
    to `/v1/deployments/{id}/logs` (which streams `event: stage`
    frames during a live deploy) and `/v1/deployments/{id}` (which
    returns the typed deployment row). This endpoint serves the
    post-stream summary use case — `gregale deploys show <id>` and
    the future dashboard widget.

    The body is the same JSON shape already stored on
    `deployments.stage_state` (ADR-117, migration 00302). The
    handler does NOT add a typed DTO — the column's jsonb IS the
    wire. The closed vocabulary (`source_download` /
    `dependency_restore` / `image_build` / `security_scan` /
    `snapshot_prepare` / `readiness`) is enforced at the
    database layer by `deployments_stage_state_current_check`,
    so a malformed row would never reach the wire. The
    `current` field is the stage the deploy is in right now
    (empty once the deployment is live, failed or cancelled);
    `history` lists the closed rows in transition order
    (oldest → newest), each carrying server-measured
    `duration_ms` so the CLI / dashboard don't have to trust
    a 2s-tick reconstruction.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account (IDOR-safe;
        no account-existence leak).

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentStageState | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> DeploymentStageState | Problem | None:
    """Get per-deploy closed-stage summary (ADR-117 follow-up).

     Returns the closed 6-stage summary for a deployment. Companion
    to `/v1/deployments/{id}/logs` (which streams `event: stage`
    frames during a live deploy) and `/v1/deployments/{id}` (which
    returns the typed deployment row). This endpoint serves the
    post-stream summary use case — `gregale deploys show <id>` and
    the future dashboard widget.

    The body is the same JSON shape already stored on
    `deployments.stage_state` (ADR-117, migration 00302). The
    handler does NOT add a typed DTO — the column's jsonb IS the
    wire. The closed vocabulary (`source_download` /
    `dependency_restore` / `image_build` / `security_scan` /
    `snapshot_prepare` / `readiness`) is enforced at the
    database layer by `deployments_stage_state_current_check`,
    so a malformed row would never reach the wire. The
    `current` field is the stage the deploy is in right now
    (empty once the deployment is live, failed or cancelled);
    `history` lists the closed rows in transition order
    (oldest → newest), each carrying server-measured
    `duration_ms` so the CLI / dashboard don't have to trust
    a 2s-tick reconstruction.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account (IDOR-safe;
        no account-existence leak).

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentStageState | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DeploymentStageState | Problem]:
    """Get per-deploy closed-stage summary (ADR-117 follow-up).

     Returns the closed 6-stage summary for a deployment. Companion
    to `/v1/deployments/{id}/logs` (which streams `event: stage`
    frames during a live deploy) and `/v1/deployments/{id}` (which
    returns the typed deployment row). This endpoint serves the
    post-stream summary use case — `gregale deploys show <id>` and
    the future dashboard widget.

    The body is the same JSON shape already stored on
    `deployments.stage_state` (ADR-117, migration 00302). The
    handler does NOT add a typed DTO — the column's jsonb IS the
    wire. The closed vocabulary (`source_download` /
    `dependency_restore` / `image_build` / `security_scan` /
    `snapshot_prepare` / `readiness`) is enforced at the
    database layer by `deployments_stage_state_current_check`,
    so a malformed row would never reach the wire. The
    `current` field is the stage the deploy is in right now
    (empty once the deployment is live, failed or cancelled);
    `history` lists the closed rows in transition order
    (oldest → newest), each carrying server-measured
    `duration_ms` so the CLI / dashboard don't have to trust
    a 2s-tick reconstruction.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account (IDOR-safe;
        no account-existence leak).

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentStageState | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> DeploymentStageState | Problem | None:
    """Get per-deploy closed-stage summary (ADR-117 follow-up).

     Returns the closed 6-stage summary for a deployment. Companion
    to `/v1/deployments/{id}/logs` (which streams `event: stage`
    frames during a live deploy) and `/v1/deployments/{id}` (which
    returns the typed deployment row). This endpoint serves the
    post-stream summary use case — `gregale deploys show <id>` and
    the future dashboard widget.

    The body is the same JSON shape already stored on
    `deployments.stage_state` (ADR-117, migration 00302). The
    handler does NOT add a typed DTO — the column's jsonb IS the
    wire. The closed vocabulary (`source_download` /
    `dependency_restore` / `image_build` / `security_scan` /
    `snapshot_prepare` / `readiness`) is enforced at the
    database layer by `deployments_stage_state_current_check`,
    so a malformed row would never reach the wire. The
    `current` field is the stage the deploy is in right now
    (empty once the deployment is live, failed or cancelled);
    `history` lists the closed rows in transition order
    (oldest → newest), each carrying server-measured
    `duration_ms` so the CLI / dashboard don't have to trust
    a 2s-tick reconstruction.

    A 404 is returned when:
      - the deployment row does not exist,
      - the deployment belongs to a different account (IDOR-safe;
        no account-existence leak).

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentStageState | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
