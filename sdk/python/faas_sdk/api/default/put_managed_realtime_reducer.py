from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_reducer_response import ManagedRealtimeReducerResponse
from ...models.put_managed_realtime_reducer_body import PutManagedRealtimeReducerBody
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    channel: str,
    *,
    body: PutManagedRealtimeReducerBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/reducer".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ManagedRealtimeReducerResponse | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeReducerResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ManagedRealtimeReducerResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutManagedRealtimeReducerBody,
) -> Response[Any | ManagedRealtimeReducerResponse]:
    """Enable a channel reducer from a state baseline matching the current sequence

     Retained JSON events support set, merge, delete, increment, append, and remove operations.
    Increment payloads use op=increment, key, field, and a required integer delta;
    optional min/max bounds reject results outside the allowed range.
    Values, deltas, bounds, and results must be safe integers in
    [-9007199254740991, 9007199254740991]. Missing entities or fields start at zero.
    Increment supports expected_version and preserves expires_at unless replaced
    or cleared with null. Array operations use key, field, and items (1..32 JSON values).
    Append accepts unique=true for structural JSON deduplication; remove deletes
    all matching items. Optional max_length (0..256) rejects longer results.
    Missing fields start as empty arrays; existing targets must be arrays of at
    most 256 items. Array operations support expected_version and the same
    expires_at behavior as increment. Every operation accepts optional conditions
    (1..16 predicates), each with a literal field and exactly one of equals (any
    JSON value) or absent:true. All predicates must hold before the update.
    Equality is structural and compares numbers exactly. Failed conditions return
    409 realtime_condition_conflict with entity_key, condition_field,
    condition_index, message_index, current_version, entity_exists, and field_exists.
    Invalid operations roll back the whole batch.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        body (PutManagedRealtimeReducerBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ManagedRealtimeReducerResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutManagedRealtimeReducerBody,
) -> Any | ManagedRealtimeReducerResponse | None:
    """Enable a channel reducer from a state baseline matching the current sequence

     Retained JSON events support set, merge, delete, increment, append, and remove operations.
    Increment payloads use op=increment, key, field, and a required integer delta;
    optional min/max bounds reject results outside the allowed range.
    Values, deltas, bounds, and results must be safe integers in
    [-9007199254740991, 9007199254740991]. Missing entities or fields start at zero.
    Increment supports expected_version and preserves expires_at unless replaced
    or cleared with null. Array operations use key, field, and items (1..32 JSON values).
    Append accepts unique=true for structural JSON deduplication; remove deletes
    all matching items. Optional max_length (0..256) rejects longer results.
    Missing fields start as empty arrays; existing targets must be arrays of at
    most 256 items. Array operations support expected_version and the same
    expires_at behavior as increment. Every operation accepts optional conditions
    (1..16 predicates), each with a literal field and exactly one of equals (any
    JSON value) or absent:true. All predicates must hold before the update.
    Equality is structural and compares numbers exactly. Failed conditions return
    409 realtime_condition_conflict with entity_key, condition_field,
    condition_index, message_index, current_version, entity_exists, and field_exists.
    Invalid operations roll back the whole batch.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        body (PutManagedRealtimeReducerBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ManagedRealtimeReducerResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutManagedRealtimeReducerBody,
) -> Response[Any | ManagedRealtimeReducerResponse]:
    """Enable a channel reducer from a state baseline matching the current sequence

     Retained JSON events support set, merge, delete, increment, append, and remove operations.
    Increment payloads use op=increment, key, field, and a required integer delta;
    optional min/max bounds reject results outside the allowed range.
    Values, deltas, bounds, and results must be safe integers in
    [-9007199254740991, 9007199254740991]. Missing entities or fields start at zero.
    Increment supports expected_version and preserves expires_at unless replaced
    or cleared with null. Array operations use key, field, and items (1..32 JSON values).
    Append accepts unique=true for structural JSON deduplication; remove deletes
    all matching items. Optional max_length (0..256) rejects longer results.
    Missing fields start as empty arrays; existing targets must be arrays of at
    most 256 items. Array operations support expected_version and the same
    expires_at behavior as increment. Every operation accepts optional conditions
    (1..16 predicates), each with a literal field and exactly one of equals (any
    JSON value) or absent:true. All predicates must hold before the update.
    Equality is structural and compares numbers exactly. Failed conditions return
    409 realtime_condition_conflict with entity_key, condition_field,
    condition_index, message_index, current_version, entity_exists, and field_exists.
    Invalid operations roll back the whole batch.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        body (PutManagedRealtimeReducerBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ManagedRealtimeReducerResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    channel: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutManagedRealtimeReducerBody,
) -> Any | ManagedRealtimeReducerResponse | None:
    """Enable a channel reducer from a state baseline matching the current sequence

     Retained JSON events support set, merge, delete, increment, append, and remove operations.
    Increment payloads use op=increment, key, field, and a required integer delta;
    optional min/max bounds reject results outside the allowed range.
    Values, deltas, bounds, and results must be safe integers in
    [-9007199254740991, 9007199254740991]. Missing entities or fields start at zero.
    Increment supports expected_version and preserves expires_at unless replaced
    or cleared with null. Array operations use key, field, and items (1..32 JSON values).
    Append accepts unique=true for structural JSON deduplication; remove deletes
    all matching items. Optional max_length (0..256) rejects longer results.
    Missing fields start as empty arrays; existing targets must be arrays of at
    most 256 items. Array operations support expected_version and the same
    expires_at behavior as increment. Every operation accepts optional conditions
    (1..16 predicates), each with a literal field and exactly one of equals (any
    JSON value) or absent:true. All predicates must hold before the update.
    Equality is structural and compares numbers exactly. Failed conditions return
    409 realtime_condition_conflict with entity_key, condition_field,
    condition_index, message_index, current_version, entity_exists, and field_exists.
    Invalid operations roll back the whole batch.

    Args:
        slug (str):
        id (UUID):
        channel (str):
        body (PutManagedRealtimeReducerBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ManagedRealtimeReducerResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            client=client,
            body=body,
        )
    ).parsed
