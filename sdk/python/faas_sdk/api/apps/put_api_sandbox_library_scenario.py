from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_library_scenario import APISandboxLibraryScenario
from ...models.api_sandbox_library_scenario_save_request import APISandboxLibraryScenarioSaveRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    scenario: UUID,
    *,
    body: APISandboxLibraryScenarioSaveRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/api-sandbox-scenarios/{scenario}".format(
            slug=quote(str(slug), safe=""),
            scenario=quote(str(scenario), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxLibraryScenario | Any | Problem | None:
    if response.status_code == 200:
        response_200 = APISandboxLibraryScenario.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

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
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxLibraryScenario | Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    scenario: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxLibraryScenarioSaveRequest,
) -> Response[APISandboxLibraryScenario | Any | Problem]:
    """Save a reusable sandbox scenario.

     Use a new UUID and an empty expected_revision to create, or the existing UUID and current revision
    to edit. A Debugger source request can be supplied when creating. Names are unique within the app.

    Args:
        slug (str):
        scenario (UUID):
        body (APISandboxLibraryScenarioSaveRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxLibraryScenario | Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scenario=scenario,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    scenario: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxLibraryScenarioSaveRequest,
) -> APISandboxLibraryScenario | Any | Problem | None:
    """Save a reusable sandbox scenario.

     Use a new UUID and an empty expected_revision to create, or the existing UUID and current revision
    to edit. A Debugger source request can be supplied when creating. Names are unique within the app.

    Args:
        slug (str):
        scenario (UUID):
        body (APISandboxLibraryScenarioSaveRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxLibraryScenario | Any | Problem
    """

    return sync_detailed(
        slug=slug,
        scenario=scenario,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    scenario: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxLibraryScenarioSaveRequest,
) -> Response[APISandboxLibraryScenario | Any | Problem]:
    """Save a reusable sandbox scenario.

     Use a new UUID and an empty expected_revision to create, or the existing UUID and current revision
    to edit. A Debugger source request can be supplied when creating. Names are unique within the app.

    Args:
        slug (str):
        scenario (UUID):
        body (APISandboxLibraryScenarioSaveRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxLibraryScenario | Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scenario=scenario,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    scenario: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: APISandboxLibraryScenarioSaveRequest,
) -> APISandboxLibraryScenario | Any | Problem | None:
    """Save a reusable sandbox scenario.

     Use a new UUID and an empty expected_revision to create, or the existing UUID and current revision
    to edit. A Debugger source request can be supplied when creating. Names are unique within the app.

    Args:
        slug (str):
        scenario (UUID):
        body (APISandboxLibraryScenarioSaveRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxLibraryScenario | Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            scenario=scenario,
            client=client,
            body=body,
        )
    ).parsed
