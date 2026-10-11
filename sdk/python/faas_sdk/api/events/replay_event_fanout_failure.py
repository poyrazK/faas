from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.replay_event_fanout_failure_request import ReplayEventFanoutFailureRequest
from ...models.replay_event_fanout_failure_response import ReplayEventFanoutFailureResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: ReplayEventFanoutFailureRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/event-deliveries:replay-fanout-failure".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ReplayEventFanoutFailureResponse | None:
    if response.status_code == 202:
        response_202 = ReplayEventFanoutFailureResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ReplayEventFanoutFailureResponse]:
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
    body: ReplayEventFanoutFailureRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | ReplayEventFanoutFailureResponse]:
    """Retry one terminal event recipient routing failure.

     Requeues only the named failed recipient captured at acceptance or
    added by a retained historical backfill. Backfill failures must be
    retryable and have a running or completed-with-failures job; reopening
    a completed job observes active-job limits. Other recipients and
    their outcomes are left untouched. With independent recipient routing,
    a terminal recipient can be replayed while siblings are active and gets
    a fresh routing retry budget. Legacy receipts must settle first.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (ReplayEventFanoutFailureRequest): Identity of one terminal fanout failure to replay.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReplayEventFanoutFailureResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ReplayEventFanoutFailureRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | ReplayEventFanoutFailureResponse | None:
    """Retry one terminal event recipient routing failure.

     Requeues only the named failed recipient captured at acceptance or
    added by a retained historical backfill. Backfill failures must be
    retryable and have a running or completed-with-failures job; reopening
    a completed job observes active-job limits. Other recipients and
    their outcomes are left untouched. With independent recipient routing,
    a terminal recipient can be replayed while siblings are active and gets
    a fresh routing retry budget. Legacy receipts must settle first.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (ReplayEventFanoutFailureRequest): Identity of one terminal fanout failure to replay.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReplayEventFanoutFailureResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ReplayEventFanoutFailureRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | ReplayEventFanoutFailureResponse]:
    """Retry one terminal event recipient routing failure.

     Requeues only the named failed recipient captured at acceptance or
    added by a retained historical backfill. Backfill failures must be
    retryable and have a running or completed-with-failures job; reopening
    a completed job observes active-job limits. Other recipients and
    their outcomes are left untouched. With independent recipient routing,
    a terminal recipient can be replayed while siblings are active and gets
    a fresh routing retry budget. Legacy receipts must settle first.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (ReplayEventFanoutFailureRequest): Identity of one terminal fanout failure to replay.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReplayEventFanoutFailureResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ReplayEventFanoutFailureRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | ReplayEventFanoutFailureResponse | None:
    """Retry one terminal event recipient routing failure.

     Requeues only the named failed recipient captured at acceptance or
    added by a retained historical backfill. Backfill failures must be
    retryable and have a running or completed-with-failures job; reopening
    a completed job observes active-job limits. Other recipients and
    their outcomes are left untouched. With independent recipient routing,
    a terminal recipient can be replayed while siblings are active and gets
    a fresh routing retry budget. Legacy receipts must settle first.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (ReplayEventFanoutFailureRequest): Identity of one terminal fanout failure to replay.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReplayEventFanoutFailureResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
