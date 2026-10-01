from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.runtime_policy_status_response import RuntimePolicyStatusResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    wait: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["wait"] = wait

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/policy/status".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RuntimePolicyStatusResponse | None:
    if response.status_code == 200:
        response_200 = RuntimePolicyStatusResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RuntimePolicyStatusResponse]:
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
    wait: str | Unset = UNSET,
) -> Response[Problem | RuntimePolicyStatusResponse]:
    """Check whether runtime policy changes have reached their serving consumers.

     Reports desired/applied positions for the gateway request envelope,
    gateway app-cache and traffic policy, edge rules, account CORS presets,
    explicit response-cache purges, app egress allowlists on nodes hosting
    live instances, and scheduler scaling-policy observation. The
    `request_policy` component covers app-row request settings such as
    request timeout and concurrency, and excludes deployment-traffic
    revisions. A response-cache purge is active only after every serving
    gateway has invalidated its local cache and optional shared tier. The
    top-level state and gateway counts remain the app-cache/traffic
    projection; use each named component for its own convergence state.
    `active` requires fresh observations from every relevant serving
    consumer. The egress allowlist is replayed from current app state by
    schedd if a notification is missed. Scheduler scaling `active` means
    the owning schedd loaded the policy, not that the replica target was
    reached. This does not attest host-level firewall policy or guest
    configuration.
    `unverified` means no revision or no relevant serving fleet can be observed.
    Gateway policy acknowledgements belong to the current process generation
    and require a fresh serving report. Replacement clears convergence until
    the new consumers replay. `traffic_runtime` reports compute gateway wiring
    separately; fresh wiring does not prove request enforcement. The optional
    `wait` applies to policy convergence only.

    Args:
        slug (str):
        wait (str | Unset):  Example: 5s.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RuntimePolicyStatusResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        wait=wait,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    wait: str | Unset = UNSET,
) -> Problem | RuntimePolicyStatusResponse | None:
    """Check whether runtime policy changes have reached their serving consumers.

     Reports desired/applied positions for the gateway request envelope,
    gateway app-cache and traffic policy, edge rules, account CORS presets,
    explicit response-cache purges, app egress allowlists on nodes hosting
    live instances, and scheduler scaling-policy observation. The
    `request_policy` component covers app-row request settings such as
    request timeout and concurrency, and excludes deployment-traffic
    revisions. A response-cache purge is active only after every serving
    gateway has invalidated its local cache and optional shared tier. The
    top-level state and gateway counts remain the app-cache/traffic
    projection; use each named component for its own convergence state.
    `active` requires fresh observations from every relevant serving
    consumer. The egress allowlist is replayed from current app state by
    schedd if a notification is missed. Scheduler scaling `active` means
    the owning schedd loaded the policy, not that the replica target was
    reached. This does not attest host-level firewall policy or guest
    configuration.
    `unverified` means no revision or no relevant serving fleet can be observed.
    Gateway policy acknowledgements belong to the current process generation
    and require a fresh serving report. Replacement clears convergence until
    the new consumers replay. `traffic_runtime` reports compute gateway wiring
    separately; fresh wiring does not prove request enforcement. The optional
    `wait` applies to policy convergence only.

    Args:
        slug (str):
        wait (str | Unset):  Example: 5s.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RuntimePolicyStatusResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        wait=wait,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    wait: str | Unset = UNSET,
) -> Response[Problem | RuntimePolicyStatusResponse]:
    """Check whether runtime policy changes have reached their serving consumers.

     Reports desired/applied positions for the gateway request envelope,
    gateway app-cache and traffic policy, edge rules, account CORS presets,
    explicit response-cache purges, app egress allowlists on nodes hosting
    live instances, and scheduler scaling-policy observation. The
    `request_policy` component covers app-row request settings such as
    request timeout and concurrency, and excludes deployment-traffic
    revisions. A response-cache purge is active only after every serving
    gateway has invalidated its local cache and optional shared tier. The
    top-level state and gateway counts remain the app-cache/traffic
    projection; use each named component for its own convergence state.
    `active` requires fresh observations from every relevant serving
    consumer. The egress allowlist is replayed from current app state by
    schedd if a notification is missed. Scheduler scaling `active` means
    the owning schedd loaded the policy, not that the replica target was
    reached. This does not attest host-level firewall policy or guest
    configuration.
    `unverified` means no revision or no relevant serving fleet can be observed.
    Gateway policy acknowledgements belong to the current process generation
    and require a fresh serving report. Replacement clears convergence until
    the new consumers replay. `traffic_runtime` reports compute gateway wiring
    separately; fresh wiring does not prove request enforcement. The optional
    `wait` applies to policy convergence only.

    Args:
        slug (str):
        wait (str | Unset):  Example: 5s.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RuntimePolicyStatusResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        wait=wait,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    wait: str | Unset = UNSET,
) -> Problem | RuntimePolicyStatusResponse | None:
    """Check whether runtime policy changes have reached their serving consumers.

     Reports desired/applied positions for the gateway request envelope,
    gateway app-cache and traffic policy, edge rules, account CORS presets,
    explicit response-cache purges, app egress allowlists on nodes hosting
    live instances, and scheduler scaling-policy observation. The
    `request_policy` component covers app-row request settings such as
    request timeout and concurrency, and excludes deployment-traffic
    revisions. A response-cache purge is active only after every serving
    gateway has invalidated its local cache and optional shared tier. The
    top-level state and gateway counts remain the app-cache/traffic
    projection; use each named component for its own convergence state.
    `active` requires fresh observations from every relevant serving
    consumer. The egress allowlist is replayed from current app state by
    schedd if a notification is missed. Scheduler scaling `active` means
    the owning schedd loaded the policy, not that the replica target was
    reached. This does not attest host-level firewall policy or guest
    configuration.
    `unverified` means no revision or no relevant serving fleet can be observed.
    Gateway policy acknowledgements belong to the current process generation
    and require a fresh serving report. Replacement clears convergence until
    the new consumers replay. `traffic_runtime` reports compute gateway wiring
    separately; fresh wiring does not prove request enforcement. The optional
    `wait` applies to policy convergence only.

    Args:
        slug (str):
        wait (str | Unset):  Example: 5s.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RuntimePolicyStatusResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            wait=wait,
        )
    ).parsed
