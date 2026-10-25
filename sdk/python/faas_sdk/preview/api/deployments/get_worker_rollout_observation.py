from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.get_worker_rollout_observation_scope import (
    GetWorkerRolloutObservationScope,
)
from ...models.problem import Problem
from ...models.worker_rollout_observation import WorkerRolloutObservation
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    environment: str | Unset = UNSET,
    scope: GetWorkerRolloutObservationScope | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["environment"] = environment

    json_scope: str | Unset = UNSET
    if not isinstance(scope, Unset):
        json_scope = scope

    params["scope"] = json_scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/rollouts/{id}/observation".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkerRolloutObservation:
    if response.status_code == 200:
        response_200 = WorkerRolloutObservation.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkerRolloutObservation]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
    environment: str | Unset = UNSET,
    scope: GetWorkerRolloutObservationScope | Unset = UNSET,
) -> Response[Problem | WorkerRolloutObservation]:
    """Read a coherent worker-generation residency snapshot

    Args:
        slug (str):
        id (UUID):
        environment (str | Unset):
        scope (GetWorkerRolloutObservationScope | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkerRolloutObservation]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        environment=environment,
        scope=scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
    environment: str | Unset = UNSET,
    scope: GetWorkerRolloutObservationScope | Unset = UNSET,
) -> Problem | WorkerRolloutObservation | None:
    """Read a coherent worker-generation residency snapshot

    Args:
        slug (str):
        id (UUID):
        environment (str | Unset):
        scope (GetWorkerRolloutObservationScope | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkerRolloutObservation
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        environment=environment,
        scope=scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
    environment: str | Unset = UNSET,
    scope: GetWorkerRolloutObservationScope | Unset = UNSET,
) -> Response[Problem | WorkerRolloutObservation]:
    """Read a coherent worker-generation residency snapshot

    Args:
        slug (str):
        id (UUID):
        environment (str | Unset):
        scope (GetWorkerRolloutObservationScope | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkerRolloutObservation]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        environment=environment,
        scope=scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient,
    environment: str | Unset = UNSET,
    scope: GetWorkerRolloutObservationScope | Unset = UNSET,
) -> Problem | WorkerRolloutObservation | None:
    """Read a coherent worker-generation residency snapshot

    Args:
        slug (str):
        id (UUID):
        environment (str | Unset):
        scope (GetWorkerRolloutObservationScope | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkerRolloutObservation
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            environment=environment,
            scope=scope,
        )
    ).parsed
