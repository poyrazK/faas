from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.task_tool_recovery_restore_request import TaskToolRecoveryRestoreRequest
from ...models.task_tool_release_intent_record import TaskToolReleaseIntentRecord
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: TaskToolRecoveryRestoreRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/task-tool-release-intents/{id}/recovery/restore".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | Problem | TaskToolReleaseIntentRecord | None:
    if response.status_code == 200:
        response_200 = TaskToolReleaseIntentRecord.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 422:
        response_422 = cast(Any, None)
        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | Problem | TaskToolReleaseIntentRecord]:
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
    body: TaskToolRecoveryRestoreRequest,
) -> Response[Any | Problem | TaskToolReleaseIntentRecord]:
    """Restore the exact owner-approved web deployment once; requires the requesting key and MFA policy

     The trusted local CLI checks retained Task compatibility under the release advisory lock and submits
    private claim possession plus fresh evidence. Native storage locks the recovery approval,
    credentials, app snapshots, running generations and observer heartbeat. The ordinary traffic
    endpoint's approval, policy, readiness, canary, binding, lifecycle and serving-CAS gates remain
    mandatory. Traffic and approval consumption commit in the same transaction. Exact repeats return the
    retained commit receipt after expiry or revocation without replaying traffic. A receipt records a
    historical traffic commit, not current runtime health. No migration, worker retirement or automatic
    claim takeover is performed.

    Args:
        id (str):
        body (TaskToolRecoveryRestoreRequest): Trusted local CLI request for the exact separately
            owner-approved restore. Agent tools accept public identities only; private claim material
            and binding/evidence details are resolved locally. No automatic retry or deadline renewal.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | TaskToolReleaseIntentRecord]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: TaskToolRecoveryRestoreRequest,
) -> Any | Problem | TaskToolReleaseIntentRecord | None:
    """Restore the exact owner-approved web deployment once; requires the requesting key and MFA policy

     The trusted local CLI checks retained Task compatibility under the release advisory lock and submits
    private claim possession plus fresh evidence. Native storage locks the recovery approval,
    credentials, app snapshots, running generations and observer heartbeat. The ordinary traffic
    endpoint's approval, policy, readiness, canary, binding, lifecycle and serving-CAS gates remain
    mandatory. Traffic and approval consumption commit in the same transaction. Exact repeats return the
    retained commit receipt after expiry or revocation without replaying traffic. A receipt records a
    historical traffic commit, not current runtime health. No migration, worker retirement or automatic
    claim takeover is performed.

    Args:
        id (str):
        body (TaskToolRecoveryRestoreRequest): Trusted local CLI request for the exact separately
            owner-approved restore. Agent tools accept public identities only; private claim material
            and binding/evidence details are resolved locally. No automatic retry or deadline renewal.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | TaskToolReleaseIntentRecord
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: TaskToolRecoveryRestoreRequest,
) -> Response[Any | Problem | TaskToolReleaseIntentRecord]:
    """Restore the exact owner-approved web deployment once; requires the requesting key and MFA policy

     The trusted local CLI checks retained Task compatibility under the release advisory lock and submits
    private claim possession plus fresh evidence. Native storage locks the recovery approval,
    credentials, app snapshots, running generations and observer heartbeat. The ordinary traffic
    endpoint's approval, policy, readiness, canary, binding, lifecycle and serving-CAS gates remain
    mandatory. Traffic and approval consumption commit in the same transaction. Exact repeats return the
    retained commit receipt after expiry or revocation without replaying traffic. A receipt records a
    historical traffic commit, not current runtime health. No migration, worker retirement or automatic
    claim takeover is performed.

    Args:
        id (str):
        body (TaskToolRecoveryRestoreRequest): Trusted local CLI request for the exact separately
            owner-approved restore. Agent tools accept public identities only; private claim material
            and binding/evidence details are resolved locally. No automatic retry or deadline renewal.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | TaskToolReleaseIntentRecord]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: TaskToolRecoveryRestoreRequest,
) -> Any | Problem | TaskToolReleaseIntentRecord | None:
    """Restore the exact owner-approved web deployment once; requires the requesting key and MFA policy

     The trusted local CLI checks retained Task compatibility under the release advisory lock and submits
    private claim possession plus fresh evidence. Native storage locks the recovery approval,
    credentials, app snapshots, running generations and observer heartbeat. The ordinary traffic
    endpoint's approval, policy, readiness, canary, binding, lifecycle and serving-CAS gates remain
    mandatory. Traffic and approval consumption commit in the same transaction. Exact repeats return the
    retained commit receipt after expiry or revocation without replaying traffic. A receipt records a
    historical traffic commit, not current runtime health. No migration, worker retirement or automatic
    claim takeover is performed.

    Args:
        id (str):
        body (TaskToolRecoveryRestoreRequest): Trusted local CLI request for the exact separately
            owner-approved restore. Agent tools accept public identities only; private claim material
            and binding/evidence details are resolved locally. No automatic retry or deadline renewal.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | TaskToolReleaseIntentRecord
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
