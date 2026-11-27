# Reaching the proxy

Code: proxy/internal/qrexec, proxy/internal/intercept, proxy/internal/socks5, proxy/cmd/torii-client, packaging/proxy

The needs this area answers are STK-261126-KZK8 and STK-261126-SM33. It
holds how a carried qube's connection reaches the proxy program and how
the program knows which qube sent it: qrexec calls in the direction of
the connection for a qube with no netvm, interception with kernel
forwarding off for a routed qube, the boot-time guard that keeps
forwarding off, and the client's ports in the qube.

## SW-261127-M36P Every qrexec call runs in the direction of the connection it carries, and only the declared ones are allowed

Status: active
Kind: invariant
Serves: STK-261126-KZK8, STK-261126-SM33

Outbound, a carried qube with no netvm calls `torii.Connect` in its own
proxy, with `+socks`, `+http` or `+f.<forward>`, and only those its entry
declares. Inbound, the proxy calls `qubes.ConnectTCP` into a carried qube,
routed or not, only on the target ports its entry or a service lists.
Between qubes on this machine, `qubes.ConnectTCP` is allowed only from the
qubes `local-inbound` names, on its port. A qube that declares no
outbound access is allowed no `torii.Connect` call, and calls
`qubes.ConnectTCP` only where another qube's `local-inbound` names it.
torii's deny file, read after every mesh's own, denies `torii.Connect`
and `torii.Status` to every caller the allows do not name;
`qubes.ConnectTCP` beyond them is left to Qubes' own policy (observation
QubesConnectTCPDenied). A hand-written policy file read before torii's
can widen any of these, and check names it (STK-261126-4Q75).

Why: a call that runs against the connection, such as a tunnel the
qube holds open to its proxy, would give every qube a standing channel
out and put a tunnel protocol inside the offline qube; a qube that only
answers can then be reached, and reach out, only as its entry says.

Scenarios:
1. `outbound-own-proxy-only` (agent, provisional): a qube declaring
   `socks` is allowed `torii.Connect+socks` to its own proxy and to no
   other qube.
2. `inbound-listed-ports` (agent, provisional): the proxy is allowed
   `qubes.ConnectTCP` into a qube on each listed target port and on no
   other.
3. `silent-qube-no-calls` (agent, provisional): a qube with no outbound
   access has no `torii.Connect` allow line, and as a caller only the
   `qubes.ConnectTCP` lines another qube's `local-inbound` gives it.
4. `others-denied` (agent, provisional): the deny file denies
   `torii.Connect` from any qube to any qube.

Changes:
- 2026-11-27: a qube with no outbound access may still call
  `qubes.ConnectTCP` where `local-inbound` names it, and the deny file
  covers torii's own services only, `qubes.ConnectTCP` resting on Qubes'
  policy; the text claimed no call at all and a deny it does not write.

## SW-261127-7C3Y A qrexec caller is the qube dom0 names, never one the caller states

Status: active
Kind: invariant
Serves: STK-261126-KZK8

The program takes a `torii.Connect` caller's name and argument from what
qrexec sets, the header qrexec-agent writes or the environment it gives
the service; nothing the caller sends is read as either. A name or
argument outside the allowed forms is refused before the call reaches
its handler. A caller the file does not list, or one
listed as routed, is refused; an argument other than `socks`, `http` or
`f.<forward>` is refused.

Why: deciding against a qube's entry is only sound if the qube cannot
choose whose entry; dom0 names the caller, and nothing the caller sends
is read as its name.

Scenarios:
1. `caller-from-qrexec` (agent, provisional): the program takes as the
   caller the qube in `QREXEC_REMOTE_DOMAIN`, and a forged header in the
   connection's bytes reaches it as data.
2. `bad-name-refused` (agent, provisional): a caller or argument that is
   not of the allowed form never reaches the program.
3. `routed-caller-refused` (agent, provisional): a `torii.Connect` call
   from a qube listed as routed is refused with `wrong-entry`.

Changes:
- 2026-11-27: a bad name is refused before the handler, in the program on
  the socket path, not before the program; the scenario says what its test
  shows.

## SW-261127-BHHX A routed qube is named by its Qubes source address, which Qubes keeps it from forging

Status: active
Kind: invariant
Serves: STK-261126-KZK8

A connection or datagram arriving from a routed qube is decided against
the entry whose Qubes address is its source address. An address no
listed qube has is the proxy's unlisted source, decided by its
`unlisted` policy, or refused when that policy is absent. The proxy
trusts the source address because Qubes drops packets on a netvm's
`vif` interfaces whose source is not the qube's own address
(observation QubesAntispoof).

