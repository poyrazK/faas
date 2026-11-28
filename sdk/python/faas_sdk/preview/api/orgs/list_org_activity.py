from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_org_activity_actor_type import ListOrgActivityActorType
from ...models.list_org_activity_response import ListOrgActivityResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
    kind_prefix: str | Unset = UNSET,
    actor_type: ListOrgActivityActorType | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["before"] = before

    params["limit"] = limit

    params["kind_prefix"] = kind_prefix

    json_actor_type: str | Unset = UNSET
    if not isinstance(actor_type, Unset):
        json_actor_type = actor_type

    params["actor_type"] = json_actor_type

    json_app_id: str | Unset = UNSET
    if not isinstance(app_id, Unset):
        json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{slug}/activity".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListOrgActivityResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListOrgActivityResponse.from_dict(response.json())

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
) -> Response[ListOrgActivityResponse | Problem]:
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
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
    kind_prefix: str | Unset = UNSET,
    actor_type: ListOrgActivityActorType | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
) -> Response[ListOrgActivityResponse | Problem]:
    """List the organization's global infrastructure activity.

     Returns one newest-first timeline across applications, deployments,
    environment configuration, domains, certificates, and automated
    platform actions. Every active member may read it (`org.view`).

    Entries are a curated customer-facing projection, not raw provider or
    security audit payloads. Labels are captured at write time and `data`
    contains non-secret display metadata only; environment values and
    credentials are never included. Pass `next_before` back unchanged as
    `before` to fetch the next older page.

    Args:
        slug (str):
        before (str | Unset):
        limit (int | Unset):  Default: 50.
        kind_prefix (str | Unset):
        actor_type (ListOrgActivityActorType | Unset):
        app_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListOrgActivityResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        before=before,
        limit=limit,
        kind_prefix=kind_prefix,
        actor_type=actor_type,
        app_id=app_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
    kind_prefix: str | Unset = UNSET,
    actor_type: ListOrgActivityActorType | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
) -> ListOrgActivityResponse | Problem | None:
    """List the organization's global infrastructure activity.

     Returns one newest-first timeline across applications, deployments,
    environment configuration, domains, certificates, and automated
    platform actions. Every active member may read it (`org.view`).

    Entries are a curated customer-facing projection, not raw provider or
    security audit payloads. Labels are captured at write time and `data`
    contains non-secret display metadata only; environment values and
    credentials are never included. Pass `next_before` back unchanged as
    `before` to fetch the next older page.

    Args:
        slug (str):
        before (str | Unset):
        limit (int | Unset):  Default: 50.
        kind_prefix (str | Unset):
        actor_type (ListOrgActivityActorType | Unset):
        app_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListOrgActivityResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        before=before,
        limit=limit,
        kind_prefix=kind_prefix,
        actor_type=actor_type,
        app_id=app_id,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
    kind_prefix: str | Unset = UNSET,
    actor_type: ListOrgActivityActorType | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
) -> Response[ListOrgActivityResponse | Problem]:
    """List the organization's global infrastructure activity.

     Returns one newest-first timeline across applications, deployments,
    environment configuration, domains, certificates, and automated
    platform actions. Every active member may read it (`org.view`).

    Entries are a curated customer-facing projection, not raw provider or
    security audit payloads. Labels are captured at write time and `data`
    contains non-secret display metadata only; environment values and
    credentials are never included. Pass `next_before` back unchanged as
    `before` to fetch the next older page.

    Args:
        slug (str):
        before (str | Unset):
        limit (int | Unset):  Default: 50.
        kind_prefix (str | Unset):
        actor_type (ListOrgActivityActorType | Unset):
        app_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListOrgActivityResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        before=before,
        limit=limit,
        kind_prefix=kind_prefix,
        actor_type=actor_type,
        app_id=app_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
    kind_prefix: str | Unset = UNSET,
    actor_type: ListOrgActivityActorType | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
) -> ListOrgActivityResponse | Problem | None:
    """List the organization's global infrastructure activity.

     Returns one newest-first timeline across applications, deployments,
    environment configuration, domains, certificates, and automated
    platform actions. Every active member may read it (`org.view`).

    Entries are a curated customer-facing projection, not raw provider or
    security audit payloads. Labels are captured at write time and `data`
    contains non-secret display metadata only; environment values and
    credentials are never included. Pass `next_before` back unchanged as
    `before` to fetch the next older page.

    Args:
        slug (str):
        before (str | Unset):
        limit (int | Unset):  Default: 50.
        kind_prefix (str | Unset):
        actor_type (ListOrgActivityActorType | Unset):
        app_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListOrgActivityResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            before=before,
            limit=limit,
            kind_prefix=kind_prefix,
            actor_type=actor_type,
            app_id=app_id,
        )
    ).parsed
