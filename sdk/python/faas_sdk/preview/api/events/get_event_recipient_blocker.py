from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_recipient_blocker_response import EventRecipientBlockerResponse
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    *,
    source: str,
    id: str,
    subscription_id: str,
    expected_accepted_at: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["source"] = source

    params["id"] = id

    params["subscription_id"] = subscription_id

    params["expected_accepted_at"] = expected_accepted_at

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/events/recipient-blocker",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventRecipientBlockerResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventRecipientBlockerResponse.from_dict(response.json())

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
) -> Response[EventRecipientBlockerResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    source: str,
    id: str,
    subscription_id: str,
    expected_accepted_at: str,
) -> Response[EventRecipientBlockerResponse | Problem]:
    """Inspect routing blockers for one exact retained recipient.

     Requires apps:read or admin. Reads only the authenticated account, in
    one repeatable read snapshot. Matches source, ID, precise acceptance and
    recipient before inspecting current routing controls. No backlog pagination
    or consumer aggregation. Returns available for current routing evidence,
    not_waiting only when the retained recipient has left pending/processing,
    unavailable for missing or inconsistent retained evidence, or
    acceptance_replaced when the retained identity has a different timestamp.
    Not_waiting never implies handler completion or replay attribution.
    Foreign and missing receipts both return unavailable with receipt_unavailable.
    Read-only; never follows links, claims work or submits recovery.

    Args:
        source (str):
        id (str):
        subscription_id (str):
        expected_accepted_at (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecipientBlockerResponse | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        id=id,
        subscription_id=subscription_id,
        expected_accepted_at=expected_accepted_at,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    source: str,
    id: str,
    subscription_id: str,
    expected_accepted_at: str,
) -> EventRecipientBlockerResponse | Problem | None:
    """Inspect routing blockers for one exact retained recipient.

     Requires apps:read or admin. Reads only the authenticated account, in
    one repeatable read snapshot. Matches source, ID, precise acceptance and
    recipient before inspecting current routing controls. No backlog pagination
    or consumer aggregation. Returns available for current routing evidence,
    not_waiting only when the retained recipient has left pending/processing,
    unavailable for missing or inconsistent retained evidence, or
    acceptance_replaced when the retained identity has a different timestamp.
    Not_waiting never implies handler completion or replay attribution.
    Foreign and missing receipts both return unavailable with receipt_unavailable.
    Read-only; never follows links, claims work or submits recovery.

    Args:
        source (str):
        id (str):
        subscription_id (str):
        expected_accepted_at (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecipientBlockerResponse | Problem
    """

    return sync_detailed(
        client=client,
        source=source,
        id=id,
        subscription_id=subscription_id,
        expected_accepted_at=expected_accepted_at,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    source: str,
    id: str,
    subscription_id: str,
    expected_accepted_at: str,
) -> Response[EventRecipientBlockerResponse | Problem]:
    """Inspect routing blockers for one exact retained recipient.

     Requires apps:read or admin. Reads only the authenticated account, in
    one repeatable read snapshot. Matches source, ID, precise acceptance and
    recipient before inspecting current routing controls. No backlog pagination
    or consumer aggregation. Returns available for current routing evidence,
    not_waiting only when the retained recipient has left pending/processing,
    unavailable for missing or inconsistent retained evidence, or
    acceptance_replaced when the retained identity has a different timestamp.
    Not_waiting never implies handler completion or replay attribution.
    Foreign and missing receipts both return unavailable with receipt_unavailable.
    Read-only; never follows links, claims work or submits recovery.

    Args:
        source (str):
        id (str):
        subscription_id (str):
        expected_accepted_at (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecipientBlockerResponse | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        id=id,
        subscription_id=subscription_id,
        expected_accepted_at=expected_accepted_at,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    source: str,
    id: str,
    subscription_id: str,
    expected_accepted_at: str,
) -> EventRecipientBlockerResponse | Problem | None:
    """Inspect routing blockers for one exact retained recipient.

     Requires apps:read or admin. Reads only the authenticated account, in
    one repeatable read snapshot. Matches source, ID, precise acceptance and
    recipient before inspecting current routing controls. No backlog pagination
    or consumer aggregation. Returns available for current routing evidence,
    not_waiting only when the retained recipient has left pending/processing,
    unavailable for missing or inconsistent retained evidence, or
    acceptance_replaced when the retained identity has a different timestamp.
    Not_waiting never implies handler completion or replay attribution.
    Foreign and missing receipts both return unavailable with receipt_unavailable.
    Read-only; never follows links, claims work or submits recovery.

    Args:
        source (str):
        id (str):
        subscription_id (str):
        expected_accepted_at (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecipientBlockerResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            source=source,
            id=id,
            subscription_id=subscription_id,
            expected_accepted_at=expected_accepted_at,
        )
    ).parsed
