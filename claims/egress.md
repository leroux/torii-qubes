# Routing and egress

Code: proxy/internal/egress

This area answers STK-261126-346B. It covers which way a qube's traffic
leaves, through its proxy's netvm or through an egress of tailnet exit
nodes; how the proxy judges an exit node healthy; the order of failover
and failback; and how an egress fails closed when no exit node is up.

## SW-261127-K5TK The proxy MUST dial an egress's traffic only into the egress identity's own network stack, never the system network

Status: active
Kind: invariant
Serves: STK-261126-346B

- The program MUST dial a connection on an egress's path, its UDP flows,
  and the DNS of a qube behind the egress into the userspace network
  stack of the egress's identity, from the identity's own address.
- The program MUST NOT use tsnet's `Server.Dial`, which falls back to the
  system network for an address with no tailnet route, and MUST NOT use
  the proxy qube's netvm for this traffic.
- DNS MUST go over TCP, through the same stack, to the egress's
  resolver. A name in the proxy qube's own hosts file is answered from
  it, and the proxy qube's search domains MAY be added to a query.
- Without an exit node, that stack has no route to the internet.

Why: failing closed must not depend on the selector being right. If the
only way out of the egress identity's stack is through an exit node, a
missing or dead exit node makes a connection fail instead of turning it
into a direct one, even when the selector misjudges a node's health.

Scenarios:
1. `exit-down-no-local-dial` (agent, provisional): given the exit node
   gone offline, when a qube on the egress connects, then the connection
   fails and nothing is dialled on the local network.
2. `udp-exit-down-no-local-dial` (agent, provisional): given the exit
   node gone offline, when a qube on the egress sends UDP, then the flow
   fails and nothing is dialled on the local network.

Changes:
- 2026-11-27: the resolver reads the proxy qube's hosts file and search
  domains, which the text did not say; DialUDP is anchored.

## SW-261127-HJNX The proxy MUST refuse an egress's connections at once, and fail its DNS, while the egress has no healthy exit node

Status: active
Kind: invariant
Serves: STK-261126-346B

- While the egress has no healthy exit node, the program MUST refuse a
  connection on the egress's path before dialling, with
  `egress-no-healthy-exit` and the egress's identity, and MUST dial
  nothing.
- A DNS query that would be answered through the egress MUST get
  SERVFAIL, not "no such name", and MUST NOT be sent anywhere.

Why: refusing at once tells the qube there is no way out, instead of
leaving it to wait for a dial that cannot succeed. Answering SERVFAIL
rather than NXDOMAIN matters because resolvers cache "no such name", and
a cached NXDOMAIN would keep breaking the name after the exit node
recovers.

Scenarios:
1. `refused-at-once` (agent, provisional): given no exit node offering,
   when a qube on the egress connects, then the connection is refused
   with `egress-no-healthy-exit` and nothing is dialled.
2. `dns-servfail` (agent, provisional): given no exit node offering,
   when the qube sends a DNS query, then it gets SERVFAIL.

## SW-261127-6YGC The egress selector MUST NOT clear an exit node, and MUST switch in one change from the old node to the new

Status: active
Kind: invariant
Serves: STK-261126-346B

The selector sets an exit node only by naming the new one, in a single
change of the identity's preferences that also keeps LAN access through
the exit node off, and it MUST NOT clear the exit node. With no exit node
healthy, the last one stays set and the egress reports itself unusable.

Why: for a stock Tailscale client, clearing the exit node is exactly how
it switches back to direct internet access. Switching in one change, and
never clearing, leaves no moment, however brief, in which the identity
would route the internet itself.

Scenarios:
1. `last-kept-when-none-healthy` (agent, provisional): given every exit
   node down, when the selector runs, then the active node stays set and
   the egress is not usable.

## SW-261127-BNFC The egress selector MUST use exit nodes in the file's order, failing over at once and back only after a hold-down

Status: active
Kind: invariant
Serves: STK-261126-346B

- An egress MUST use the first of its exit nodes that is healthy.
- When the active node stops being healthy, the egress MUST move at once
  to the first healthy one.
