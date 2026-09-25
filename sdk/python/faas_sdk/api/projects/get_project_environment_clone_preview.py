from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_clone_plan_response import ProjectEnvironmentClonePlanResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    to: str,
    share_resources: bool | Unset = False,
    preview_pr_number: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["to"] = to

    params["share_resources"] = share_resources

    params["preview_pr_number"] = preview_pr_number

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/clone-preview".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentClonePlanResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentClonePlanResponse.from_dict(response.json())

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
) -> Response[Problem | ProjectEnvironmentClonePlanResponse]:
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
    to: str,
    share_resources: bool | Unset = False,
    preview_pr_number: int | Unset = UNSET,
) -> Response[Problem | ProjectEnvironmentClonePlanResponse]:
    """Preflight an environment clone without creating resources.

     Reports non-secret resource actions, known provider blockers, and
    whether every source workload has a live release available for a later
    promotion. Account quotas are rechecked during create and are not
    reserved by this read-only endpoint. Secret values and keys are never
    included.

    Args:
        slug (str):
        environment (str):
        to (str):
        share_resources (bool | Unset):  Default: False.
        preview_pr_number (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentClonePlanResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        to=to,
        share_resources=share_resources,
        preview_pr_number=preview_pr_number,
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
    to: str,
    share_resources: bool | Unset = False,
    preview_pr_number: int | Unset = UNSET,
) -> Problem | ProjectEnvironmentClonePlanResponse | None:
    """Preflight an environment clone without creating resources.

     Reports non-secret resource actions, known provider blockers, and
    whether every source workload has a live release available for a later
    promotion. Account quotas are rechecked during create and are not
    reserved by this read-only endpoint. Secret values and keys are never
    included.

    Args:
        slug (str):
        environment (str):
        to (str):
        share_resources (bool | Unset):  Default: False.
        preview_pr_number (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentClonePlanResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        to=to,
        share_resources=share_resources,
        preview_pr_number=preview_pr_number,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    to: str,
    share_resources: bool | Unset = False,
    preview_pr_number: int | Unset = UNSET,
) -> Response[Problem | ProjectEnvironmentClonePlanResponse]:
    """Preflight an environment clone without creating resources.

     Reports non-secret resource actions, known provider blockers, and
    whether every source workload has a live release available for a later
    promotion. Account quotas are rechecked during create and are not
    reserved by this read-only endpoint. Secret values and keys are never
    included.

    Args:
        slug (str):
        environment (str):
        to (str):
        share_resources (bool | Unset):  Default: False.
        preview_pr_number (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentClonePlanResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        to=to,
        share_resources=share_resources,
        preview_pr_number=preview_pr_number,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    to: str,
    share_resources: bool | Unset = False,
    preview_pr_number: int | Unset = UNSET,
) -> Problem | ProjectEnvironmentClonePlanResponse | None:
    """Preflight an environment clone without creating resources.

     Reports non-secret resource actions, known provider blockers, and
    whether every source workload has a live release available for a later
    promotion. Account quotas are rechecked during create and are not
    reserved by this read-only endpoint. Secret values and keys are never
    included.

    Args:
        slug (str):
        environment (str):
        to (str):
        share_resources (bool | Unset):  Default: False.
        preview_pr_number (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentClonePlanResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            to=to,
            share_resources=share_resources,
            preview_pr_number=preview_pr_number,
        )
    ).parsed
