from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_initial_recovery_review import WorkflowInitialRecoveryReview
from ...models.workflow_initial_recovery_review_decision_request import WorkflowInitialRecoveryReviewDecisionRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    review_id: UUID,
    *,
    body: WorkflowInitialRecoveryReviewDecisionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/initial-recovery-reviews/{review_id}/decision".format(
            slug=quote(str(slug), safe=""),
            review_id=quote(str(review_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowInitialRecoveryReview | None:
    if response.status_code == 200:
        response_200 = WorkflowInitialRecoveryReview.from_dict(response.json())

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

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowInitialRecoveryReview]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    review_id: UUID,
    *,
    client: AuthenticatedClient,
    body: WorkflowInitialRecoveryReviewDecisionRequest,
) -> Response[Problem | WorkflowInitialRecoveryReview]:
    """Submit or cancel a durable first-recovery review

     Submit or cancel a durable first-recovery review. Policy-version-fenced initial recovery.
    Preparation captures retained generation-zero evidence and a live safety preview. Submit repeats
    evidence, safety, cooldown and budget checks and atomically creates a native recovery job. Ready
    notifications use the existing delivery ledger. Reads never prepare or submit. Expiry and changed
    policy authority block submission and pending admission. Submission is explicit; no automatic first
    recovery is enabled.

    Args:
        slug (str):
        review_id (UUID):
        body (WorkflowInitialRecoveryReviewDecisionRequest): Decision and policy version for
            submitting an initial recovery review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowInitialRecoveryReview]
    """

    kwargs = _get_kwargs(
        slug=slug,
        review_id=review_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    review_id: UUID,
    *,
    client: AuthenticatedClient,
    body: WorkflowInitialRecoveryReviewDecisionRequest,
) -> Problem | WorkflowInitialRecoveryReview | None:
    """Submit or cancel a durable first-recovery review

     Submit or cancel a durable first-recovery review. Policy-version-fenced initial recovery.
    Preparation captures retained generation-zero evidence and a live safety preview. Submit repeats
    evidence, safety, cooldown and budget checks and atomically creates a native recovery job. Ready
    notifications use the existing delivery ledger. Reads never prepare or submit. Expiry and changed
    policy authority block submission and pending admission. Submission is explicit; no automatic first
    recovery is enabled.

    Args:
        slug (str):
        review_id (UUID):
        body (WorkflowInitialRecoveryReviewDecisionRequest): Decision and policy version for
            submitting an initial recovery review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowInitialRecoveryReview
    """

    return sync_detailed(
        slug=slug,
        review_id=review_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    review_id: UUID,
    *,
    client: AuthenticatedClient,
    body: WorkflowInitialRecoveryReviewDecisionRequest,
) -> Response[Problem | WorkflowInitialRecoveryReview]:
    """Submit or cancel a durable first-recovery review

     Submit or cancel a durable first-recovery review. Policy-version-fenced initial recovery.
    Preparation captures retained generation-zero evidence and a live safety preview. Submit repeats
    evidence, safety, cooldown and budget checks and atomically creates a native recovery job. Ready
    notifications use the existing delivery ledger. Reads never prepare or submit. Expiry and changed
    policy authority block submission and pending admission. Submission is explicit; no automatic first
    recovery is enabled.

    Args:
        slug (str):
        review_id (UUID):
        body (WorkflowInitialRecoveryReviewDecisionRequest): Decision and policy version for
            submitting an initial recovery review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowInitialRecoveryReview]
    """

    kwargs = _get_kwargs(
        slug=slug,
        review_id=review_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    review_id: UUID,
    *,
    client: AuthenticatedClient,
    body: WorkflowInitialRecoveryReviewDecisionRequest,
) -> Problem | WorkflowInitialRecoveryReview | None:
    """Submit or cancel a durable first-recovery review

     Submit or cancel a durable first-recovery review. Policy-version-fenced initial recovery.
    Preparation captures retained generation-zero evidence and a live safety preview. Submit repeats
    evidence, safety, cooldown and budget checks and atomically creates a native recovery job. Ready
    notifications use the existing delivery ledger. Reads never prepare or submit. Expiry and changed
    policy authority block submission and pending admission. Submission is explicit; no automatic first
    recovery is enabled.

    Args:
        slug (str):
        review_id (UUID):
        body (WorkflowInitialRecoveryReviewDecisionRequest): Decision and policy version for
            submitting an initial recovery review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowInitialRecoveryReview
    """

    return (
        await asyncio_detailed(
            slug=slug,
            review_id=review_id,
            client=client,
            body=body,
        )
    ).parsed
