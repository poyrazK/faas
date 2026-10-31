from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_trigger_dead_letter_response import ListTriggerDeadLetterResponse
from ...models.problem import Problem
from ...models.trigger_dead_letter_reason import TriggerDeadLetterReason
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    reason: TriggerDeadLetterReason | Unset = UNSET,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_reason: str | Unset = UNSET
    if not isinstance(reason, Unset):
        json_reason = reason

    params["reason"] = json_reason

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/triggers/{id}/dlq".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListTriggerDeadLetterResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListTriggerDeadLetterResponse.from_dict(response.json())

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
) -> Response[ListTriggerDeadLetterResponse | Problem]:
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
    reason: TriggerDeadLetterReason | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[ListTriggerDeadLetterResponse | Problem]:
    """List dead-letter rows for one trigger.

    Args:
        id (str):
        reason (TriggerDeadLetterReason | Unset): Reason enum pinned by SQL CHECK on
            trigger_dead_letter.reason.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListTriggerDeadLetterResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        reason=reason,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    reason: TriggerDeadLetterReason | Unset = UNSET,
    limit: int | Unset = 50,
) -> ListTriggerDeadLetterResponse | Problem | None:
    """List dead-letter rows for one trigger.

    Args:
        id (str):
        reason (TriggerDeadLetterReason | Unset): Reason enum pinned by SQL CHECK on
            trigger_dead_letter.reason.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListTriggerDeadLetterResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        reason=reason,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    reason: TriggerDeadLetterReason | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[ListTriggerDeadLetterResponse | Problem]:
    """List dead-letter rows for one trigger.

    Args:
        id (str):
        reason (TriggerDeadLetterReason | Unset): Reason enum pinned by SQL CHECK on
            trigger_dead_letter.reason.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListTriggerDeadLetterResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        reason=reason,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    reason: TriggerDeadLetterReason | Unset = UNSET,
    limit: int | Unset = 50,
) -> ListTriggerDeadLetterResponse | Problem | None:
    """List dead-letter rows for one trigger.

    Args:
        id (str):
        reason (TriggerDeadLetterReason | Unset): Reason enum pinned by SQL CHECK on
            trigger_dead_letter.reason.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListTriggerDeadLetterResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            reason=reason,
            limit=limit,
        )
    ).parsed
