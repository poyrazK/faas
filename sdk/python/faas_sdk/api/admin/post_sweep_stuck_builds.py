from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.post_sweep_stuck_builds_confirm import PostSweepStuckBuildsConfirm
from ...models.problem import Problem
from ...models.sweep_stuck_builds_response import SweepStuckBuildsResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    confirm: PostSweepStuckBuildsConfirm,
    older_than: str | Unset = UNSET,
    reason: str | Unset = "operator_reclaim_build",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_confirm: str = confirm
    params["confirm"] = json_confirm

    params["older_than"] = older_than

    params["reason"] = reason

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/builds/sweep-stuck",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SweepStuckBuildsResponse | None:
    if response.status_code == 200:
        response_200 = SweepStuckBuildsResponse.from_dict(response.json())

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

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | SweepStuckBuildsResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    confirm: PostSweepStuckBuildsConfirm,
    older_than: str | Unset = UNSET,
    reason: str | Unset = "operator_reclaim_build",
) -> Response[Problem | SweepStuckBuildsResponse]:
    r"""Flip every build row stuck in 'running' past the threshold to 'failed/timeout' (admin-only).

     Operator-side recovery primitive for builder microVMs that
    crashed (OOM, kernel panic, host reboot) and left their
    `builds` row in 'running' indefinitely. Mirrors the
    in-process reaper at pkg/builderd/reaper.go:48 — the
    operator-facing endpoint is the manual escape hatch for
    when the reaper's grace period is too long for an
    incident.

    `?older_than=` is clamped to [1m, 60m] so a fat-fingered
    \"1ns\" cannot sweep in-flight builds. Default 15m.

    Audit row: operator.action.reclaim_build with
    account_id=NULL (fleet-level, not tenant-scoped), including
    the normalized operator reason.

    Args:
        confirm (PostSweepStuckBuildsConfirm):
        older_than (str | Unset):  Example: 15m.
        reason (str | Unset):  Default: 'operator_reclaim_build'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SweepStuckBuildsResponse]
    """

    kwargs = _get_kwargs(
        confirm=confirm,
        older_than=older_than,
        reason=reason,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    confirm: PostSweepStuckBuildsConfirm,
    older_than: str | Unset = UNSET,
    reason: str | Unset = "operator_reclaim_build",
) -> Problem | SweepStuckBuildsResponse | None:
    r"""Flip every build row stuck in 'running' past the threshold to 'failed/timeout' (admin-only).

     Operator-side recovery primitive for builder microVMs that
    crashed (OOM, kernel panic, host reboot) and left their
    `builds` row in 'running' indefinitely. Mirrors the
    in-process reaper at pkg/builderd/reaper.go:48 — the
    operator-facing endpoint is the manual escape hatch for
    when the reaper's grace period is too long for an
    incident.

    `?older_than=` is clamped to [1m, 60m] so a fat-fingered
    \"1ns\" cannot sweep in-flight builds. Default 15m.

    Audit row: operator.action.reclaim_build with
    account_id=NULL (fleet-level, not tenant-scoped), including
    the normalized operator reason.

    Args:
        confirm (PostSweepStuckBuildsConfirm):
        older_than (str | Unset):  Example: 15m.
        reason (str | Unset):  Default: 'operator_reclaim_build'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SweepStuckBuildsResponse
    """

    return sync_detailed(
        client=client,
        confirm=confirm,
        older_than=older_than,
        reason=reason,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    confirm: PostSweepStuckBuildsConfirm,
    older_than: str | Unset = UNSET,
    reason: str | Unset = "operator_reclaim_build",
) -> Response[Problem | SweepStuckBuildsResponse]:
    r"""Flip every build row stuck in 'running' past the threshold to 'failed/timeout' (admin-only).

     Operator-side recovery primitive for builder microVMs that
    crashed (OOM, kernel panic, host reboot) and left their
    `builds` row in 'running' indefinitely. Mirrors the
    in-process reaper at pkg/builderd/reaper.go:48 — the
    operator-facing endpoint is the manual escape hatch for
    when the reaper's grace period is too long for an
    incident.

    `?older_than=` is clamped to [1m, 60m] so a fat-fingered
    \"1ns\" cannot sweep in-flight builds. Default 15m.

    Audit row: operator.action.reclaim_build with
    account_id=NULL (fleet-level, not tenant-scoped), including
    the normalized operator reason.

    Args:
        confirm (PostSweepStuckBuildsConfirm):
        older_than (str | Unset):  Example: 15m.
        reason (str | Unset):  Default: 'operator_reclaim_build'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SweepStuckBuildsResponse]
    """

    kwargs = _get_kwargs(
        confirm=confirm,
        older_than=older_than,
        reason=reason,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    confirm: PostSweepStuckBuildsConfirm,
    older_than: str | Unset = UNSET,
    reason: str | Unset = "operator_reclaim_build",
) -> Problem | SweepStuckBuildsResponse | None:
    r"""Flip every build row stuck in 'running' past the threshold to 'failed/timeout' (admin-only).

     Operator-side recovery primitive for builder microVMs that
    crashed (OOM, kernel panic, host reboot) and left their
    `builds` row in 'running' indefinitely. Mirrors the
    in-process reaper at pkg/builderd/reaper.go:48 — the
    operator-facing endpoint is the manual escape hatch for
    when the reaper's grace period is too long for an
    incident.

    `?older_than=` is clamped to [1m, 60m] so a fat-fingered
    \"1ns\" cannot sweep in-flight builds. Default 15m.

    Audit row: operator.action.reclaim_build with
    account_id=NULL (fleet-level, not tenant-scoped), including
    the normalized operator reason.

    Args:
        confirm (PostSweepStuckBuildsConfirm):
        older_than (str | Unset):  Example: 15m.
        reason (str | Unset):  Default: 'operator_reclaim_build'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SweepStuckBuildsResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            confirm=confirm,
            older_than=older_than,
            reason=reason,
        )
    ).parsed
