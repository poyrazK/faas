from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.hosted_task_source_decision import HostedTaskSourceDecision
from ...models.hosted_task_source_intent import HostedTaskSourceIntent
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: HostedTaskSourceDecision,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/task-tool-source-intents/{id}/decision".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | HostedTaskSourceIntent | None:
    if response.status_code == 200:
        response_200 = HostedTaskSourceIntent.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | HostedTaskSourceIntent]:
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
    body: HostedTaskSourceDecision,
) -> Response[Any | HostedTaskSourceIntent]:
    """Record an owner decision on hosted Task source

     An authenticated personal owner or fresh unscoped non-agent personal administrator approves, rejects
    or revokes the exact retained intent and review digests. Agent and MCP-login credentials cannot
    decide. Approval requires enabled source retention and rechecks retained source availability,
    original requester authority and native app baselines atomically. Rejection and revocation do not
    require live requester or app facts; stale and expired reviews stay terminal. Exact retries preserve
    the first decision timestamp and fixed deadline. Approval is only a source review; readiness,
    credential verification and execution authorization remain absent. Native journal idempotency
    rechecks authority on every call instead of replaying an HTTP response cache.

    Args:
        id (str):
        body (HostedTaskSourceDecision): Personal-owner source review decision bound to both
            immutable hosted source digests.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | HostedTaskSourceIntent]
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
    body: HostedTaskSourceDecision,
) -> Any | HostedTaskSourceIntent | None:
    """Record an owner decision on hosted Task source

     An authenticated personal owner or fresh unscoped non-agent personal administrator approves, rejects
    or revokes the exact retained intent and review digests. Agent and MCP-login credentials cannot
    decide. Approval requires enabled source retention and rechecks retained source availability,
    original requester authority and native app baselines atomically. Rejection and revocation do not
    require live requester or app facts; stale and expired reviews stay terminal. Exact retries preserve
    the first decision timestamp and fixed deadline. Approval is only a source review; readiness,
    credential verification and execution authorization remain absent. Native journal idempotency
    rechecks authority on every call instead of replaying an HTTP response cache.

    Args:
        id (str):
        body (HostedTaskSourceDecision): Personal-owner source review decision bound to both
            immutable hosted source digests.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | HostedTaskSourceIntent
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
    body: HostedTaskSourceDecision,
) -> Response[Any | HostedTaskSourceIntent]:
    """Record an owner decision on hosted Task source

     An authenticated personal owner or fresh unscoped non-agent personal administrator approves, rejects
    or revokes the exact retained intent and review digests. Agent and MCP-login credentials cannot
    decide. Approval requires enabled source retention and rechecks retained source availability,
    original requester authority and native app baselines atomically. Rejection and revocation do not
    require live requester or app facts; stale and expired reviews stay terminal. Exact retries preserve
    the first decision timestamp and fixed deadline. Approval is only a source review; readiness,
    credential verification and execution authorization remain absent. Native journal idempotency
    rechecks authority on every call instead of replaying an HTTP response cache.

    Args:
        id (str):
        body (HostedTaskSourceDecision): Personal-owner source review decision bound to both
            immutable hosted source digests.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | HostedTaskSourceIntent]
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
    body: HostedTaskSourceDecision,
) -> Any | HostedTaskSourceIntent | None:
    """Record an owner decision on hosted Task source

     An authenticated personal owner or fresh unscoped non-agent personal administrator approves, rejects
    or revokes the exact retained intent and review digests. Agent and MCP-login credentials cannot
    decide. Approval requires enabled source retention and rechecks retained source availability,
    original requester authority and native app baselines atomically. Rejection and revocation do not
    require live requester or app facts; stale and expired reviews stay terminal. Exact retries preserve
    the first decision timestamp and fixed deadline. Approval is only a source review; readiness,
    credential verification and execution authorization remain absent. Native journal idempotency
    rechecks authority on every call instead of replaying an HTTP response cache.

    Args:
        id (str):
        body (HostedTaskSourceDecision): Personal-owner source review decision bound to both
            immutable hosted source digests.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | HostedTaskSourceIntent
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
