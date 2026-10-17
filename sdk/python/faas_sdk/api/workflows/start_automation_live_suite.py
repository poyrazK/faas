from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.automation_live_suite import AutomationLiveSuite
from ...models.problem import Problem
from ...models.start_automation_live_suite_request import StartAutomationLiveSuiteRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    body: StartAutomationLiveSuiteRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/automations/{name}/live-suites/{suite_id}/start".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
            suite_id=quote(str(suite_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AutomationLiveSuite | Problem | None:
    if response.status_code == 200:
        response_200 = AutomationLiveSuite.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[AutomationLiveSuite | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: StartAutomationLiveSuiteRequest,
) -> Response[AutomationLiveSuite | Problem]:
    """Start a durable live suite with explicit real-action confirmation.

     Creates an immutable ordered selection and admits its first case atomically with the saved run link.
    Retry this same suite ID and body after a lost response. A retained start replay only reads
    progress; it never advances later cases. New IDs must be UUIDv7 generated within the last ten
    minutes. Existing metadata-only suites cannot be taken over.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):
        body (StartAutomationLiveSuiteRequest): At most 16 KiB. Bind an ordered selection of saved
            live case names to the exact saved version, published revision and deployment, with
            explicit real-action authorization.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationLiveSuite | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        suite_id=suite_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: StartAutomationLiveSuiteRequest,
) -> AutomationLiveSuite | Problem | None:
    """Start a durable live suite with explicit real-action confirmation.

     Creates an immutable ordered selection and admits its first case atomically with the saved run link.
    Retry this same suite ID and body after a lost response. A retained start replay only reads
    progress; it never advances later cases. New IDs must be UUIDv7 generated within the last ten
    minutes. Existing metadata-only suites cannot be taken over.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):
        body (StartAutomationLiveSuiteRequest): At most 16 KiB. Bind an ordered selection of saved
            live case names to the exact saved version, published revision and deployment, with
            explicit real-action authorization.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationLiveSuite | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        suite_id=suite_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: StartAutomationLiveSuiteRequest,
) -> Response[AutomationLiveSuite | Problem]:
    """Start a durable live suite with explicit real-action confirmation.

     Creates an immutable ordered selection and admits its first case atomically with the saved run link.
    Retry this same suite ID and body after a lost response. A retained start replay only reads
    progress; it never advances later cases. New IDs must be UUIDv7 generated within the last ten
    minutes. Existing metadata-only suites cannot be taken over.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):
        body (StartAutomationLiveSuiteRequest): At most 16 KiB. Bind an ordered selection of saved
            live case names to the exact saved version, published revision and deployment, with
            explicit real-action authorization.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationLiveSuite | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        suite_id=suite_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: StartAutomationLiveSuiteRequest,
) -> AutomationLiveSuite | Problem | None:
    """Start a durable live suite with explicit real-action confirmation.

     Creates an immutable ordered selection and admits its first case atomically with the saved run link.
    Retry this same suite ID and body after a lost response. A retained start replay only reads
    progress; it never advances later cases. New IDs must be UUIDv7 generated within the last ten
    minutes. Existing metadata-only suites cannot be taken over.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):
        body (StartAutomationLiveSuiteRequest): At most 16 KiB. Bind an ordered selection of saved
            live case names to the exact saved version, published revision and deployment, with
            explicit real-action authorization.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationLiveSuite | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            suite_id=suite_id,
            client=client,
            body=body,
        )
    ).parsed
