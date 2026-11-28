from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_outbound_ai_prompt_evaluation_request import CreateOutboundAIPromptEvaluationRequest
from ...models.outbound_ai_prompt_evaluation import OutboundAIPromptEvaluation
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    prompt: str,
    *,
    body: CreateOutboundAIPromptEvaluationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/outbound/ai-prompts/{prompt}/evaluations".format(
            prompt=quote(str(prompt), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundAIPromptEvaluation | Problem | None:
    if response.status_code == 202:
        response_202 = OutboundAIPromptEvaluation.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OutboundAIPromptEvaluation | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    prompt: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIPromptEvaluationRequest,
) -> Response[OutboundAIPromptEvaluation | Problem]:
    """Evaluate a managed AI prompt version.

     Requires MFA and deploy-write scope. The outbound gateway renders the version and the live version
    for each case's variables, calls each version's target, and asks the judge model which answer is
    better, in a random order. It passes when losses and candidate errors are at most max_loss_rate
    percent of the cases; a passing evaluation starts its canary, if it has one, while the baseline is
    still live and the prompt has no active canary. Calls are metered and capped under the caller
    evaluation.

    Args:
        prompt (str):
        body (CreateOutboundAIPromptEvaluationRequest): A candidate version, the variable sets to
            run it on, and the judge model.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIPromptEvaluation | Problem]
    """

    kwargs = _get_kwargs(
        prompt=prompt,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    prompt: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIPromptEvaluationRequest,
) -> OutboundAIPromptEvaluation | Problem | None:
    """Evaluate a managed AI prompt version.

     Requires MFA and deploy-write scope. The outbound gateway renders the version and the live version
    for each case's variables, calls each version's target, and asks the judge model which answer is
    better, in a random order. It passes when losses and candidate errors are at most max_loss_rate
    percent of the cases; a passing evaluation starts its canary, if it has one, while the baseline is
    still live and the prompt has no active canary. Calls are metered and capped under the caller
    evaluation.

    Args:
        prompt (str):
        body (CreateOutboundAIPromptEvaluationRequest): A candidate version, the variable sets to
            run it on, and the judge model.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIPromptEvaluation | Problem
    """

    return sync_detailed(
        prompt=prompt,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    prompt: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIPromptEvaluationRequest,
) -> Response[OutboundAIPromptEvaluation | Problem]:
    """Evaluate a managed AI prompt version.

     Requires MFA and deploy-write scope. The outbound gateway renders the version and the live version
    for each case's variables, calls each version's target, and asks the judge model which answer is
    better, in a random order. It passes when losses and candidate errors are at most max_loss_rate
    percent of the cases; a passing evaluation starts its canary, if it has one, while the baseline is
    still live and the prompt has no active canary. Calls are metered and capped under the caller
    evaluation.

    Args:
        prompt (str):
        body (CreateOutboundAIPromptEvaluationRequest): A candidate version, the variable sets to
            run it on, and the judge model.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIPromptEvaluation | Problem]
    """

    kwargs = _get_kwargs(
        prompt=prompt,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    prompt: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIPromptEvaluationRequest,
) -> OutboundAIPromptEvaluation | Problem | None:
    """Evaluate a managed AI prompt version.

     Requires MFA and deploy-write scope. The outbound gateway renders the version and the live version
    for each case's variables, calls each version's target, and asks the judge model which answer is
    better, in a random order. It passes when losses and candidate errors are at most max_loss_rate
    percent of the cases; a passing evaluation starts its canary, if it has one, while the baseline is
    still live and the prompt has no active canary. Calls are metered and capped under the caller
    evaluation.

    Args:
        prompt (str):
        body (CreateOutboundAIPromptEvaluationRequest): A candidate version, the variable sets to
            run it on, and the judge model.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIPromptEvaluation | Problem
    """

    return (
        await asyncio_detailed(
            prompt=prompt,
            client=client,
            body=body,
        )
    ).parsed
