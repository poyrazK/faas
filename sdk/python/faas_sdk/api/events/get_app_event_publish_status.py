from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_event_publish_status_response import AppEventPublishStatusResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    key: str,
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
    expected_accepted_at: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["key"] = key

    params["after"] = after

    params["limit"] = limit

    params["expected_accepted_at"] = expected_accepted_at

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/events/publish-status".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppEventPublishStatusResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppEventPublishStatusResponse.from_dict(response.json())

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

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppEventPublishStatusResponse | Problem]:
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
    key: str,
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
    expected_accepted_at: str | Unset = UNSET,
) -> Response[AppEventPublishStatusResponse | Problem]:
    """Reconcile an app producer key without publishing another event.

     Requires an owned app, apps:read/admin scopes, MFA and existing rate limits.
    Derives exactly the same source and event ID as app key publication.
    Read-only five-second receipt snapshot; no append, claim, replay or retention refresh.
    Returns processing while retained routing is unsettled, accepted after routing
    settles, or unavailable when no retained receipt is visible. Both processing
    and accepted prove durable acceptance. Accepted does not prove delivery or
    handler success; consult independent recipient execution and workflow evidence.
    Unavailable cannot distinguish never accepted, concurrent acceptance not yet
    visible, or pruning. It must not automatically trigger a replacement publish.
    Evidence includes full routing summaries and a bounded recipient page.
    Follow evidence.next_after using after; pages are live snapshots and cursors
    bind account, source, event ID and acceptance identity. Stale cursors fail 400.
    With expected_accepted_at, acceptance is same_acceptance, replacement_acceptance
    or unavailable separately from routing status. Evidence describes the currently
    observed acceptance, which can be a replacement. Never treat its outcomes as
    proof for the expected acceptance. Raw keys and event payloads are not returned. App renames
    preserve
    identity; replacement apps have a different UUID namespace. Matching legacy
    account/source/ID publication addresses the same identity.

    Args:
        slug (str):
        key (str):
        after (str | Unset):
        limit (int | Unset):  Default: 100.
        expected_accepted_at (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppEventPublishStatusResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        key=key,
        after=after,
        limit=limit,
        expected_accepted_at=expected_accepted_at,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    key: str,
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
    expected_accepted_at: str | Unset = UNSET,
) -> AppEventPublishStatusResponse | Problem | None:
    """Reconcile an app producer key without publishing another event.

     Requires an owned app, apps:read/admin scopes, MFA and existing rate limits.
    Derives exactly the same source and event ID as app key publication.
    Read-only five-second receipt snapshot; no append, claim, replay or retention refresh.
    Returns processing while retained routing is unsettled, accepted after routing
    settles, or unavailable when no retained receipt is visible. Both processing
    and accepted prove durable acceptance. Accepted does not prove delivery or
    handler success; consult independent recipient execution and workflow evidence.
    Unavailable cannot distinguish never accepted, concurrent acceptance not yet
    visible, or pruning. It must not automatically trigger a replacement publish.
    Evidence includes full routing summaries and a bounded recipient page.
    Follow evidence.next_after using after; pages are live snapshots and cursors
    bind account, source, event ID and acceptance identity. Stale cursors fail 400.
    With expected_accepted_at, acceptance is same_acceptance, replacement_acceptance
    or unavailable separately from routing status. Evidence describes the currently
    observed acceptance, which can be a replacement. Never treat its outcomes as
    proof for the expected acceptance. Raw keys and event payloads are not returned. App renames
    preserve
    identity; replacement apps have a different UUID namespace. Matching legacy
    account/source/ID publication addresses the same identity.

    Args:
        slug (str):
        key (str):
        after (str | Unset):
        limit (int | Unset):  Default: 100.
        expected_accepted_at (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppEventPublishStatusResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        key=key,
        after=after,
        limit=limit,
        expected_accepted_at=expected_accepted_at,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    key: str,
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
    expected_accepted_at: str | Unset = UNSET,
) -> Response[AppEventPublishStatusResponse | Problem]:
    """Reconcile an app producer key without publishing another event.

     Requires an owned app, apps:read/admin scopes, MFA and existing rate limits.
    Derives exactly the same source and event ID as app key publication.
    Read-only five-second receipt snapshot; no append, claim, replay or retention refresh.
    Returns processing while retained routing is unsettled, accepted after routing
    settles, or unavailable when no retained receipt is visible. Both processing
    and accepted prove durable acceptance. Accepted does not prove delivery or
    handler success; consult independent recipient execution and workflow evidence.
    Unavailable cannot distinguish never accepted, concurrent acceptance not yet
    visible, or pruning. It must not automatically trigger a replacement publish.
    Evidence includes full routing summaries and a bounded recipient page.
    Follow evidence.next_after using after; pages are live snapshots and cursors
    bind account, source, event ID and acceptance identity. Stale cursors fail 400.
    With expected_accepted_at, acceptance is same_acceptance, replacement_acceptance
    or unavailable separately from routing status. Evidence describes the currently
    observed acceptance, which can be a replacement. Never treat its outcomes as
    proof for the expected acceptance. Raw keys and event payloads are not returned. App renames
    preserve
    identity; replacement apps have a different UUID namespace. Matching legacy
    account/source/ID publication addresses the same identity.

    Args:
        slug (str):
        key (str):
        after (str | Unset):
        limit (int | Unset):  Default: 100.
        expected_accepted_at (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppEventPublishStatusResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        key=key,
        after=after,
        limit=limit,
        expected_accepted_at=expected_accepted_at,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    key: str,
    after: str | Unset = UNSET,
    limit: int | Unset = 100,
    expected_accepted_at: str | Unset = UNSET,
) -> AppEventPublishStatusResponse | Problem | None:
    """Reconcile an app producer key without publishing another event.

     Requires an owned app, apps:read/admin scopes, MFA and existing rate limits.
    Derives exactly the same source and event ID as app key publication.
    Read-only five-second receipt snapshot; no append, claim, replay or retention refresh.
    Returns processing while retained routing is unsettled, accepted after routing
    settles, or unavailable when no retained receipt is visible. Both processing
    and accepted prove durable acceptance. Accepted does not prove delivery or
    handler success; consult independent recipient execution and workflow evidence.
    Unavailable cannot distinguish never accepted, concurrent acceptance not yet
    visible, or pruning. It must not automatically trigger a replacement publish.
    Evidence includes full routing summaries and a bounded recipient page.
    Follow evidence.next_after using after; pages are live snapshots and cursors
    bind account, source, event ID and acceptance identity. Stale cursors fail 400.
    With expected_accepted_at, acceptance is same_acceptance, replacement_acceptance
    or unavailable separately from routing status. Evidence describes the currently
    observed acceptance, which can be a replacement. Never treat its outcomes as
    proof for the expected acceptance. Raw keys and event payloads are not returned. App renames
    preserve
    identity; replacement apps have a different UUID namespace. Matching legacy
    account/source/ID publication addresses the same identity.

    Args:
        slug (str):
        key (str):
        after (str | Unset):
        limit (int | Unset):  Default: 100.
        expected_accepted_at (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppEventPublishStatusResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            key=key,
            after=after,
            limit=limit,
            expected_accepted_at=expected_accepted_at,
        )
    ).parsed
