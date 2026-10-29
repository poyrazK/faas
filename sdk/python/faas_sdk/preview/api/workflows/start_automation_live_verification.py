from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.automation_live_verification import AutomationLiveVerification
from ...models.problem import Problem
from ...models.start_automation_live_verification_request import StartAutomationLiveVerificationRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: StartAutomationLiveVerificationRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/automations/{name}/live-verifications".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AutomationLiveVerification | Problem | None:
    if response.status_code == 201:
        response_201 = AutomationLiveVerification.from_dict(response.json())

        return response_201

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[AutomationLiveVerification | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: StartAutomationLiveVerificationRequest,
    idempotency_key: str,
) -> Response[AutomationLiveVerification | Problem]:
    """Explicitly execute one saved case input with atomic target fences and idempotency.

    Args:
        slug (str):
        name (str):
        idempotency_key (str):
        body (StartAutomationLiveVerificationRequest): Fenced request to execute one saved case
            input against a specific published revision and deployment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationLiveVerification | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: StartAutomationLiveVerificationRequest,
    idempotency_key: str,
) -> AutomationLiveVerification | Problem | None:
    """Explicitly execute one saved case input with atomic target fences and idempotency.

    Args:
        slug (str):
        name (str):
        idempotency_key (str):
        body (StartAutomationLiveVerificationRequest): Fenced request to execute one saved case
            input against a specific published revision and deployment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationLiveVerification | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: StartAutomationLiveVerificationRequest,
    idempotency_key: str,
) -> Response[AutomationLiveVerification | Problem]:
    """Explicitly execute one saved case input with atomic target fences and idempotency.

    Args:
        slug (str):
        name (str):
        idempotency_key (str):
        body (StartAutomationLiveVerificationRequest): Fenced request to execute one saved case
            input against a specific published revision and deployment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationLiveVerification | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: StartAutomationLiveVerificationRequest,
    idempotency_key: str,
) -> AutomationLiveVerification | Problem | None:
    """Explicitly execute one saved case input with atomic target fences and idempotency.

    Args:
        slug (str):
        name (str):
        idempotency_key (str):
        body (StartAutomationLiveVerificationRequest): Fenced request to execute one saved case
            input against a specific published revision and deployment.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationLiveVerification | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
