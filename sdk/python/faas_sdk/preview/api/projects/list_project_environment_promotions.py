from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_project_environment_promotions_status import (
    ListProjectEnvironmentPromotionsStatus,
)
from ...models.problem import Problem
from ...models.project_environment_promotion_list_response import ProjectEnvironmentPromotionListResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
    from_: str | Unset = UNSET,
    status: ListProjectEnvironmentPromotionsStatus | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["before"] = before

    params["limit"] = limit

    params["from"] = from_

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/promotions".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentPromotionListResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentPromotionListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[Problem | ProjectEnvironmentPromotionListResponse]:
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
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
    from_: str | Unset = UNSET,
    status: ListProjectEnvironmentPromotionsStatus | Unset = UNSET,
) -> Response[Problem | ProjectEnvironmentPromotionListResponse]:
    """List project environment promotion history.

     Returns newest-first compact promotion history. Use the promotion status endpoint for per-workload
    checkpoints.

    Args:
        slug (str):
        environment (str):
        before (str | Unset):
        limit (int | Unset):  Default: 50.
        from_ (str | Unset):
        status (ListProjectEnvironmentPromotionsStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentPromotionListResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        before=before,
        limit=limit,
        from_=from_,
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
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
    from_: str | Unset = UNSET,
    status: ListProjectEnvironmentPromotionsStatus | Unset = UNSET,
) -> Problem | ProjectEnvironmentPromotionListResponse | None:
    """List project environment promotion history.

     Returns newest-first compact promotion history. Use the promotion status endpoint for per-workload
    checkpoints.

    Args:
        slug (str):
        environment (str):
        before (str | Unset):
        limit (int | Unset):  Default: 50.
        from_ (str | Unset):
        status (ListProjectEnvironmentPromotionsStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentPromotionListResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        before=before,
        limit=limit,
        from_=from_,
        status=status,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
    from_: str | Unset = UNSET,
    status: ListProjectEnvironmentPromotionsStatus | Unset = UNSET,
) -> Response[Problem | ProjectEnvironmentPromotionListResponse]:
    """List project environment promotion history.

     Returns newest-first compact promotion history. Use the promotion status endpoint for per-workload
    checkpoints.

    Args:
        slug (str):
        environment (str):
        before (str | Unset):
        limit (int | Unset):  Default: 50.
        from_ (str | Unset):
        status (ListProjectEnvironmentPromotionsStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentPromotionListResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        before=before,
        limit=limit,
        from_=from_,
        status=status,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 50,
    from_: str | Unset = UNSET,
    status: ListProjectEnvironmentPromotionsStatus | Unset = UNSET,
) -> Problem | ProjectEnvironmentPromotionListResponse | None:
    """List project environment promotion history.

     Returns newest-first compact promotion history. Use the promotion status endpoint for per-workload
    checkpoints.

    Args:
        slug (str):
        environment (str):
        before (str | Unset):
        limit (int | Unset):  Default: 50.
        from_ (str | Unset):
        status (ListProjectEnvironmentPromotionsStatus | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentPromotionListResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            before=before,
            limit=limit,
            from_=from_,
            status=status,
        )
    ).parsed
