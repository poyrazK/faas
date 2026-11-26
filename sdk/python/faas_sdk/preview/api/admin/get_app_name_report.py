from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_name_report_response import AppNameReportResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    blocklist: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["blocklist"] = blocklist

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/app-names/report",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppNameReportResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppNameReportResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppNameReportResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    blocklist: str | Unset = UNSET,
) -> Response[AppNameReportResponse | Problem]:
    """Existing apps an app name blocklist matches (operator-only).

     Lists existing apps whose names the live `app_name_blocklist` runtime
    setting matches, for review. Nothing is changed: existing apps keep
    their names. With `blocklist` the report uses that list instead of the
    live one, so an operator can see a list's effect before setting it.
    Deleted apps that can still be restored are listed too (status
    `deleted`): a restore brings their name back. When more than 5000 apps
    match, the first 5000 by name are listed and `truncated` is true.

    Args:
        blocklist (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppNameReportResponse | Problem]
    """

    kwargs = _get_kwargs(
        blocklist=blocklist,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    blocklist: str | Unset = UNSET,
) -> AppNameReportResponse | Problem | None:
    """Existing apps an app name blocklist matches (operator-only).

     Lists existing apps whose names the live `app_name_blocklist` runtime
    setting matches, for review. Nothing is changed: existing apps keep
    their names. With `blocklist` the report uses that list instead of the
    live one, so an operator can see a list's effect before setting it.
    Deleted apps that can still be restored are listed too (status
    `deleted`): a restore brings their name back. When more than 5000 apps
    match, the first 5000 by name are listed and `truncated` is true.

    Args:
        blocklist (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppNameReportResponse | Problem
    """

    return sync_detailed(
        client=client,
        blocklist=blocklist,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    blocklist: str | Unset = UNSET,
) -> Response[AppNameReportResponse | Problem]:
    """Existing apps an app name blocklist matches (operator-only).

     Lists existing apps whose names the live `app_name_blocklist` runtime
    setting matches, for review. Nothing is changed: existing apps keep
    their names. With `blocklist` the report uses that list instead of the
    live one, so an operator can see a list's effect before setting it.
    Deleted apps that can still be restored are listed too (status
    `deleted`): a restore brings their name back. When more than 5000 apps
    match, the first 5000 by name are listed and `truncated` is true.

    Args:
        blocklist (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppNameReportResponse | Problem]
    """

    kwargs = _get_kwargs(
        blocklist=blocklist,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    blocklist: str | Unset = UNSET,
) -> AppNameReportResponse | Problem | None:
    """Existing apps an app name blocklist matches (operator-only).

     Lists existing apps whose names the live `app_name_blocklist` runtime
    setting matches, for review. Nothing is changed: existing apps keep
    their names. With `blocklist` the report uses that list instead of the
    live one, so an operator can see a list's effect before setting it.
    Deleted apps that can still be restored are listed too (status
    `deleted`): a restore brings their name back. When more than 5000 apps
    match, the first 5000 by name are listed and `truncated` is true.

    Args:
        blocklist (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppNameReportResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            blocklist=blocklist,
        )
    ).parsed
