#!/usr/bin/env python3
"""Verify source-bound cross-host gateway transport measurements."""
from datetime import datetime, timedelta
import hashlib
import ipaddress
import json
import re
from urllib.parse import urlsplit

SOURCE = "pkg/fcvm/financial_pair_gateway_metal_test.go"
SUPPORT = (
    SOURCE, "pkg/fcvm/financial_pair_gateway_client_metal_test.go",
    "pkg/fcvm/financial_pair_rpc_metal_test.go", "pkg/fcvm/financial_pair_traffic_export_test.go",
    "pkg/fcvm/financial_compute_owner_metal_test.go", "pkg/fcvm/financial_compute_postgres_metal_test.go",
    "pkg/fcvm/financial_pair_helpers_metal_test.go", "pkg/e2etest/financialguest/main.go",
    "pkg/gateway/forwardproxy.go", "pkg/gateway/tcpforward.go", "pkg/gateway/udpforward.go",
    "pkg/vmmdgrpc/forward.go", "pkg/vmmdgrpc/forward_tcp.go", "pkg/vmmdgrpc/forward_udp.go",
    "pkg/vmmdgrpc/public_forward.go", "pkg/netns/public_forward.go", "pkg/netns/public_forward_linux.go",
    "pkg/netns/public_forward_policy.go", "pkg/wire/private_forward.go",
    "scripts/ci/financial-pair-runner.py", "scripts/ci/financial-pair-host.sh",
    "scripts/ci/financial-pair-traffic.py", "scripts/ci/financial-pair-evidence.py",
)
PROTOCOLS = {"http_stream", "custom_main", "sidecar_8080", "sidecar_9001", "h2c_grpc", "upgrade", "tcp", "udp"}
CASES = PROTOCOLS | {"exhaustion", "private_isolation", "deadline", "before_crash", "retired_attempt", "new_attempt"}
INTEGER_FIELDS = (
    "Payload", "Before", "After", "Funded", "RetryPayload", "StableAfter", "ReservedMillicents", "CommittedMillicents",
    "LimitMillicents", "EgressReserved", "EgressCommitted", "ReceiptRevision", "ReplayRevision", "UnconfirmedLeases", "InterfaceIndex",
)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def verify(root, output, hosts, fixture):
    selected = re.findall(r't\.Run\("([^"\n]+)"', (root / SOURCE).read_text())
    if set(selected) != CASES or len(selected) != len(CASES):
        raise ValueError("gateway traffic source must select the complete reviewed case matrix")
    measurements = {}
    for line in output.splitlines():
        if "financial-pair-traffic:" not in line:
            continue
        raw = line.split("financial-pair-traffic:", 1)[1]
        value = json.loads(raw)
        name = value["case"]
        if name not in CASES or name in measurements:
            raise ValueError("unknown or repeated gateway traffic measurement")
        for key in INTEGER_FIELDS:
            if type(value.get(key)) is not int or not 0 <= value[key] <= 2**63 - 1:
                raise ValueError(f"{name}: {key} is not a canonical bounded integer")
        for key in ("PhysicalAbsent", "DatabaseUnavailable", "CounterAvailable", "Settled"):
            if type(value.get(key)) is not bool:
                raise ValueError(f"{name}: missing physical/authority state")
        for key in ("Node", "Instance", "LeaseID"):
            if not re.fullmatch(r"[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}", value.get(key, "")):
                raise ValueError(f"{name}: missing native attempt identity")
        if not isinstance(value.get("Incarnation"), str) or not value["Incarnation"]:
            raise ValueError(f"{name}: missing owner incarnation")
        for key, node in (("gateway_host", "a"), ("owner_host", "b")):
            host = value[key]
            if not isinstance(host, dict):
                raise ValueError(f"{name}: invalid host facts")
            expected = hosts[node]["host"]
            if any(host.get(field) != expected.get(field) for field in ("machine_id_sha256", "boot_id_sha256")):
                raise ValueError(f"{name}: gateway/owner did not execute on the attested host/boot")
            if host.get("architecture") != "amd64" or host.get("root") is not True or host.get("kvm") is not True:
                raise ValueError(f"{name}: missing native host facts")
        if value["gateway_host"]["machine_id_sha256"] == value["owner_host"]["machine_id_sha256"] or value["gateway_host"]["boot_id_sha256"] == value["owner_host"]["boot_id_sha256"]:
            raise ValueError(f"{name}: gateway and owner share a host/boot")
        if not isinstance(value.get("RPCTarget"), str):
            raise ValueError(f"{name}: missing authenticated target")
        target = urlsplit(value["RPCTarget"])
        address = ipaddress.ip_address(target.hostname)
        if target.scheme != "tcp" or not target.port or target.path or str(address) != fixture["traffic_host_b"] or address.is_loopback or address.is_unspecified:
            raise ValueError(f"{name}: gateway did not dial the dedicated peer TCP target")
        if not 0 < value["Funded"] or value["ReservedMillicents"] + value["CommittedMillicents"] > value["LimitMillicents"]:
            raise ValueError(f"{name}: missing funded bytes or shared account ceiling exceeded")
        if value["After"] > value["Funded"] or value["Before"] > value["Funded"]:
            raise ValueError(f"{name}: interface bytes exceed issued authority")
        if any(not isinstance(value.get(key), str) for key in ("ObservedAt", "FundedUntil")):
            raise ValueError(f"{name}: missing timestamps")
        observed, until = (datetime.fromisoformat(value[key].replace("Z", "+00:00")) for key in ("ObservedAt", "FundedUntil"))
        if observed.tzinfo is None or until.tzinfo is None:
            raise ValueError(f"{name}: timestamps must identify UTC instants")
        absent = name in {"deadline", "retired_attempt"}
        if value["Settled"] != (name == "retired_attempt"):
            raise ValueError(f"{name}: live or uncertain authority was settled prematurely")
        if name != "retired_attempt" and (value["UnconfirmedLeases"] == 0 or value["EgressReserved"] < value["Funded"]):
            raise ValueError(f"{name}: outstanding funded authority was released")
        if value["PhysicalAbsent"] != absent or value["CounterAvailable"] == absent:
            raise ValueError(f"{name}: missing physical/counter proof")
        if value["DatabaseUnavailable"] != (name in {"exhaustion", "private_isolation", "deadline"}):
            raise ValueError(f"{name}: SQL partition state is wrong")
        if not absent:
            if not 0 < value["InterfaceIndex"] < 2**31 or not re.fullmatch(r"[A-Za-z0-9_.-]{1,15}", value.get("Veth", "")) or value["After"] < value["Before"]:
                raise ValueError(f"{name}: canonical meter retired or reset")
            delta = value["After"] - value["Before"]
            if value["Payload"] <= 0 or (name == "private_isolation" and delta != 0) or (name != "private_isolation" and delta < value["Payload"]):
                raise ValueError(f"{name}: private bytes billed or public response bypassed the meter")
            if name not in {"private_isolation", "exhaustion"} and delta >= 2 * value["Payload"]:
                raise ValueError(f"{name}: duplicated response accounting")
        elif value["Payload"] != 0 or value["After"] != 0:
            raise ValueError(f"{name}: retired authority delivered payload or retained its interface")
        if name == "exhaustion" and (value["RetryPayload"] != 0 or value["StableAfter"] != value["After"]):
            raise ValueError("kernel quota reopened after exhaustion")
        if name == "exhaustion":
            blocked = value.get("BlockedPayload")
            if not isinstance(blocked, dict) or set(blocked) != {"h2c_grpc", "upgrade", "tcp", "udp"} or any(type(amount) is not int or amount != 0 for amount in blocked.values()):
                raise ValueError("a public protocol reopened exhausted byte authority")
        if name == "deadline" and (value["Settled"] or observed > until + timedelta(seconds=1)):
            raise ValueError("deadline retired late or refunded uncertain authority")
        if name == "retired_attempt" and (not value["Settled"] or value["UnconfirmedLeases"] != 0 or value["EgressReserved"] != 0 or value["EgressCommitted"] != value["Funded"] or value["ReceiptRevision"] == 0 or value["ReceiptRevision"] != value["ReplayRevision"]):
            raise ValueError("retired credits were lost, released early or settled twice")
        measurements[name] = value
    if set(measurements) != CASES:
        raise ValueError("required cross-host gateway traffic measurements missing")
    for group in (PROTOCOLS, {"exhaustion", "private_isolation", "deadline"}, {"before_crash", "retired_attempt"}):
        if len({tuple(measurements[name][key] for key in ("Node", "Instance", "LeaseID")) for name in group}) != 1:
            raise ValueError("traffic phases changed their funded attempt")
    prior = None
    for name in (name for name in selected if name in PROTOCOLS):
        current = measurements[name]
        if prior and (current["Before"] < prior["After"] or current["InterfaceIndex"] != prior["InterfaceIndex"] or current["Veth"] != prior["Veth"]):
            raise ValueError("protocol matrix reset or replaced its canonical interface")
        prior = current
    exhaustion, private, deadline = (measurements[name] for name in ("exhaustion", "private_isolation", "deadline"))
    if exhaustion["After"] != private["Before"] or exhaustion["After"] != private["After"] or private["After"] != deadline["Before"]:
        raise ValueError("partitioned authority reopened between phases")
    old, retired, new = (measurements[name] for name in ("before_crash", "retired_attempt", "new_attempt"))
    if old["Incarnation"] == retired["Incarnation"] or new["Incarnation"] in {old["Incarnation"], retired["Incarnation"]} or new["Node"] != old["Node"] or new["Instance"] == old["Instance"] or new["LeaseID"] == old["LeaseID"] or new["InterfaceIndex"] == old["InterfaceIndex"]:
        raise ValueError("owner restart reused old physical or financial authority")
    return {"scope": "production_gateway_transports_to_remote_native_vmmd_rpc", "sources": {name: digest(root / name) for name in SUPPORT}, "measurements": measurements}
