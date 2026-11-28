from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.task_tool_release_intent import TaskToolReleaseIntent
from ...models.task_tool_release_intent_record import TaskToolReleaseIntentRecord
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: TaskToolReleaseIntent,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/task-tool-release-intents/{id}".format(
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

    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429

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
    body: TaskToolReleaseIntent,
) -> Response[Any | TaskToolReleaseIntentRecord]:
    """Save an immutable Task stack review; requires deploy-write scope and MFA policy

     Save an immutable Node Task stack review under a caller-chosen stable identity. Bind its source
    fingerprint, exact native app snapshots and requesting key to the account. Existing identical
    requests return the retained record; changed reviews conflict. The review expires fifteen minutes
    after preparation and grants no runtime authority. Retain at most 256 intent identities per account.

    Args:
        id (str):
        body (TaskToolReleaseIntent): Immutable review metadata bounded to 64 KiB. Required setup
            remains unverified. No source paths, executable arguments, database URLs, credentials or
            credential references.

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
    body: TaskToolReleaseIntent,
) -> Any | TaskToolReleaseIntentRecord | None:
    """Save an immutable Task stack review; requires deploy-write scope and MFA policy

     Save an immutable Node Task stack review under a caller-chosen stable identity. Bind its source
    fingerprint, exact native app snapshots and requesting key to the account. Existing identical
    requests return the retained record; changed reviews conflict. The review expires fifteen minutes
    after preparation and grants no runtime authority. Retain at most 256 intent identities per account.

    Args:
        id (str):
        body (TaskToolReleaseIntent): Immutable review metadata bounded to 64 KiB. Required setup
            remains unverified. No source paths, executable arguments, database URLs, credentials or
            credential references.

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
    body: TaskToolReleaseIntent,
) -> Response[Any | TaskToolReleaseIntentRecord]:
    """Save an immutable Task stack review; requires deploy-write scope and MFA policy

     Save an immutable Node Task stack review under a caller-chosen stable identity. Bind its source
    fingerprint, exact native app snapshots and requesting key to the account. Existing identical
    requests return the retained record; changed reviews conflict. The review expires fifteen minutes
    after preparation and grants no runtime authority. Retain at most 256 intent identities per account.

    Args:
        id (str):
        body (TaskToolReleaseIntent): Immutable review metadata bounded to 64 KiB. Required setup
            remains unverified. No source paths, executable arguments, database URLs, credentials or
            credential references.

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
    body: TaskToolReleaseIntent,
) -> Any | TaskToolReleaseIntentRecord | None:
    """Save an immutable Task stack review; requires deploy-write scope and MFA policy

     Save an immutable Node Task stack review under a caller-chosen stable identity. Bind its source
    fingerprint, exact native app snapshots and requesting key to the account. Existing identical
    requests return the retained record; changed reviews conflict. The review expires fifteen minutes
    after preparation and grants no runtime authority. Retain at most 256 intent identities per account.

    Args:
        id (str):
        body (TaskToolReleaseIntent): Immutable review metadata bounded to 64 KiB. Required setup
            remains unverified. No source paths, executable arguments, database URLs, credentials or
            credential references.

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
