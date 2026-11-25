from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_rollback_operation import ProjectRollbackOperation
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    to_deploy: UUID | Unset = UNSET,
    all_: bool | Unset = False,
    acknowledge_releases: bool | Unset = False,
    verify: bool | Unset = False,
    verify_before_cutover: bool | Unset = False,
    stabilize_for_seconds: int | Unset = 0,
    verify_live_traffic: bool | Unset = False,
    undo_on_health_failure: bool | Unset = False,
    undo_on_live_traffic_failure: bool | Unset = False,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_to_deploy: str | Unset = UNSET
    if not isinstance(to_deploy, Unset):
        json_to_deploy = str(to_deploy)
    params["to_deploy"] = json_to_deploy

    params["all"] = all_

    params["acknowledge_releases"] = acknowledge_releases

    params["verify"] = verify

    params["verify_before_cutover"] = verify_before_cutover

    params["stabilize_for_seconds"] = stabilize_for_seconds

    params["verify_live_traffic"] = verify_live_traffic

    params["undo_on_health_failure"] = undo_on_health_failure

    params["undo_on_live_traffic_failure"] = undo_on_live_traffic_failure

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/rollbacks/preview".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectRollbackOperation | None:
    if response.status_code == 200:
        response_200 = ProjectRollbackOperation.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProjectRollbackOperation]:
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
    to_deploy: UUID | Unset = UNSET,
    all_: bool | Unset = False,
    acknowledge_releases: bool | Unset = False,
    verify: bool | Unset = False,
    verify_before_cutover: bool | Unset = False,
    stabilize_for_seconds: int | Unset = 0,
    verify_live_traffic: bool | Unset = False,
    undo_on_health_failure: bool | Unset = False,
    undo_on_live_traffic_failure: bool | Unset = False,
) -> Response[Problem | ProjectRollbackOperation]:
    """Preview a project rollback and its service blockers.

    Args:
        slug (str):
        to_deploy (UUID | Unset):
        all_ (bool | Unset):  Default: False.
        acknowledge_releases (bool | Unset):  Default: False.
        verify (bool | Unset):  Default: False.
        verify_before_cutover (bool | Unset):  Default: False.
        stabilize_for_seconds (int | Unset):  Default: 0.
        verify_live_traffic (bool | Unset):  Default: False.
        undo_on_health_failure (bool | Unset):  Default: False.
        undo_on_live_traffic_failure (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectRollbackOperation]
    """

    kwargs = _get_kwargs(
        slug=slug,
        to_deploy=to_deploy,
        all_=all_,
        acknowledge_releases=acknowledge_releases,
        verify=verify,
        verify_before_cutover=verify_before_cutover,
        stabilize_for_seconds=stabilize_for_seconds,
        verify_live_traffic=verify_live_traffic,
        undo_on_health_failure=undo_on_health_failure,
        undo_on_live_traffic_failure=undo_on_live_traffic_failure,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    to_deploy: UUID | Unset = UNSET,
    all_: bool | Unset = False,
    acknowledge_releases: bool | Unset = False,
    verify: bool | Unset = False,
    verify_before_cutover: bool | Unset = False,
    stabilize_for_seconds: int | Unset = 0,
    verify_live_traffic: bool | Unset = False,
    undo_on_health_failure: bool | Unset = False,
    undo_on_live_traffic_failure: bool | Unset = False,
) -> Problem | ProjectRollbackOperation | None:
    """Preview a project rollback and its service blockers.

    Args:
        slug (str):
        to_deploy (UUID | Unset):
        all_ (bool | Unset):  Default: False.
        acknowledge_releases (bool | Unset):  Default: False.
        verify (bool | Unset):  Default: False.
        verify_before_cutover (bool | Unset):  Default: False.
        stabilize_for_seconds (int | Unset):  Default: 0.
        verify_live_traffic (bool | Unset):  Default: False.
        undo_on_health_failure (bool | Unset):  Default: False.
        undo_on_live_traffic_failure (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectRollbackOperation
    """

    return sync_detailed(
        slug=slug,
        client=client,
        to_deploy=to_deploy,
        all_=all_,
        acknowledge_releases=acknowledge_releases,
        verify=verify,
        verify_before_cutover=verify_before_cutover,
        stabilize_for_seconds=stabilize_for_seconds,
        verify_live_traffic=verify_live_traffic,
        undo_on_health_failure=undo_on_health_failure,
        undo_on_live_traffic_failure=undo_on_live_traffic_failure,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    to_deploy: UUID | Unset = UNSET,
    all_: bool | Unset = False,
    acknowledge_releases: bool | Unset = False,
    verify: bool | Unset = False,
    verify_before_cutover: bool | Unset = False,
    stabilize_for_seconds: int | Unset = 0,
    verify_live_traffic: bool | Unset = False,
    undo_on_health_failure: bool | Unset = False,
    undo_on_live_traffic_failure: bool | Unset = False,
) -> Response[Problem | ProjectRollbackOperation]:
    """Preview a project rollback and its service blockers.

    Args:
        slug (str):
        to_deploy (UUID | Unset):
        all_ (bool | Unset):  Default: False.
        acknowledge_releases (bool | Unset):  Default: False.
        verify (bool | Unset):  Default: False.
        verify_before_cutover (bool | Unset):  Default: False.
        stabilize_for_seconds (int | Unset):  Default: 0.
        verify_live_traffic (bool | Unset):  Default: False.
        undo_on_health_failure (bool | Unset):  Default: False.
        undo_on_live_traffic_failure (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectRollbackOperation]
    """

    kwargs = _get_kwargs(
        slug=slug,
        to_deploy=to_deploy,
        all_=all_,
        acknowledge_releases=acknowledge_releases,
        verify=verify,
        verify_before_cutover=verify_before_cutover,
        stabilize_for_seconds=stabilize_for_seconds,
        verify_live_traffic=verify_live_traffic,
        undo_on_health_failure=undo_on_health_failure,
        undo_on_live_traffic_failure=undo_on_live_traffic_failure,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    to_deploy: UUID | Unset = UNSET,
    all_: bool | Unset = False,
    acknowledge_releases: bool | Unset = False,
    verify: bool | Unset = False,
    verify_before_cutover: bool | Unset = False,
    stabilize_for_seconds: int | Unset = 0,
    verify_live_traffic: bool | Unset = False,
    undo_on_health_failure: bool | Unset = False,
    undo_on_live_traffic_failure: bool | Unset = False,
) -> Problem | ProjectRollbackOperation | None:
    """Preview a project rollback and its service blockers.

    Args:
        slug (str):
        to_deploy (UUID | Unset):
        all_ (bool | Unset):  Default: False.
        acknowledge_releases (bool | Unset):  Default: False.
        verify (bool | Unset):  Default: False.
        verify_before_cutover (bool | Unset):  Default: False.
        stabilize_for_seconds (int | Unset):  Default: 0.
        verify_live_traffic (bool | Unset):  Default: False.
        undo_on_health_failure (bool | Unset):  Default: False.
        undo_on_live_traffic_failure (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectRollbackOperation
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            to_deploy=to_deploy,
            all_=all_,
            acknowledge_releases=acknowledge_releases,
            verify=verify,
            verify_before_cutover=verify_before_cutover,
            stabilize_for_seconds=stabilize_for_seconds,
            verify_live_traffic=verify_live_traffic,
            undo_on_health_failure=undo_on_health_failure,
            undo_on_live_traffic_failure=undo_on_live_traffic_failure,
        )
    ).parsed
