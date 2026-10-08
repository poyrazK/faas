from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_operational_summary import AppOperationalSummary
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/operational-summary".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppOperationalSummary | Problem | None:
    if response.status_code == 200:
        response_200 = AppOperationalSummary.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppOperationalSummary | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppOperationalSummary | Problem]:
    """Read current production monitoring and recovery progress.

     Requires app read access and completed MFA. Joins current default-scope production route evidence,
    metadata for the saved open incident, pending checked rollbacks across app scopes, and pending or
    failed restart handoffs. Reads do not wake workloads, change traffic, or declare incident recovery.
    Component availability and bounded-list truncation are explicit. Deployment smoke verification
    remains a separate launch-time result. Customer identities, request evidence, free-form rollback
    reasons and internal restart errors are omitted. No query parameters are accepted.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppOperationalSummary | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> AppOperationalSummary | Problem | None:
    """Read current production monitoring and recovery progress.

     Requires app read access and completed MFA. Joins current default-scope production route evidence,
    metadata for the saved open incident, pending checked rollbacks across app scopes, and pending or
    failed restart handoffs. Reads do not wake workloads, change traffic, or declare incident recovery.
    Component availability and bounded-list truncation are explicit. Deployment smoke verification
    remains a separate launch-time result. Customer identities, request evidence, free-form rollback
    reasons and internal restart errors are omitted. No query parameters are accepted.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppOperationalSummary | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppOperationalSummary | Problem]:
    """Read current production monitoring and recovery progress.

     Requires app read access and completed MFA. Joins current default-scope production route evidence,
    metadata for the saved open incident, pending checked rollbacks across app scopes, and pending or
    failed restart handoffs. Reads do not wake workloads, change traffic, or declare incident recovery.
    Component availability and bounded-list truncation are explicit. Deployment smoke verification
    remains a separate launch-time result. Customer identities, request evidence, free-form rollback
    reasons and internal restart errors are omitted. No query parameters are accepted.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppOperationalSummary | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> AppOperationalSummary | Problem | None:
    """Read current production monitoring and recovery progress.

     Requires app read access and completed MFA. Joins current default-scope production route evidence,
    metadata for the saved open incident, pending checked rollbacks across app scopes, and pending or
    failed restart handoffs. Reads do not wake workloads, change traffic, or declare incident recovery.
    Component availability and bounded-list truncation are explicit. Deployment smoke verification
    remains a separate launch-time result. Customer identities, request evidence, free-form rollback
    reasons and internal restart errors are omitted. No query parameters are accepted.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppOperationalSummary | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
