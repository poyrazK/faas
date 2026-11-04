from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.async_invoke_response import AsyncInvokeResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/invocations/{id}/replay-keyed".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AsyncInvokeResponse | Problem | None:
    if response.status_code == 202:
        response_202 = AsyncInvokeResponse.from_dict(response.json())

        return response_202

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
) -> Response[AsyncInvokeResponse | Problem]:
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
) -> Response[AsyncInvokeResponse | Problem]:
    """Recover failed keyed work in its captured policy lane

     Accepts no request body. Requires deploy write scope and configured MFA.
    Only failed keyed work without a queue binding is eligible. The child
    retains the captured policy revision, key, fairness limits, deployment
    scope, customer identity, payload, retry policy and trusted replay root.
    It receives the next sequence in the same lane, after previously admitted
    work. Replay does not supersede pending rows or restart debounce.

    The original pending expiry and start deadline remain effective. A new
    replay after either elapsed deadline returns `keyed_replay_expired`.
    Each parent creates at most one child. Repeating a request returns that
    child, including after completion; a subsequent recovery must target
    the failed child. If the child has been pruned while its parent remains,
    the parent returns `keyed_replay_unavailable` instead of executing again.
    The original failure remains visible in event receipts and retained
    replay history. Delivery and application side effects remain at least once.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AsyncInvokeResponse | Problem]
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
) -> AsyncInvokeResponse | Problem | None:
    """Recover failed keyed work in its captured policy lane

     Accepts no request body. Requires deploy write scope and configured MFA.
    Only failed keyed work without a queue binding is eligible. The child
    retains the captured policy revision, key, fairness limits, deployment
    scope, customer identity, payload, retry policy and trusted replay root.
    It receives the next sequence in the same lane, after previously admitted
    work. Replay does not supersede pending rows or restart debounce.

    The original pending expiry and start deadline remain effective. A new
    replay after either elapsed deadline returns `keyed_replay_expired`.
    Each parent creates at most one child. Repeating a request returns that
    child, including after completion; a subsequent recovery must target
    the failed child. If the child has been pruned while its parent remains,
    the parent returns `keyed_replay_unavailable` instead of executing again.
    The original failure remains visible in event receipts and retained
    replay history. Delivery and application side effects remain at least once.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AsyncInvokeResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AsyncInvokeResponse | Problem]:
    """Recover failed keyed work in its captured policy lane

     Accepts no request body. Requires deploy write scope and configured MFA.
    Only failed keyed work without a queue binding is eligible. The child
    retains the captured policy revision, key, fairness limits, deployment
    scope, customer identity, payload, retry policy and trusted replay root.
    It receives the next sequence in the same lane, after previously admitted
    work. Replay does not supersede pending rows or restart debounce.

    The original pending expiry and start deadline remain effective. A new
    replay after either elapsed deadline returns `keyed_replay_expired`.
    Each parent creates at most one child. Repeating a request returns that
    child, including after completion; a subsequent recovery must target
    the failed child. If the child has been pruned while its parent remains,
    the parent returns `keyed_replay_unavailable` instead of executing again.
    The original failure remains visible in event receipts and retained
    replay history. Delivery and application side effects remain at least once.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AsyncInvokeResponse | Problem]
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
) -> AsyncInvokeResponse | Problem | None:
    """Recover failed keyed work in its captured policy lane

     Accepts no request body. Requires deploy write scope and configured MFA.
    Only failed keyed work without a queue binding is eligible. The child
    retains the captured policy revision, key, fairness limits, deployment
    scope, customer identity, payload, retry policy and trusted replay root.
    It receives the next sequence in the same lane, after previously admitted
    work. Replay does not supersede pending rows or restart debounce.

    The original pending expiry and start deadline remain effective. A new
    replay after either elapsed deadline returns `keyed_replay_expired`.
    Each parent creates at most one child. Repeating a request returns that
    child, including after completion; a subsequent recovery must target
    the failed child. If the child has been pruned while its parent remains,
    the parent returns `keyed_replay_unavailable` instead of executing again.
    The original failure remains visible in event receipts and retained
    replay history. Delivery and application side effects remain at least once.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AsyncInvokeResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
