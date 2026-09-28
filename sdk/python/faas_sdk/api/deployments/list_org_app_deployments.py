import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.org_app_deployment_list_response import OrgAppDeploymentListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    app_slug: str,
    *,
    limit: int | Unset = 50,
    before: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    json_before: str | Unset = UNSET
    if not isinstance(before, Unset):
        json_before = before.isoformat()
    params["before"] = json_before

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{slug}/apps/{app_slug}/deployments".format(
            slug=quote(str(slug), safe=""),
            app_slug=quote(str(app_slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OrgAppDeploymentListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OrgAppDeploymentListResponse.from_dict(response.json())

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
) -> Response[OrgAppDeploymentListResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    app_slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    before: datetime.datetime | Unset = UNSET,
) -> Response[OrgAppDeploymentListResponse | Problem]:
    """List safe deployment status for a workspace app.

     Returns newest-first deployment summaries for an active application
    whose persisted `org_id` matches this workspace. Every active member
    may read this projection (`org.view`). It contains only deployment id,
    revision, kind, status, and creation time; detailed configuration and
    diagnostics remain creator-scoped. Use the organization activity
    timeline for captured actor attribution. `next_before` is an RFC3339Nano
    cursor from the last row; pass it as `before` to fetch older deployments.

    Args:
        slug (str):
        app_slug (str):
        limit (int | Unset):  Default: 50.
        before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgAppDeploymentListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        app_slug=app_slug,
        limit=limit,
        before=before,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    app_slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    before: datetime.datetime | Unset = UNSET,
) -> OrgAppDeploymentListResponse | Problem | None:
    """List safe deployment status for a workspace app.

     Returns newest-first deployment summaries for an active application
    whose persisted `org_id` matches this workspace. Every active member
    may read this projection (`org.view`). It contains only deployment id,
    revision, kind, status, and creation time; detailed configuration and
    diagnostics remain creator-scoped. Use the organization activity
    timeline for captured actor attribution. `next_before` is an RFC3339Nano
    cursor from the last row; pass it as `before` to fetch older deployments.

    Args:
        slug (str):
        app_slug (str):
        limit (int | Unset):  Default: 50.
        before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgAppDeploymentListResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        app_slug=app_slug,
        client=client,
        limit=limit,
        before=before,
    ).parsed


async def asyncio_detailed(
    slug: str,
    app_slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    before: datetime.datetime | Unset = UNSET,
) -> Response[OrgAppDeploymentListResponse | Problem]:
    """List safe deployment status for a workspace app.

     Returns newest-first deployment summaries for an active application
    whose persisted `org_id` matches this workspace. Every active member
    may read this projection (`org.view`). It contains only deployment id,
    revision, kind, status, and creation time; detailed configuration and
    diagnostics remain creator-scoped. Use the organization activity
    timeline for captured actor attribution. `next_before` is an RFC3339Nano
    cursor from the last row; pass it as `before` to fetch older deployments.

    Args:
        slug (str):
        app_slug (str):
        limit (int | Unset):  Default: 50.
        before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgAppDeploymentListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        app_slug=app_slug,
        limit=limit,
        before=before,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    app_slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    before: datetime.datetime | Unset = UNSET,
) -> OrgAppDeploymentListResponse | Problem | None:
    """List safe deployment status for a workspace app.

     Returns newest-first deployment summaries for an active application
    whose persisted `org_id` matches this workspace. Every active member
    may read this projection (`org.view`). It contains only deployment id,
    revision, kind, status, and creation time; detailed configuration and
    diagnostics remain creator-scoped. Use the organization activity
    timeline for captured actor attribution. `next_before` is an RFC3339Nano
    cursor from the last row; pass it as `before` to fetch older deployments.

    Args:
        slug (str):
        app_slug (str):
        limit (int | Unset):  Default: 50.
        before (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgAppDeploymentListResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            app_slug=app_slug,
            client=client,
            limit=limit,
            before=before,
        )
    ).parsed
