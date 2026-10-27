from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_queue_bindings_response import ProjectEnvironmentQueueBindingsResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    workload: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/workloads/{workload}/queue-bindings".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            workload=quote(str(workload), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentQueueBindingsResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentQueueBindingsResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProjectEnvironmentQueueBindingsResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    workload: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ProjectEnvironmentQueueBindingsResponse]:
    """Read a stage workload's complete desired queue configuration.

     Returns logical definitions and the current workload revision. A missing
    collection returns 409 environment_queue_collection_unavailable and never
    inherits production queues. X-Gregale-Workload-Revision identifies the
    head to use when initializing or replacing the collection.
    Consumer activation is currently unavailable; saved definitions do not
    activate delivery or qualify the stage for promotion.

    Args:
        slug (str):
        environment (str):
        workload (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentQueueBindingsResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        workload=workload,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    workload: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ProjectEnvironmentQueueBindingsResponse | None:
    """Read a stage workload's complete desired queue configuration.

     Returns logical definitions and the current workload revision. A missing
    collection returns 409 environment_queue_collection_unavailable and never
    inherits production queues. X-Gregale-Workload-Revision identifies the
    head to use when initializing or replacing the collection.
    Consumer activation is currently unavailable; saved definitions do not
    activate delivery or qualify the stage for promotion.

    Args:
        slug (str):
        environment (str):
        workload (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentQueueBindingsResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        workload=workload,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    workload: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ProjectEnvironmentQueueBindingsResponse]:
    """Read a stage workload's complete desired queue configuration.

     Returns logical definitions and the current workload revision. A missing
    collection returns 409 environment_queue_collection_unavailable and never
    inherits production queues. X-Gregale-Workload-Revision identifies the
    head to use when initializing or replacing the collection.
    Consumer activation is currently unavailable; saved definitions do not
    activate delivery or qualify the stage for promotion.

    Args:
        slug (str):
        environment (str):
        workload (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentQueueBindingsResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        workload=workload,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    workload: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ProjectEnvironmentQueueBindingsResponse | None:
    """Read a stage workload's complete desired queue configuration.

     Returns logical definitions and the current workload revision. A missing
    collection returns 409 environment_queue_collection_unavailable and never
    inherits production queues. X-Gregale-Workload-Revision identifies the
    head to use when initializing or replacing the collection.
    Consumer activation is currently unavailable; saved definitions do not
    activate delivery or qualify the stage for promotion.

    Args:
        slug (str):
        environment (str):
        workload (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentQueueBindingsResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            workload=workload,
            client=client,
        )
    ).parsed
