from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.install_bind_request import InstallBindRequest
from ...models.install_bind_response import InstallBindResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: InstallBindRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/install/bind".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> InstallBindResponse | Problem | None:
    if response.status_code == 200:
        response_200 = InstallBindResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[InstallBindResponse | Problem]:
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
    body: InstallBindRequest,
) -> Response[InstallBindResponse | Problem]:
    """Persist the (account, app, installation, repo, branch) bind row.

     Cookie-session-authenticated (NOT API-key). Persists the
    GitHub install binding.

    the exact installation must be durably connected to
    the session's Gregale account. Requires the named, account-bound
    bind_app_to_repo CSRF proof. A separate GitHub sign-in claim is not
    required. Live installation verification and the GitHub integration's current
    repository access check still run before persistence.

    On success emits `auth.install.bound` for the audit trail.

    Args:
        slug (str):
        body (InstallBindRequest): Body for both `POST /v1/install/repos/list` and
            `POST /v1/apps/{slug}/install/bind`. Carries the
            (installation_id, repo_full_name, production_branch) tuple
            the dashboard's bind picker commits. `production_branch` is
            optional — when omitted, the GitHub integration uses the install's
            `default_branch` from `/installations/{id}`.
            Binding requires a `csrf_token` issued for `bind_app_to_repo`
            by `GET /v1/auth/csrf` and its matching HttpOnly cookie.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InstallBindResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: InstallBindRequest,
) -> InstallBindResponse | Problem | None:
    """Persist the (account, app, installation, repo, branch) bind row.

     Cookie-session-authenticated (NOT API-key). Persists the
    GitHub install binding.

    the exact installation must be durably connected to
    the session's Gregale account. Requires the named, account-bound
    bind_app_to_repo CSRF proof. A separate GitHub sign-in claim is not
    required. Live installation verification and the GitHub integration's current
    repository access check still run before persistence.

    On success emits `auth.install.bound` for the audit trail.

    Args:
        slug (str):
        body (InstallBindRequest): Body for both `POST /v1/install/repos/list` and
            `POST /v1/apps/{slug}/install/bind`. Carries the
            (installation_id, repo_full_name, production_branch) tuple
            the dashboard's bind picker commits. `production_branch` is
            optional — when omitted, the GitHub integration uses the install's
            `default_branch` from `/installations/{id}`.
            Binding requires a `csrf_token` issued for `bind_app_to_repo`
            by `GET /v1/auth/csrf` and its matching HttpOnly cookie.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InstallBindResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: InstallBindRequest,
) -> Response[InstallBindResponse | Problem]:
    """Persist the (account, app, installation, repo, branch) bind row.

     Cookie-session-authenticated (NOT API-key). Persists the
    GitHub install binding.

    the exact installation must be durably connected to
    the session's Gregale account. Requires the named, account-bound
    bind_app_to_repo CSRF proof. A separate GitHub sign-in claim is not
    required. Live installation verification and the GitHub integration's current
    repository access check still run before persistence.

    On success emits `auth.install.bound` for the audit trail.

    Args:
        slug (str):
        body (InstallBindRequest): Body for both `POST /v1/install/repos/list` and
            `POST /v1/apps/{slug}/install/bind`. Carries the
            (installation_id, repo_full_name, production_branch) tuple
            the dashboard's bind picker commits. `production_branch` is
            optional — when omitted, the GitHub integration uses the install's
            `default_branch` from `/installations/{id}`.
            Binding requires a `csrf_token` issued for `bind_app_to_repo`
            by `GET /v1/auth/csrf` and its matching HttpOnly cookie.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InstallBindResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: InstallBindRequest,
) -> InstallBindResponse | Problem | None:
    """Persist the (account, app, installation, repo, branch) bind row.

     Cookie-session-authenticated (NOT API-key). Persists the
    GitHub install binding.

    the exact installation must be durably connected to
    the session's Gregale account. Requires the named, account-bound
    bind_app_to_repo CSRF proof. A separate GitHub sign-in claim is not
    required. Live installation verification and the GitHub integration's current
    repository access check still run before persistence.

    On success emits `auth.install.bound` for the audit trail.

    Args:
        slug (str):
        body (InstallBindRequest): Body for both `POST /v1/install/repos/list` and
            `POST /v1/apps/{slug}/install/bind`. Carries the
            (installation_id, repo_full_name, production_branch) tuple
            the dashboard's bind picker commits. `production_branch` is
            optional — when omitted, the GitHub integration uses the install's
            `default_branch` from `/installations/{id}`.
            Binding requires a `csrf_token` issued for `bind_app_to_repo`
            by `GET /v1/auth/csrf` and its matching HttpOnly cookie.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InstallBindResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
