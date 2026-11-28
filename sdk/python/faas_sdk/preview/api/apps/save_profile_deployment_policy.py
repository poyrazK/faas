from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.profile_deployment_policy import ProfileDeploymentPolicy
from ...models.save_profile_deployment_policy_request import SaveProfileDeploymentPolicyRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: SaveProfileDeploymentPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/profiles/deployment-policy".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProfileDeploymentPolicy | None:
    if response.status_code == 200:
        response_200 = ProfileDeploymentPolicy.from_dict(response.json())

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
) -> Response[Problem | ProfileDeploymentPolicy]:
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
    body: SaveProfileDeploymentPolicyRequest,
) -> Response[Problem | ProfileDeploymentPolicy]:
    """Configure CPU checks for future successful rollouts.

     Requires deploy-write access and the current policy revision; use zero for the initial save.
    Enabling requires a profiling plan and configured backend. Saving advances the revision and cancels
    pending checks; new completions use the new policy. The worker compares the previous successful
    deployment in the same environment and uses the selected runtime on both sides. Equal windows end at
    candidate creation for the baseline and start after rollout completion plus warm-up for the
    candidate. Retries preserve these windows, allow up to five attempts for late or insufficient data
    and remain inconclusive when data is absent. Deployment activation is independent of this background
    check.

    Args:
        slug (str):
        body (SaveProfileDeploymentPolicyRequest): Full replacement of the CPU rollout-check
            settings. Changing or disabling settings cancels queued and leased work for earlier policy
            revisions.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileDeploymentPolicy]
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
    body: SaveProfileDeploymentPolicyRequest,
) -> Problem | ProfileDeploymentPolicy | None:
    """Configure CPU checks for future successful rollouts.

     Requires deploy-write access and the current policy revision; use zero for the initial save.
    Enabling requires a profiling plan and configured backend. Saving advances the revision and cancels
    pending checks; new completions use the new policy. The worker compares the previous successful
    deployment in the same environment and uses the selected runtime on both sides. Equal windows end at
    candidate creation for the baseline and start after rollout completion plus warm-up for the
    candidate. Retries preserve these windows, allow up to five attempts for late or insufficient data
    and remain inconclusive when data is absent. Deployment activation is independent of this background
    check.

    Args:
        slug (str):
        body (SaveProfileDeploymentPolicyRequest): Full replacement of the CPU rollout-check
            settings. Changing or disabling settings cancels queued and leased work for earlier policy
            revisions.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileDeploymentPolicy
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
    body: SaveProfileDeploymentPolicyRequest,
) -> Response[Problem | ProfileDeploymentPolicy]:
    """Configure CPU checks for future successful rollouts.

     Requires deploy-write access and the current policy revision; use zero for the initial save.
    Enabling requires a profiling plan and configured backend. Saving advances the revision and cancels
    pending checks; new completions use the new policy. The worker compares the previous successful
    deployment in the same environment and uses the selected runtime on both sides. Equal windows end at
    candidate creation for the baseline and start after rollout completion plus warm-up for the
    candidate. Retries preserve these windows, allow up to five attempts for late or insufficient data
    and remain inconclusive when data is absent. Deployment activation is independent of this background
    check.

    Args:
        slug (str):
        body (SaveProfileDeploymentPolicyRequest): Full replacement of the CPU rollout-check
            settings. Changing or disabling settings cancels queued and leased work for earlier policy
            revisions.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileDeploymentPolicy]
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
    body: SaveProfileDeploymentPolicyRequest,
) -> Problem | ProfileDeploymentPolicy | None:
    """Configure CPU checks for future successful rollouts.

     Requires deploy-write access and the current policy revision; use zero for the initial save.
    Enabling requires a profiling plan and configured backend. Saving advances the revision and cancels
    pending checks; new completions use the new policy. The worker compares the previous successful
    deployment in the same environment and uses the selected runtime on both sides. Equal windows end at
    candidate creation for the baseline and start after rollout completion plus warm-up for the
    candidate. Retries preserve these windows, allow up to five attempts for late or insufficient data
    and remain inconclusive when data is absent. Deployment activation is independent of this background
    check.

    Args:
        slug (str):
        body (SaveProfileDeploymentPolicyRequest): Full replacement of the CPU rollout-check
            settings. Changing or disabling settings cancels queued and leased work for earlier policy
            revisions.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileDeploymentPolicy
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
