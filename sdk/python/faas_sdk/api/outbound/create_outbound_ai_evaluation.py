from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_outbound_ai_evaluation_request import CreateOutboundAIEvaluationRequest
from ...models.outbound_ai_evaluation import OutboundAIEvaluation
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    route: UUID,
    *,
    body: CreateOutboundAIEvaluationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/outbound/ai-routes/{route}/evaluations".format(
            route=quote(str(route), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundAIEvaluation | Problem | None:
    if response.status_code == 202:
        response_202 = OutboundAIEvaluation.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

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
) -> Response[OutboundAIEvaluation | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    route: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIEvaluationRequest,
) -> Response[OutboundAIEvaluation | Problem]:
    """Evaluate an AI route target before a canary.

     Requires MFA and deploy-write scope. outboundd replays the route's first target's latest captured
    OpenAI chat completions and Responses calls on the target and asks the judge model which answer is
    better, in a random order (ADR-1431). The evaluation passes when losses and candidate errors are at
    most max_loss_rate percent of the sample; a passing evaluation starts its canary, if it has one,
    unless the route already has an active canary or the target changed. Candidate and judge calls are
    metered and capped under the caller evaluation. The first target's integration needs
    request_log_content.

    Args:
        route (UUID):
        body (CreateOutboundAIEvaluationRequest): Evaluate a route target against the route's
            first target. Omitted settings take their defaults: a sample of 20 and a max_loss_rate of
            20. canary, when set, must name the same target and starts if the evaluation passes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIEvaluation | Problem]
    """

    kwargs = _get_kwargs(
        route=route,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    route: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIEvaluationRequest,
) -> OutboundAIEvaluation | Problem | None:
    """Evaluate an AI route target before a canary.

     Requires MFA and deploy-write scope. outboundd replays the route's first target's latest captured
    OpenAI chat completions and Responses calls on the target and asks the judge model which answer is
    better, in a random order (ADR-1431). The evaluation passes when losses and candidate errors are at
    most max_loss_rate percent of the sample; a passing evaluation starts its canary, if it has one,
    unless the route already has an active canary or the target changed. Candidate and judge calls are
    metered and capped under the caller evaluation. The first target's integration needs
    request_log_content.

    Args:
        route (UUID):
        body (CreateOutboundAIEvaluationRequest): Evaluate a route target against the route's
            first target. Omitted settings take their defaults: a sample of 20 and a max_loss_rate of
            20. canary, when set, must name the same target and starts if the evaluation passes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIEvaluation | Problem
    """

    return sync_detailed(
        route=route,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    route: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIEvaluationRequest,
) -> Response[OutboundAIEvaluation | Problem]:
    """Evaluate an AI route target before a canary.

     Requires MFA and deploy-write scope. outboundd replays the route's first target's latest captured
    OpenAI chat completions and Responses calls on the target and asks the judge model which answer is
    better, in a random order (ADR-1431). The evaluation passes when losses and candidate errors are at
    most max_loss_rate percent of the sample; a passing evaluation starts its canary, if it has one,
    unless the route already has an active canary or the target changed. Candidate and judge calls are
    metered and capped under the caller evaluation. The first target's integration needs
    request_log_content.

    Args:
        route (UUID):
        body (CreateOutboundAIEvaluationRequest): Evaluate a route target against the route's
            first target. Omitted settings take their defaults: a sample of 20 and a max_loss_rate of
            20. canary, when set, must name the same target and starts if the evaluation passes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIEvaluation | Problem]
    """

    kwargs = _get_kwargs(
        route=route,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    route: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIEvaluationRequest,
) -> OutboundAIEvaluation | Problem | None:
    """Evaluate an AI route target before a canary.

     Requires MFA and deploy-write scope. outboundd replays the route's first target's latest captured
    OpenAI chat completions and Responses calls on the target and asks the judge model which answer is
    better, in a random order (ADR-1431). The evaluation passes when losses and candidate errors are at
    most max_loss_rate percent of the sample; a passing evaluation starts its canary, if it has one,
    unless the route already has an active canary or the target changed. Candidate and judge calls are
    metered and capped under the caller evaluation. The first target's integration needs
    request_log_content.

    Args:
        route (UUID):
        body (CreateOutboundAIEvaluationRequest): Evaluate a route target against the route's
            first target. Omitted settings take their defaults: a sample of 20 and a max_loss_rate of
            20. canary, when set, must name the same target and starts if the evaluation passes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIEvaluation | Problem
    """

    return (
        await asyncio_detailed(
            route=route,
            client=client,
            body=body,
        )
    ).parsed