- The egress MUST move back to a node earlier in the list only once that
  node has stayed healthy for `failback-after`, two minutes unless the
  file says otherwise.
- When no node was healthy before, the first to come back MUST be used
  at once.

Why: the order expresses the owner's preference, for example a home
exit node before a cloud one. Failing back only after a hold-down keeps a
node that keeps dropping and returning from moving the qubes' public
address every minute.

Scenarios:
1. `first-healthy-in-order` (agent, provisional): given the first exit
   node healthy, when a node is chosen, then it is the first; given the
   first down and the second up, then it is the second.
2. `failback-after-hold-down` (agent, provisional): given the first node
   back after an outage, when the selector runs, then it moves back only
   once the first has been healthy for `failback-after`.
3. `default-two-minutes` (agent, provisional): given an egress that names
   no `failback-after`, when the file is read and the config built, then
   the hold-down is 120 seconds in both.

Changes:
- 2026-11-27: a scenario holds the two-minute default to its number on
  both sides.

## SW-261127-8DFY The egress selector MUST judge an exit node healthy only while it is listed, answers pings, and is not failing every real connection

Status: active
Kind: bound
Serves: STK-261126-346B

- Every 10 seconds, the selector MUST check every exit node, each check
  bounded to 5 seconds.
- A node MUST count as healthy while the tailnet map lists it online and
  offering an exit node, and fewer than 3 pings in a row have gone
  unanswered.
- The active node MUST also be marked down for the hold-down when, within
  30 seconds, 3 or more distinct destinations failed through it and none
  succeeded; and, when the egress names a `probe`, when that probe failed
  3 times in a row.
- The selector MUST NOT run a probe unless the file names one.

Why: each signal covers a gap in another. The tailnet map lags by tens of
seconds, and a ping proves the node is reachable but not that traffic
flows through it, so real connections are counted too. Counting distinct
destinations keeps one dead site from tripping the node, and probing
only when asked avoids sending a recognisable connection out of every
exit node by default.

Scenarios:
1. `withdrawn-route-down` (agent, provisional): given a node online and
   answering pings, when it stops offering an exit node, then it is down.
2. `missed-pings-down` (agent, provisional): given a node, when three
   pings in a row go unanswered, then it is down; when one or two do,
   then it is not.
3. `passive-trip` (agent, provisional): given the active node, when three
   distinct destinations fail through it within 30 seconds with no
   success, then it trips; when two fail, or one fails three times, then
   it does not.
4. `probe-trips` (agent, provisional): given a named probe, when it fails
   three times in a row, then the active node trips; given no probe named,
   then none runs.
5. `cadence` (agent, provisional): given a running selector, when time
   passes, then checks run every 10 seconds, each bounded to 5.

Changes:
- 2026-11-27: a scenario holds the cadence to its numbers; Run and the
  probe's wiring in Engine.Start are anchored.

## SW-261127-KYZW The proxy MUST send a qube's LAN traffic directly, never through its egress, and torii plan MUST say so

Status: active
Kind: invariant
Serves: STK-261126-346B

- On an egress, a qube's connections to the LAN zone MUST go directly
  through its proxy's netvm; only the internet zone takes the exit path.
- `torii plan` MUST note each qube that has both an egress and LAN
  access, since the exit node does not hide its LAN traffic.

Why: an exit node sits on the tailnet, not on this machine's network, so
the LAN can only be reached from here. An owner who routes a qube through
an exit node to hide where it is must be told that part of its traffic
does not go that way.

Scenarios:
1. `lan-direct` (agent, provisional): given a qube on an egress, when it
   connects to an allowed LAN address and to an internet address, then
   the first takes the direct path and the second the exit path.
2. `lan-noted` (agent, provisional): given qubes with and without an
   egress and LAN access, when plan runs, then it notes the qube on an
   egress that may reach the LAN, and no qube without both.

Changes:
- 2026-11-27: lan-noted also holds that a qube without an egress, or
  without LAN access, is not noted.