Why: a routed qube has no other name the proxy can read; the source
address is that name only because Qubes, not the qube, guarantees it.

Scenarios:
1. `source-names-qube` (agent, provisional): a connection from 10.137.0.20
   is decided against the entry with that address.
2. `unknown-address-unlisted` (agent, provisional): a connection from an
   address no entry has is refused when the proxy has no `unlisted`
   policy.

## SW-261127-B6Z6 A routed qube's traffic reaches the program and nothing else: interception, with forwarding off

Status: active
Kind: invariant
Serves: STK-261126-KZK8, STK-261126-SM33

On a proxy qube, every TCP connection and every DNS query arriving from
a `vif` interface is redirected to the program, and every other IPv4
UDP datagram is handed to it keeping its destination. Kernel forwarding
is turned off, for IPv4 and IPv6, and the forward chain drops
everything. Input from a `vif` interface is accepted only on the
program's ports, for what was handed to it, for ICMP, and for
connections already established; everything else is dropped. A TCP
connection to any other port of the proxy qube is redirected like any
other, and the program refuses it as the proxy's own address.

Why: the program can only decide what it is given; redirecting
everything to it, and forwarding nothing, makes a connection it did not
relay impossible rather than merely refused.

Scenarios:
1. `all-redirected` (agent, provisional): the proxy's rules redirect TCP
   and DNS from `vif*` to the program's ports and hand it other IPv4 UDP.
2. `forwarding-off` (agent, provisional): once the program has started,
   the proxy qube's `ip_forward` is 0 and a forwarded packet is dropped.
3. `input-closed` (agent, provisional): input from the workloads'
   interfaces accepts exactly the program's ports, what was handed to
   it, ICMP and established connections, and drops the rest.

Changes:
- 2026-11-27: TCP to another port of the proxy qube is redirected and
  refused by the program, not dropped, which the rules say themselves;
  input-closed names the exact rules its test checks.

## SW-261127-JWK2 A guard loaded at boot keeps the proxy qube forwarding nothing, whatever the program does

Status: active
Kind: invariant
Serves: STK-261126-SM33, STK-261126-KZK8

Every qube of the proxy template loads a guard ruleset at boot, before
Qubes sets up its network; if the guard fails to load, neither Qubes'
network service, which turns forwarding on, nor the program starts. The
program's rules are a table of their own and never touch the guard. In
base chains at priority -400 the guard drops every forwarded packet,
and any packet about to leave with an input interface other than IPv6
link-local and multicast control traffic (counted and logged as a
tripwire); drops IPv6 arriving from `vif` interfaces; and drops the
proxy qube's own connections to other qubes' Qubes addresses.

Why: interception holds only while the program runs; a stopped,
crashed or reloading program, or Qubes turning forwarding back on, must
still leave nothing forwarded, so the guard does not depend on the
program at all.

Scenarios:
1. `guard-before-network` (agent, provisional): the guard's unit orders
   itself before Qubes' network services, and Qubes' network unit
   requires it.
2. `forward-dropped` (agent, provisional): the guard's forward chain drops
   every packet at priority -400.
3. `program-stopped` (agent, provisional): with the program stopped and
   `ip_forward` set to 1, a routed qube's packet to the internet is not
   forwarded.

Changes:
- 2026-11-27: without the guard, Qubes' network service and the program do
  not start, but the uplink may, which the text overstated; the tripwire's
  IPv6 link-local and multicast exception is stated.

## SW-261127-N3N8 In a qube with no netvm, the client listens on loopback only, one qrexec call per connection

Status: active
Kind: invariant
Serves: STK-261126-KZK8

The client listens on 127.0.0.1, on the ports dom0's feature for the
qube lists, and turns each connection into one qrexec call to the
service and target listed for that port. A list with an entry outside
the allowed forms, a port of zero or a port listed twice is refused
whole, and the client listens on nothing.

Why: the qube's programs need ordinary ports to reach out; listening
only on loopback keeps the ports from being a way in, and one call per
connection lets dom0's policy and the proxy decide each one.

Scenarios:
1. `loopback-only` (agent, provisional): the client's listeners are on
   127.0.0.1 only.
2. `one-call-per-connection` (agent, provisional): two connections to a
   port make two qrexec calls with that port's service and target.
3. `bad-service-not-listened` (agent, provisional): a list with one
   entry whose service or target is not of the allowed form is refused
   whole, so no port gets a listener.

Changes:
- 2026-11-27: a list with a bad entry is refused whole, not that entry
  alone, which is what the client does.
- 2026-11-27: the client listens on loopback with no option to listen
  elsewhere; restarting on a changed feature is a convenience, not this
  decision, and is dropped from the text.
