from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.flag_lifecycle_report import FlagLifecycleReport
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/flags/lifecycle".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FlagLifecycleReport | Problem | None:
    if response.status_code == 200:
        response_200 = FlagLifecycleReport.from_dict(response.json())

        return response_200

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[FlagLifecycleReport | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[FlagLifecycleReport | Problem]:
    """Report each flag's owner, expiry, last change and use.

     Flags whose attention list is not empty need a decision: expired, expires_soon (within 7 days),
    stale (serves one value and unchanged for 14 days), never_evaluated (enabled, no decision in 7 days
    of retained request telemetry) or no_owner. An hourly check also records one
    flags.lifecycle_attention audit event per flag, reason and configuration version.

    Args:
        slug (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagLifecycleReport | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
) -> FlagLifecycleReport | Problem | None:
    """Report each flag's owner, expiry, last change and use.

     Flags whose attention list is not empty need a decision: expired, expires_soon (within 7 days),
    stale (serves one value and unchanged for 14 days), never_evaluated (enabled, no decision in 7 days
    of retained request telemetry) or no_owner. An hourly check also records one
    flags.lifecycle_attention audit event per flag, reason and configuration version.

    Args:
        slug (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagLifecycleReport | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[FlagLifecycleReport | Problem]:
    """Report each flag's owner, expiry, last change and use.

     Flags whose attention list is not empty need a decision: expired, expires_soon (within 7 days),
    stale (serves one value and unchanged for 14 days), never_evaluated (enabled, no decision in 7 days
    of retained request telemetry) or no_owner. An hourly check also records one
    flags.lifecycle_attention audit event per flag, reason and configuration version.

    Args:
        slug (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagLifecycleReport | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
) -> FlagLifecycleReport | Problem | None:
    """Report each flag's owner, expiry, last change and use.

     Flags whose attention list is not empty need a decision: expired, expires_soon (within 7 days),
    stale (serves one value and unchanged for 14 days), never_evaluated (enabled, no decision in 7 days
    of retained request telemetry) or no_owner. An hourly check also records one
    flags.lifecycle_attention audit event per flag, reason and configuration version.

    Args:
        slug (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagLifecycleReport | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
        )
    ).parsed
