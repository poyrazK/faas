import datetime
from http import HTTPStatus
from io import BytesIO
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...types import UNSET, File, Response, Unset


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    after_sequence: str | Unset = "0",
    group_id: UUID | Unset = UNSET,
    through_sequence: str | Unset = UNSET,
    snapshot_at: datetime.datetime | Unset = UNSET,
    failure_code: list[str] | Unset = UNSET,
    limit: int | Unset = 32,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["after_sequence"] = after_sequence

    json_group_id: str | Unset = UNSET
    if not isinstance(group_id, Unset):
        json_group_id = str(group_id)
    params["group_id"] = json_group_id

    params["through_sequence"] = through_sequence

    json_snapshot_at: str | Unset = UNSET
    if not isinstance(snapshot_at, Unset):
        json_snapshot_at = snapshot_at.isoformat()
    params["snapshot_at"] = json_snapshot_at

    json_failure_code: list[str] | Unset = UNSET
    if not isinstance(failure_code, Unset):
        json_failure_code = failure_code

    params["failure_code"] = json_failure_code

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/export".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            group=quote(str(group), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | File | None:
    if response.status_code == 200:
        response_200 = File(payload=BytesIO(response.content))

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = cast(Any, None)
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

    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | File]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    after_sequence: str | Unset = "0",
    group_id: UUID | Unset = UNSET,
    through_sequence: str | Unset = UNSET,
    snapshot_at: datetime.datetime | Unset = UNSET,
    failure_code: list[str] | Unset = UNSET,
    limit: int | Unset = 32,
) -> Response[Any | File]:
    """Download a read-only dead-letter JSONL page

     Account ownership, read scope, completed MFA and realtime history preview required. Returns one
    manifest line followed by zero to 128 dead_letter lines, ordered by numeric sequence. Payload is the
    retained snapshot with base64 data and metadata. No claim or credential tokens. Every page uses a
    repeatable-read read-only transaction; export never claims, replays, cleans up or extends retention.
    First page captures group_id, through_sequence and snapshot_at. Resume with those three values and
    next_after_sequence; keep failure_code filters unchanged. Later dead-letter transitions are excluded
    by the snapshot cutoff. Replay, deletion and retention cleanup can remove records between pages;
    this is a live export, not an immutable server archive. A recreated group or rewound assignment
    boundary returns 409 realtime_dead_letter_export_changed. Verify the manifest count and complete
    transfer before advancing the cursor. Link headers provide the next page. Empty exports still
    contain a manifest. At most 2 MiB per page. Persist downloads externally for archival.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        after_sequence (str | Unset):  Default: '0'.
        group_id (UUID | Unset):
        through_sequence (str | Unset):
        snapshot_at (datetime.datetime | Unset):
        failure_code (list[str] | Unset):
        limit (int | Unset):  Default: 32.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | File]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        after_sequence=after_sequence,
        group_id=group_id,
        through_sequence=through_sequence,
        snapshot_at=snapshot_at,
        failure_code=failure_code,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    after_sequence: str | Unset = "0",
    group_id: UUID | Unset = UNSET,
    through_sequence: str | Unset = UNSET,
    snapshot_at: datetime.datetime | Unset = UNSET,
    failure_code: list[str] | Unset = UNSET,
    limit: int | Unset = 32,
) -> Any | File | None:
    """Download a read-only dead-letter JSONL page

     Account ownership, read scope, completed MFA and realtime history preview required. Returns one
    manifest line followed by zero to 128 dead_letter lines, ordered by numeric sequence. Payload is the
    retained snapshot with base64 data and metadata. No claim or credential tokens. Every page uses a
    repeatable-read read-only transaction; export never claims, replays, cleans up or extends retention.
    First page captures group_id, through_sequence and snapshot_at. Resume with those three values and
    next_after_sequence; keep failure_code filters unchanged. Later dead-letter transitions are excluded
    by the snapshot cutoff. Replay, deletion and retention cleanup can remove records between pages;
    this is a live export, not an immutable server archive. A recreated group or rewound assignment
    boundary returns 409 realtime_dead_letter_export_changed. Verify the manifest count and complete
    transfer before advancing the cursor. Link headers provide the next page. Empty exports still
    contain a manifest. At most 2 MiB per page. Persist downloads externally for archival.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        after_sequence (str | Unset):  Default: '0'.
        group_id (UUID | Unset):
        through_sequence (str | Unset):
        snapshot_at (datetime.datetime | Unset):
        failure_code (list[str] | Unset):
        limit (int | Unset):  Default: 32.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | File
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        client=client,
        after_sequence=after_sequence,
        group_id=group_id,
        through_sequence=through_sequence,
        snapshot_at=snapshot_at,
        failure_code=failure_code,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    after_sequence: str | Unset = "0",
    group_id: UUID | Unset = UNSET,
    through_sequence: str | Unset = UNSET,
    snapshot_at: datetime.datetime | Unset = UNSET,
    failure_code: list[str] | Unset = UNSET,
    limit: int | Unset = 32,
) -> Response[Any | File]:
    """Download a read-only dead-letter JSONL page

     Account ownership, read scope, completed MFA and realtime history preview required. Returns one
    manifest line followed by zero to 128 dead_letter lines, ordered by numeric sequence. Payload is the
    retained snapshot with base64 data and metadata. No claim or credential tokens. Every page uses a
    repeatable-read read-only transaction; export never claims, replays, cleans up or extends retention.
    First page captures group_id, through_sequence and snapshot_at. Resume with those three values and
    next_after_sequence; keep failure_code filters unchanged. Later dead-letter transitions are excluded
    by the snapshot cutoff. Replay, deletion and retention cleanup can remove records between pages;
    this is a live export, not an immutable server archive. A recreated group or rewound assignment
    boundary returns 409 realtime_dead_letter_export_changed. Verify the manifest count and complete
    transfer before advancing the cursor. Link headers provide the next page. Empty exports still
    contain a manifest. At most 2 MiB per page. Persist downloads externally for archival.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        after_sequence (str | Unset):  Default: '0'.
        group_id (UUID | Unset):
        through_sequence (str | Unset):
        snapshot_at (datetime.datetime | Unset):
        failure_code (list[str] | Unset):
        limit (int | Unset):  Default: 32.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | File]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        after_sequence=after_sequence,
        group_id=group_id,
        through_sequence=through_sequence,
        snapshot_at=snapshot_at,
        failure_code=failure_code,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    channel: str,
    group: str,
    *,
    client: AuthenticatedClient,
    after_sequence: str | Unset = "0",
    group_id: UUID | Unset = UNSET,
    through_sequence: str | Unset = UNSET,
    snapshot_at: datetime.datetime | Unset = UNSET,
    failure_code: list[str] | Unset = UNSET,
    limit: int | Unset = 32,
) -> Any | File | None:
    """Download a read-only dead-letter JSONL page

     Account ownership, read scope, completed MFA and realtime history preview required. Returns one
    manifest line followed by zero to 128 dead_letter lines, ordered by numeric sequence. Payload is the
    retained snapshot with base64 data and metadata. No claim or credential tokens. Every page uses a
    repeatable-read read-only transaction; export never claims, replays, cleans up or extends retention.
    First page captures group_id, through_sequence and snapshot_at. Resume with those three values and
    next_after_sequence; keep failure_code filters unchanged. Later dead-letter transitions are excluded
    by the snapshot cutoff. Replay, deletion and retention cleanup can remove records between pages;
    this is a live export, not an immutable server archive. A recreated group or rewound assignment
    boundary returns 409 realtime_dead_letter_export_changed. Verify the manifest count and complete
    transfer before advancing the cursor. Link headers provide the next page. Empty exports still
    contain a manifest. At most 2 MiB per page. Persist downloads externally for archival.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        after_sequence (str | Unset):  Default: '0'.
        group_id (UUID | Unset):
        through_sequence (str | Unset):
        snapshot_at (datetime.datetime | Unset):
        failure_code (list[str] | Unset):
        limit (int | Unset):  Default: 32.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | File
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            group=group,
            client=client,
            after_sequence=after_sequence,
            group_id=group_id,
            through_sequence=through_sequence,
            snapshot_at=snapshot_at,
            failure_code=failure_code,
            limit=limit,
        )
    ).parsed
