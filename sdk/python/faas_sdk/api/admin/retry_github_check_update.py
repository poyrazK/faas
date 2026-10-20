from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.github_recovery_retry_response import GithubRecoveryRetryResponse
from ...models.problem import Problem
from ...models.retry_github_check_update_confirm import (
    RetryGithubCheckUpdateConfirm,
)
from ...types import UNSET, Response


def _get_kwargs(
    id: UUID,
    *,
    confirm: RetryGithubCheckUpdateConfirm,
    reason: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_confirm: str = confirm
    params["confirm"] = json_confirm

    params["reason"] = reason

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/ops/github/check-updates/{id}/retry".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GithubRecoveryRetryResponse | Problem | None:
    if response.status_code == 200:
        response_200 = GithubRecoveryRetryResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[GithubRecoveryRetryResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    confirm: RetryGithubCheckUpdateConfirm,
    reason: str,
) -> Response[GithubRecoveryRetryResponse | Problem]:
    """Move one dead GitHub Check Run update back to pending.

     Strict operator-session mutation requiring a recent MFA step-up,
    Idempotency-Key, explicit confirmation, and a durable reason. The
    compare-and-swap write is owned by githubd and emits a trace-linked
    operator.action.github_check_update_retry audit event.

    Args:
        id (UUID):
        confirm (RetryGithubCheckUpdateConfirm):
        reason (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GithubRecoveryRetryResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        confirm=confirm,
        reason=reason,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    confirm: RetryGithubCheckUpdateConfirm,
    reason: str,
) -> GithubRecoveryRetryResponse | Problem | None:
    """Move one dead GitHub Check Run update back to pending.

     Strict operator-session mutation requiring a recent MFA step-up,
    Idempotency-Key, explicit confirmation, and a durable reason. The
    compare-and-swap write is owned by githubd and emits a trace-linked
    operator.action.github_check_update_retry audit event.

    Args:
        id (UUID):
        confirm (RetryGithubCheckUpdateConfirm):
        reason (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GithubRecoveryRetryResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        confirm=confirm,
        reason=reason,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    confirm: RetryGithubCheckUpdateConfirm,
    reason: str,
) -> Response[GithubRecoveryRetryResponse | Problem]:
    """Move one dead GitHub Check Run update back to pending.

     Strict operator-session mutation requiring a recent MFA step-up,
    Idempotency-Key, explicit confirmation, and a durable reason. The
    compare-and-swap write is owned by githubd and emits a trace-linked
    operator.action.github_check_update_retry audit event.

    Args:
        id (UUID):
        confirm (RetryGithubCheckUpdateConfirm):
        reason (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GithubRecoveryRetryResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        confirm=confirm,
        reason=reason,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    confirm: RetryGithubCheckUpdateConfirm,
    reason: str,
) -> GithubRecoveryRetryResponse | Problem | None:
    """Move one dead GitHub Check Run update back to pending.

     Strict operator-session mutation requiring a recent MFA step-up,
    Idempotency-Key, explicit confirmation, and a durable reason. The
    compare-and-swap write is owned by githubd and emits a trace-linked
    operator.action.github_check_update_retry audit event.

    Args:
        id (UUID):
        confirm (RetryGithubCheckUpdateConfirm):
        reason (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GithubRecoveryRetryResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            confirm=confirm,
            reason=reason,
        )
    ).parsed
