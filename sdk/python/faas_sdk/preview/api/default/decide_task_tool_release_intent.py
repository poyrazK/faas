from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.task_tool_release_intent_decision import TaskToolReleaseIntentDecision
from ...models.task_tool_release_intent_record import TaskToolReleaseIntentRecord
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: TaskToolReleaseIntentDecision,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/task-tool-release-intents/{id}/decision".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | TaskToolReleaseIntentRecord | None:
    if response.status_code == 200:
        response_200 = TaskToolReleaseIntentRecord.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | TaskToolReleaseIntentRecord]:
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
    body: TaskToolReleaseIntentDecision,
) -> Response[Any | TaskToolReleaseIntentRecord]:
    """Record an exact-digest owner decision; requires personal non-agent administrator and MFA policy

     An unscoped personal administrator approves, rejects or revokes the exact intent digest and retained
    release fingerprint. Agent and MCP-login credentials cannot decide. Approval revalidates native app
    and key facts. Owner cancellation uses the retained review even after source files or requesting-key
    authority disappear. Decisions never renew the fifteen-minute deadline. A separate authorize
    decision from a non-agent admin API key can bind one local Task execution identity to a salted
    private binding digest. It requires an approved fresh review and cannot replace or renew an existing
    execution claim.

    Args:
        id (str):
        body (TaskToolReleaseIntentDecision): Owner verdict bound to an immutable Task intent
            digest and its retained source fingerprint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | TaskToolReleaseIntentRecord]
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
    body: TaskToolReleaseIntentDecision,
) -> Any | TaskToolReleaseIntentRecord | None:
    """Record an exact-digest owner decision; requires personal non-agent administrator and MFA policy

     An unscoped personal administrator approves, rejects or revokes the exact intent digest and retained
    release fingerprint. Agent and MCP-login credentials cannot decide. Approval revalidates native app
    and key facts. Owner cancellation uses the retained review even after source files or requesting-key
    authority disappear. Decisions never renew the fifteen-minute deadline. A separate authorize
    decision from a non-agent admin API key can bind one local Task execution identity to a salted
    private binding digest. It requires an approved fresh review and cannot replace or renew an existing
    execution claim.

    Args:
        id (str):
        body (TaskToolReleaseIntentDecision): Owner verdict bound to an immutable Task intent
            digest and its retained source fingerprint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | TaskToolReleaseIntentRecord
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
    body: TaskToolReleaseIntentDecision,
) -> Response[Any | TaskToolReleaseIntentRecord]:
    """Record an exact-digest owner decision; requires personal non-agent administrator and MFA policy

     An unscoped personal administrator approves, rejects or revokes the exact intent digest and retained
    release fingerprint. Agent and MCP-login credentials cannot decide. Approval revalidates native app
    and key facts. Owner cancellation uses the retained review even after source files or requesting-key
    authority disappear. Decisions never renew the fifteen-minute deadline. A separate authorize
    decision from a non-agent admin API key can bind one local Task execution identity to a salted
    private binding digest. It requires an approved fresh review and cannot replace or renew an existing
    execution claim.

    Args:
        id (str):
        body (TaskToolReleaseIntentDecision): Owner verdict bound to an immutable Task intent
            digest and its retained source fingerprint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | TaskToolReleaseIntentRecord]
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
    body: TaskToolReleaseIntentDecision,
) -> Any | TaskToolReleaseIntentRecord | None:
    """Record an exact-digest owner decision; requires personal non-agent administrator and MFA policy

     An unscoped personal administrator approves, rejects or revokes the exact intent digest and retained
    release fingerprint. Agent and MCP-login credentials cannot decide. Approval revalidates native app
    and key facts. Owner cancellation uses the retained review even after source files or requesting-key
    authority disappear. Decisions never renew the fifteen-minute deadline. A separate authorize
    decision from a non-agent admin API key can bind one local Task execution identity to a salted
    private binding digest. It requires an approved fresh review and cannot replace or renew an existing
    execution claim.

    Args:
        id (str):
        body (TaskToolReleaseIntentDecision): Owner verdict bound to an immutable Task intent
            digest and its retained source fingerprint.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | TaskToolReleaseIntentRecord
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
