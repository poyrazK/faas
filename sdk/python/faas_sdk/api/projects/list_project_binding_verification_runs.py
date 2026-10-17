from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.binding_verification_run_list import BindingVerificationRunList
from ...models.list_project_binding_verification_runs_status import (
    ListProjectBindingVerificationRunsStatus,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    status: ListProjectBindingVerificationRunsStatus | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    params["cursor"] = cursor

    json_deployment_id: str | Unset = UNSET
    if not isinstance(deployment_id, Unset):
        json_deployment_id = str(deployment_id)
    params["deployment_id"] = json_deployment_id

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BindingVerificationRunList | Problem | None:
    if response.status_code == 200:
        response_200 = BindingVerificationRunList.from_dict(response.json())

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
) -> Response[BindingVerificationRunList | Problem]:
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
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    status: ListProjectBindingVerificationRunsStatus | Unset = UNSET,
) -> Response[BindingVerificationRunList | Problem]:
    """List retained verification receipt summaries without requalifying evidence.

     Newest first with creation-time and UUID keyset pagination. Reuse filters with next_cursor.
    observed_at freezes the expiry cutoff; recorded state may change between pages. Listings grant no
    admission or release authority.

    Args:
        slug (str):
        environment (str):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):
        deployment_id (UUID | Unset):
        status (ListProjectBindingVerificationRunsStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BindingVerificationRunList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        limit=limit,
        cursor=cursor,
        deployment_id=deployment_id,
        status=status,
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
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    status: ListProjectBindingVerificationRunsStatus | Unset = UNSET,
) -> BindingVerificationRunList | Problem | None:
    """List retained verification receipt summaries without requalifying evidence.

     Newest first with creation-time and UUID keyset pagination. Reuse filters with next_cursor.
    observed_at freezes the expiry cutoff; recorded state may change between pages. Listings grant no
    admission or release authority.

    Args:
        slug (str):
        environment (str):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):
        deployment_id (UUID | Unset):
        status (ListProjectBindingVerificationRunsStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BindingVerificationRunList | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        limit=limit,
        cursor=cursor,
        deployment_id=deployment_id,
        status=status,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    status: ListProjectBindingVerificationRunsStatus | Unset = UNSET,
) -> Response[BindingVerificationRunList | Problem]:
    """List retained verification receipt summaries without requalifying evidence.

     Newest first with creation-time and UUID keyset pagination. Reuse filters with next_cursor.
    observed_at freezes the expiry cutoff; recorded state may change between pages. Listings grant no
    admission or release authority.

    Args:
        slug (str):
        environment (str):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):
        deployment_id (UUID | Unset):
        status (ListProjectBindingVerificationRunsStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BindingVerificationRunList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        limit=limit,
        cursor=cursor,
        deployment_id=deployment_id,
        status=status,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    cursor: str | Unset = UNSET,
    deployment_id: UUID | Unset = UNSET,
    status: ListProjectBindingVerificationRunsStatus | Unset = UNSET,
) -> BindingVerificationRunList | Problem | None:
    """List retained verification receipt summaries without requalifying evidence.

     Newest first with creation-time and UUID keyset pagination. Reuse filters with next_cursor.
    observed_at freezes the expiry cutoff; recorded state may change between pages. Listings grant no
    admission or release authority.

    Args:
        slug (str):
        environment (str):
        limit (int | Unset):  Default: 20.
        cursor (str | Unset):
        deployment_id (UUID | Unset):
        status (ListProjectBindingVerificationRunsStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BindingVerificationRunList | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            limit=limit,
            cursor=cursor,
            deployment_id=deployment_id,
            status=status,
        )
    ).parsed
