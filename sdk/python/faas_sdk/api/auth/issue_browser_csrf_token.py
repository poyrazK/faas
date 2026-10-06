from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.csrf_token_response import CSRFTokenResponse
from ...models.issue_browser_csrf_token_action import IssueBrowserCSRFTokenAction
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    action: IssueBrowserCSRFTokenAction,
    faas_sid: str | Unset = UNSET,
) -> dict[str, Any]:

    cookies = {}
    if faas_sid is not UNSET:
        cookies["faas_sid"] = faas_sid

    params: dict[str, Any] = {}

    json_action: str = action
    params["action"] = json_action

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/auth/csrf",
        "params": params,
        "cookies": cookies,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CSRFTokenResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CSRFTokenResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CSRFTokenResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    action: IssueBrowserCSRFTokenAction,
    faas_sid: str | Unset = UNSET,
) -> Response[CSRFTokenResponse | Problem]:
    """Issue an action-bound CSRF token for the dashboard.

     Returns a short-lived CSRF token bound to the authenticated
    account and the requested browser mutation. The matching
    `faas_csrf` cookie is HttpOnly; clients send the returned
    `csrf_token` in the mutation's JSON body. For `connect_github`,
    the cookie is `faas_csrf_github_connect` and clients send the
    token as a form field in a native browser POST to
    `/dashboard/install/connect`, which redirects to GitHub.
    This route remains reachable while the session is `mfa_pending` so the dashboard
    can complete MFA enrollment or recovery.

    Args:
        action (IssueBrowserCSRFTokenAction):
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CSRFTokenResponse | Problem]
    """

    kwargs = _get_kwargs(
        action=action,
        faas_sid=faas_sid,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    action: IssueBrowserCSRFTokenAction,
    faas_sid: str | Unset = UNSET,
) -> CSRFTokenResponse | Problem | None:
    """Issue an action-bound CSRF token for the dashboard.

     Returns a short-lived CSRF token bound to the authenticated
    account and the requested browser mutation. The matching
    `faas_csrf` cookie is HttpOnly; clients send the returned
    `csrf_token` in the mutation's JSON body. For `connect_github`,
    the cookie is `faas_csrf_github_connect` and clients send the
    token as a form field in a native browser POST to
    `/dashboard/install/connect`, which redirects to GitHub.
    This route remains reachable while the session is `mfa_pending` so the dashboard
    can complete MFA enrollment or recovery.

    Args:
        action (IssueBrowserCSRFTokenAction):
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CSRFTokenResponse | Problem
    """

    return sync_detailed(
        client=client,
        action=action,
        faas_sid=faas_sid,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    action: IssueBrowserCSRFTokenAction,
    faas_sid: str | Unset = UNSET,
) -> Response[CSRFTokenResponse | Problem]:
    """Issue an action-bound CSRF token for the dashboard.

     Returns a short-lived CSRF token bound to the authenticated
    account and the requested browser mutation. The matching
    `faas_csrf` cookie is HttpOnly; clients send the returned
    `csrf_token` in the mutation's JSON body. For `connect_github`,
    the cookie is `faas_csrf_github_connect` and clients send the
    token as a form field in a native browser POST to
    `/dashboard/install/connect`, which redirects to GitHub.
    This route remains reachable while the session is `mfa_pending` so the dashboard
    can complete MFA enrollment or recovery.

    Args:
        action (IssueBrowserCSRFTokenAction):
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CSRFTokenResponse | Problem]
    """

    kwargs = _get_kwargs(
        action=action,
        faas_sid=faas_sid,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    action: IssueBrowserCSRFTokenAction,
    faas_sid: str | Unset = UNSET,
) -> CSRFTokenResponse | Problem | None:
    """Issue an action-bound CSRF token for the dashboard.

     Returns a short-lived CSRF token bound to the authenticated
    account and the requested browser mutation. The matching
    `faas_csrf` cookie is HttpOnly; clients send the returned
    `csrf_token` in the mutation's JSON body. For `connect_github`,
    the cookie is `faas_csrf_github_connect` and clients send the
    token as a form field in a native browser POST to
    `/dashboard/install/connect`, which redirects to GitHub.
    This route remains reachable while the session is `mfa_pending` so the dashboard
    can complete MFA enrollment or recovery.

    Args:
        action (IssueBrowserCSRFTokenAction):
        faas_sid (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CSRFTokenResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            action=action,
            faas_sid=faas_sid,
        )
    ).parsed
