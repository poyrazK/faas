from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.install_bind_request import InstallBindRequest
from ...models.problem import Problem
from ...models.repo_response import RepoResponse
from ...types import Response


def _get_kwargs(
    *,
    body: InstallBindRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/install/repos/list",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[RepoResponse] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = RepoResponse.from_dict(response_200_item_data)

            response_200.append(response_200_item)

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | list[RepoResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: InstallBindRequest,
) -> Response[Problem | list[RepoResponse]]:
    """List repos the user's GitHub App installation can see.

     Cookie-session-authenticated (NOT API-key). Hydrates the
    dashboard bind picker's repo dropdown. Returns the repos
    visible to the user's GitHub App installation — githubd
    resolves the per-install token from the session's account.

    ADR-1345 / §11: the exact installation must be durably connected to
    the session's Gregale account before any GitHub call. A separate
    GitHub sign-in claim is not required. Live installation verification
    and account-scoped repository access checks still apply.

    Args:
        body (InstallBindRequest): Body for both `POST /v1/install/repos/list` and
            `POST /v1/apps/{slug}/install/bind`. Carries the
            (installation_id, repo_full_name, production_branch) tuple
            the dashboard's bind picker commits. `production_branch` is
            optional — when omitted, githubd uses the install's
            `default_branch` from `/installations/{id}`.
            Binding requires a `csrf_token` issued for `bind_app_to_repo`
            by `GET /v1/auth/csrf` and its matching HttpOnly cookie.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[RepoResponse]]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: InstallBindRequest,
) -> Problem | list[RepoResponse] | None:
    """List repos the user's GitHub App installation can see.

     Cookie-session-authenticated (NOT API-key). Hydrates the
    dashboard bind picker's repo dropdown. Returns the repos
    visible to the user's GitHub App installation — githubd
    resolves the per-install token from the session's account.

    ADR-1345 / §11: the exact installation must be durably connected to
    the session's Gregale account before any GitHub call. A separate
    GitHub sign-in claim is not required. Live installation verification
    and account-scoped repository access checks still apply.

    Args:
        body (InstallBindRequest): Body for both `POST /v1/install/repos/list` and
            `POST /v1/apps/{slug}/install/bind`. Carries the
            (installation_id, repo_full_name, production_branch) tuple
            the dashboard's bind picker commits. `production_branch` is
            optional — when omitted, githubd uses the install's
            `default_branch` from `/installations/{id}`.
            Binding requires a `csrf_token` issued for `bind_app_to_repo`
            by `GET /v1/auth/csrf` and its matching HttpOnly cookie.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[RepoResponse]
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: InstallBindRequest,
) -> Response[Problem | list[RepoResponse]]:
    """List repos the user's GitHub App installation can see.

     Cookie-session-authenticated (NOT API-key). Hydrates the
    dashboard bind picker's repo dropdown. Returns the repos
    visible to the user's GitHub App installation — githubd
    resolves the per-install token from the session's account.

    ADR-1345 / §11: the exact installation must be durably connected to
    the session's Gregale account before any GitHub call. A separate
    GitHub sign-in claim is not required. Live installation verification
    and account-scoped repository access checks still apply.

    Args:
        body (InstallBindRequest): Body for both `POST /v1/install/repos/list` and
            `POST /v1/apps/{slug}/install/bind`. Carries the
            (installation_id, repo_full_name, production_branch) tuple
            the dashboard's bind picker commits. `production_branch` is
            optional — when omitted, githubd uses the install's
            `default_branch` from `/installations/{id}`.
            Binding requires a `csrf_token` issued for `bind_app_to_repo`
            by `GET /v1/auth/csrf` and its matching HttpOnly cookie.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[RepoResponse]]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: InstallBindRequest,
) -> Problem | list[RepoResponse] | None:
    """List repos the user's GitHub App installation can see.

     Cookie-session-authenticated (NOT API-key). Hydrates the
    dashboard bind picker's repo dropdown. Returns the repos
    visible to the user's GitHub App installation — githubd
    resolves the per-install token from the session's account.

    ADR-1345 / §11: the exact installation must be durably connected to
    the session's Gregale account before any GitHub call. A separate
    GitHub sign-in claim is not required. Live installation verification
    and account-scoped repository access checks still apply.

    Args:
        body (InstallBindRequest): Body for both `POST /v1/install/repos/list` and
            `POST /v1/apps/{slug}/install/bind`. Carries the
            (installation_id, repo_full_name, production_branch) tuple
            the dashboard's bind picker commits. `production_branch` is
            optional — when omitted, githubd uses the install's
            `default_branch` from `/installations/{id}`.
            Binding requires a `csrf_token` issued for `bind_app_to_repo`
            by `GET /v1/auth/csrf` and its matching HttpOnly cookie.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[RepoResponse]
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
