from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.simulate_automation_request import SimulateAutomationRequest
from ...models.simulate_automation_response import SimulateAutomationResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: SimulateAutomationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/automations:simulate".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SimulateAutomationResponse | None:
    if response.status_code == 200:
        response_200 = SimulateAutomationResponse.from_dict(response.json())

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
) -> Response[Problem | SimulateAutomationResponse]:
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
    body: SimulateAutomationRequest,
) -> Response[Problem | SimulateAutomationResponse]:
    r"""Simulate automation data flow with sample input and successful action mocks.

     Returns a deterministic hypothetical trace without saving a definition,
    creating runs, invoking handlers, publishing events or calling integrations.
    Requires ownership, MFA and read scope. Works without a live deployment
    or enabled workflow runtime. Definition validation uses the account plan
    and checks managed integration bindings without opening credentials.
    Missing action results block dependent steps. Waits remain unresolved;
    failure and timeout outcomes cannot be injected. Action mocks using the
    reserved exact {\"timeout\":true} output with an on_timeout route return
    400. Loop mocks form a
    sequential prefix. A complete trace means all roots resolved or skipped
    under the supplied successful mocks, not that live execution will succeed.
    Limits: 3 MiB request, 1 MiB definition and each sample value, 128 roots,
    1024 trace entries and 4 MiB response, plus existing loop bounds.
    Invalid definitions return 200 with definition_valid=false and no trace.

    Args:
        slug (str):
        body (SimulateAutomationRequest): Sample workflow data and mocked action, event/callback
            payload or timeout outcomes for a stateless simulation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SimulateAutomationResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SimulateAutomationRequest,
) -> Problem | SimulateAutomationResponse | None:
    r"""Simulate automation data flow with sample input and successful action mocks.

     Returns a deterministic hypothetical trace without saving a definition,
    creating runs, invoking handlers, publishing events or calling integrations.
    Requires ownership, MFA and read scope. Works without a live deployment
    or enabled workflow runtime. Definition validation uses the account plan
    and checks managed integration bindings without opening credentials.
    Missing action results block dependent steps. Waits remain unresolved;
    failure and timeout outcomes cannot be injected. Action mocks using the
    reserved exact {\"timeout\":true} output with an on_timeout route return
    400. Loop mocks form a
    sequential prefix. A complete trace means all roots resolved or skipped
    under the supplied successful mocks, not that live execution will succeed.
    Limits: 3 MiB request, 1 MiB definition and each sample value, 128 roots,
    1024 trace entries and 4 MiB response, plus existing loop bounds.
    Invalid definitions return 200 with definition_valid=false and no trace.

    Args:
        slug (str):
        body (SimulateAutomationRequest): Sample workflow data and mocked action, event/callback
            payload or timeout outcomes for a stateless simulation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SimulateAutomationResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SimulateAutomationRequest,
) -> Response[Problem | SimulateAutomationResponse]:
    r"""Simulate automation data flow with sample input and successful action mocks.

     Returns a deterministic hypothetical trace without saving a definition,
    creating runs, invoking handlers, publishing events or calling integrations.
    Requires ownership, MFA and read scope. Works without a live deployment
    or enabled workflow runtime. Definition validation uses the account plan
    and checks managed integration bindings without opening credentials.
    Missing action results block dependent steps. Waits remain unresolved;
    failure and timeout outcomes cannot be injected. Action mocks using the
    reserved exact {\"timeout\":true} output with an on_timeout route return
    400. Loop mocks form a
    sequential prefix. A complete trace means all roots resolved or skipped
    under the supplied successful mocks, not that live execution will succeed.
    Limits: 3 MiB request, 1 MiB definition and each sample value, 128 roots,
    1024 trace entries and 4 MiB response, plus existing loop bounds.
    Invalid definitions return 200 with definition_valid=false and no trace.

    Args:
        slug (str):
        body (SimulateAutomationRequest): Sample workflow data and mocked action, event/callback
            payload or timeout outcomes for a stateless simulation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SimulateAutomationResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: SimulateAutomationRequest,
) -> Problem | SimulateAutomationResponse | None:
    r"""Simulate automation data flow with sample input and successful action mocks.

     Returns a deterministic hypothetical trace without saving a definition,
    creating runs, invoking handlers, publishing events or calling integrations.
    Requires ownership, MFA and read scope. Works without a live deployment
    or enabled workflow runtime. Definition validation uses the account plan
    and checks managed integration bindings without opening credentials.
    Missing action results block dependent steps. Waits remain unresolved;
    failure and timeout outcomes cannot be injected. Action mocks using the
    reserved exact {\"timeout\":true} output with an on_timeout route return
    400. Loop mocks form a
    sequential prefix. A complete trace means all roots resolved or skipped
    under the supplied successful mocks, not that live execution will succeed.
    Limits: 3 MiB request, 1 MiB definition and each sample value, 128 roots,
    1024 trace entries and 4 MiB response, plus existing loop bounds.
    Invalid definitions return 200 with definition_valid=false and no trace.

    Args:
        slug (str):
        body (SimulateAutomationRequest): Sample workflow data and mocked action, event/callback
            payload or timeout outcomes for a stateless simulation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SimulateAutomationResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
