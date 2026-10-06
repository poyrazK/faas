from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_release_check_response import ProjectReleaseCheckResponse
from ...models.publish_project_release_set_request import PublishProjectReleaseSetRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: PublishProjectReleaseSetRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/release-sets/check".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectReleaseCheckResponse | None:
    if response.status_code == 200:
        response_200 = ProjectReleaseCheckResponse.from_dict(response.json())

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
) -> Response[Problem | ProjectReleaseCheckResponse]:
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
    body: PublishProjectReleaseSetRequest,
) -> Response[Problem | ProjectReleaseCheckResponse]:
    """Qualify an exact candidate deployment graph without changing routing.

     Requires expected_active_release_id, including empty for no active graph. Reads binding evidence and
    returns per-member blockers. Does not create probes, wake services, or invoke handlers. A passed
    report is an observation; publication rechecks all evidence under locks.

    Args:
        slug (str):
        environment (str):
        body (PublishProjectReleaseSetRequest): A complete mapping of project workload slugs to
            live deployment UUIDs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectReleaseCheckResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
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
    body: PublishProjectReleaseSetRequest,
) -> Problem | ProjectReleaseCheckResponse | None:
    """Qualify an exact candidate deployment graph without changing routing.

     Requires expected_active_release_id, including empty for no active graph. Reads binding evidence and
    returns per-member blockers. Does not create probes, wake services, or invoke handlers. A passed
    report is an observation; publication rechecks all evidence under locks.

    Args:
        slug (str):
        environment (str):
        body (PublishProjectReleaseSetRequest): A complete mapping of project workload slugs to
            live deployment UUIDs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectReleaseCheckResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: PublishProjectReleaseSetRequest,
) -> Response[Problem | ProjectReleaseCheckResponse]:
    """Qualify an exact candidate deployment graph without changing routing.

     Requires expected_active_release_id, including empty for no active graph. Reads binding evidence and
    returns per-member blockers. Does not create probes, wake services, or invoke handlers. A passed
    report is an observation; publication rechecks all evidence under locks.

    Args:
        slug (str):
        environment (str):
        body (PublishProjectReleaseSetRequest): A complete mapping of project workload slugs to
            live deployment UUIDs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectReleaseCheckResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: PublishProjectReleaseSetRequest,
) -> Problem | ProjectReleaseCheckResponse | None:
    """Qualify an exact candidate deployment graph without changing routing.

     Requires expected_active_release_id, including empty for no active graph. Reads binding evidence and
    returns per-member blockers. Does not create probes, wake services, or invoke handlers. A passed
    report is an observation; publication rechecks all evidence under locks.

    Args:
        slug (str):
        environment (str):
        body (PublishProjectReleaseSetRequest): A complete mapping of project workload slugs to
            live deployment UUIDs.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectReleaseCheckResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
