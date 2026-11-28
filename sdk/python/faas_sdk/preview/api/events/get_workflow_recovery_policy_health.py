from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_recovery_policy_health import WorkflowRecoveryPolicyHealth
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    stall_after_seconds: int | Unset = 300,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["stall_after_seconds"] = stall_after_seconds

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/recovery-policy/health".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowRecoveryPolicyHealth | None:
    if response.status_code == 200:
        response_200 = WorkflowRecoveryPolicyHealth.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowRecoveryPolicyHealth]:
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
    stall_after_seconds: int | Unset = 300,
) -> Response[Problem | WorkflowRecoveryPolicyHealth]:
    """Inspect recovery automation health

     Read-only app-scoped recovery policy health from a consistent bounded queue snapshot. Expected
    cooldown and exhausted budgets are blocked rather than stalled. Stalled means worker evaluation is
    overdue, not that a running child exceeded its execution deadline. This is advisory and never
    releases slots or authorizes execution.

    Args:
        slug (str):
        stall_after_seconds (int | Unset):  Default: 300.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryPolicyHealth]
    """

    kwargs = _get_kwargs(
        slug=slug,
        stall_after_seconds=stall_after_seconds,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    stall_after_seconds: int | Unset = 300,
) -> Problem | WorkflowRecoveryPolicyHealth | None:
    """Inspect recovery automation health

     Read-only app-scoped recovery policy health from a consistent bounded queue snapshot. Expected
    cooldown and exhausted budgets are blocked rather than stalled. Stalled means worker evaluation is
    overdue, not that a running child exceeded its execution deadline. This is advisory and never
    releases slots or authorizes execution.

    Args:
        slug (str):
        stall_after_seconds (int | Unset):  Default: 300.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryPolicyHealth
    """

    return sync_detailed(
        slug=slug,
        client=client,
        stall_after_seconds=stall_after_seconds,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    stall_after_seconds: int | Unset = 300,
) -> Response[Problem | WorkflowRecoveryPolicyHealth]:
    """Inspect recovery automation health

     Read-only app-scoped recovery policy health from a consistent bounded queue snapshot. Expected
    cooldown and exhausted budgets are blocked rather than stalled. Stalled means worker evaluation is
    overdue, not that a running child exceeded its execution deadline. This is advisory and never
    releases slots or authorizes execution.

    Args:
        slug (str):
        stall_after_seconds (int | Unset):  Default: 300.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowRecoveryPolicyHealth]
    """

    kwargs = _get_kwargs(
        slug=slug,
        stall_after_seconds=stall_after_seconds,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    stall_after_seconds: int | Unset = 300,
) -> Problem | WorkflowRecoveryPolicyHealth | None:
    """Inspect recovery automation health

     Read-only app-scoped recovery policy health from a consistent bounded queue snapshot. Expected
    cooldown and exhausted budgets are blocked rather than stalled. Stalled means worker evaluation is
    overdue, not that a running child exceeded its execution deadline. This is advisory and never
    releases slots or authorizes execution.

    Args:
        slug (str):
        stall_after_seconds (int | Unset):  Default: 300.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowRecoveryPolicyHealth
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            stall_after_seconds=stall_after_seconds,
        )
    ).parsed
