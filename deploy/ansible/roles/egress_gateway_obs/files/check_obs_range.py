#!/usr/bin/env python3
"""Fail if an address on this host, outside the obs interface, overlaps the
wg-obs range (ADR-20261117185346 amendment 2026-11-19)."""
import ipaddress
import json
import subprocess
import sys

net = ipaddress.ip_network(sys.argv[1])
iface = sys.argv[2]
links = json.loads(subprocess.check_output(["ip", "-j", "-4", "addr", "show"]))
for link in links:
    if link["ifname"] == iface:
        continue
    for addr in link.get("addr_info", []):
        have = ipaddress.ip_interface(f"{addr['local']}/{addr['prefixlen']}").network
        if have.overlaps(net):
            sys.exit(
                f"{link['ifname']} has {addr['local']}/{addr['prefixlen']}, inside {net}: "
                "set faas_gateway_obs_tunnel_cidr to a free range"
            )
