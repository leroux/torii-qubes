# Routing and egress

Code: proxy/internal/egress

The need this area answers is STK-261126-346B. It holds which way a
qube's traffic leaves: through its proxy's netvm or an egress of tailnet
exit nodes, how an exit node is judged healthy, the order of failover
and failback, and failing closed when none is up.

## SW-261127-K5TK An egress's traffic is dialled only into its identity's own network stack, never the system network

Status: active
Kind: invariant
Serves: STK-261126-346B

A connection on an egress's path, its UDP flows and the DNS of a qube
behind the egress are dialled into the userspace network stack of the
egress's identity, from the identity's own address; the program never
uses tsnet's `Server.Dial`, which falls back to the system network for
an address with no tailnet route, nor the proxy qube's netvm. DNS goes
over TCP, through the same stack, to the egress's resolver. Without an
exit node that stack has no route to the internet.

Why: failing closed must not depend on the selector being right; if the
only way out is through the exit node, a missing exit node is a refused
connection, never a direct one.

Scenarios:
1. `exit-down-no-local-dial` (agent, provisional): with the exit node
   gone offline, a qube's connection fails and nothing is dialled on the
   local network.
2. `udp-exit-down-no-local-dial` (agent, provisional): the same for a UDP
   flow.

## SW-261127-HJNX While an egress has no healthy exit node, its connections are refused at once and its DNS fails

Status: active
Kind: invariant
Serves: STK-261126-346B

Before dialling, the program refuses a connection on an egress's path
while the egress has no healthy exit node, with `egress-no-healthy-exit`
and the egress's identity, and dials nothing. A DNS question that would
be answered through the egress gets SERVFAIL, not "no such name", and is
asked nowhere.

Why: refusing at once tells the qube there is no way out instead of
letting it wait for a dial that cannot succeed; SERVFAIL keeps a
resolver from caching that a name does not exist.

Scenarios:
1. `refused-at-once` (agent, provisional): with no exit node offering,
   a connection is refused with `egress-no-healthy-exit` and nothing is
   dialled.
2. `dns-servfail` (agent, provisional): a DNS question from the qube then
   gets SERVFAIL.

## SW-261127-6YGC An egress's exit node is never cleared: a switch is one change from the old node to the new

Status: active
Kind: invariant
Serves: STK-261126-346B

The selector sets an exit node only by naming the new one, in one
change of the identity's preferences that also keeps LAN access through
the exit node off; it never clears it. With no exit node healthy, the
last one stays set and the egress reports itself unusable.

Why: for a stock Tailscale client, clearing the exit node is exactly the
switch back to direct; never clearing it leaves no moment, however
brief, in which the identity would route the internet itself.

Scenarios:
1. `last-kept-when-none-healthy` (agent, provisional): with every exit
   node down, the active one stays set and the egress is not usable.

## SW-261127-BNFC Exit nodes are used in the file's order, failing over at once and back only after a hold-down

Status: active
Kind: invariant
Serves: STK-261126-346B

An egress uses the first of its exit nodes that is healthy. When the
active one stops being healthy it moves at once to the first healthy
one; it moves back to one earlier in the list only once that one has
stayed healthy for `failback-after`, two minutes unless the file says
otherwise. With none healthy before, the first to come back is used at
once.

Why: the order is the owner's preference, a home exit node before a
cloud one; failing back only after a hold-down keeps a flapping node
from moving the qubes' public address every minute.

Scenarios:
1. `first-healthy-in-order` (agent, provisional): with the first exit
   node healthy, it is used; with it down and the second up, the second.
2. `failback-after-hold-down` (agent, provisional): the first comes back
   and is used only once it has been healthy for `failback-after`.

## SW-261127-8DFY An exit node is healthy while listed, answering pings, and not failing every real connection through it

Status: active
Kind: bound
Serves: STK-261126-346B

Every 10 seconds, each check bounded to 5, every exit node is checked:
it is healthy while the tailnet map lists it online and offering an
exit node, and fewer than 3 pings in a row have gone unanswered. The
active one is also marked down for the hold-down when, within 30
seconds, 3 or more distinct destinations failed through it and none
succeeded; and, when the egress names a `probe`, when that probe failed
3 times in a row. No probe runs unless the file names one.

Why: the map lags by tens of seconds and a ping does not prove traffic
flows; counting distinct destinations keeps one dead site from tripping
it, and a default probe would send a recognisable connection out of
every exit node.

Scenarios:
1. `withdrawn-route-down` (agent, provisional): a node online and
   answering pings but no longer offering an exit node is down.
2. `missed-pings-down` (agent, provisional): three pings in a row
   unanswered take a node down; one or two do not.
3. `passive-trip` (agent, provisional): three distinct destinations
   failing within 30 seconds, with no success, trip the active node; two,
   or one failing three times, do not.
4. `probe-trips` (agent, provisional): a named probe failing three times
   in a row trips the active node; with no probe named, none runs.

## SW-261127-KYZW A qube's LAN traffic never goes through its egress, and plan says so

Status: active
Kind: invariant
Serves: STK-261126-346B

On an egress, a qube's connections to the LAN zone go directly through
its proxy's netvm; only the internet zone takes the exit path. plan
notes each qube with both an egress and LAN access, since its LAN
traffic is not hidden by the exit node.

Why: an exit node is on the tailnet, not this machine's network; the
LAN can only be reached from here, and an owner who routes a qube
through an exit node must be told that part of its traffic is not.

Scenarios:
1. `lan-direct` (agent, provisional): an allowed LAN address of a qube on
   an egress takes the direct path, an internet address the exit path.
2. `lan-noted` (agent, provisional): plan notes a qube on an egress that
   may reach the LAN.
