#!/bin/bash
# Direction check for the per-plan tbf cap (#4382), no VM needed.
# Two private namespaces stand in for an instance: G holds VethPeer (the
# instance side), H holds VethHost (the host side; in production it is the
# root namespace, which does not change what a root qdisc shapes). Applies
# the argv netns.Config.TcCommands produces (old: VethHost only; new:
# VethHost + VethPeer) and times a 24 MB TCP transfer in each direction.
set -uo pipefail
G=egqg$$; H=egqh$$; VH=egqh$$v; VP=egqp$$; HOST=10.250.77.1; PEER=10.250.77.2
cleanup() { ip netns del $G 2>/dev/null; ip netns del $H 2>/dev/null; }
trap cleanup EXIT
ip netns add $G; ip netns add $H
ip link add $VH type veth peer name $VP
ip link set $VH netns $H; ip link set $VP netns $G
ip netns exec $H ip addr add $HOST/30 dev $VH; ip netns exec $H ip link set $VH up
ip netns exec $G ip addr add $PEER/30 dev $VP; ip netns exec $G ip link set $VP up

RECV='import socket,sys,time
s=socket.socket();s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
s.bind((sys.argv[1],int(sys.argv[2])));s.listen(1);s.settimeout(15);c,_=s.accept();c.settimeout(60)
n=0;t=None
while True:
  b=c.recv(1<<16)
  if not b:break
  if t is None:t=time.time()
  n+=len(b)
d=time.time()-t;print("%.1f Mbit/s (%d bytes in %.2fs)"%(n*8/d/1e6,n,d))'
SEND='import socket,sys
s=socket.create_connection((sys.argv[1],int(sys.argv[2])),timeout=10);s.settimeout(60);b=b"x"*65536
for _ in range(384):s.sendall(b)
s.close()'

measure() { # label direction
  local port=$((20000 + RANDOM % 20000)) out
  if [ "$2" = egress ]; then # instance -> host: sender on VethPeer's side
    timeout 90 ip netns exec $H python3 -c "$RECV" $HOST $port > /tmp/egq-$$.out 2>&1 & sleep 0.5
    timeout 80 ip netns exec $G python3 -c "$SEND" $HOST $port
  else # host -> instance: receiver on VethPeer's side
    timeout 90 ip netns exec $G python3 -c "$RECV" $PEER $port > /tmp/egq-$$.out 2>&1 & sleep 0.5
    timeout 80 ip netns exec $H python3 -c "$SEND" $PEER $port
  fi
  wait; out=$(tail -1 /tmp/egq-$$.out); rm -f /tmp/egq-$$.out
  echo "$1 $2: $out"
}

measure "none" egress; measure "none" ingress
for MBIT in 25 250; do
  ip netns exec $H tc qdisc del dev $VH root 2>/dev/null; ip netns exec $G tc qdisc del dev $VP root 2>/dev/null
  # Before #4382's fix: TcCommands = [VethHost only].
  ip netns exec $H tc qdisc add dev $VH root tbf rate ${MBIT}mbit burst 32kbit latency 400ms
  measure "old ${MBIT}mbit" egress; measure "old ${MBIT}mbit" ingress
  # After: TcCommands also shapes VethPeer inside the instance namespace.
  ip netns exec $G tc qdisc add dev $VP root tbf rate ${MBIT}mbit burst 32kbit latency 400ms
  measure "new ${MBIT}mbit" egress; measure "new ${MBIT}mbit" ingress
done
