# Reaching the proxy

Code: proxy/internal/qrexec, proxy/internal/intercept, proxy/internal/socks5, proxy/cmd/torii-client, packaging/proxy

This area answers STK-261126-KZK8 and STK-261126-SM33. It covers how a
carried qube's connection reaches the proxy program and how the program
knows which qube sent it: for a qube with no netvm, qrexec calls that run
in the direction of the connection; for a routed qube, interception with
kernel forwarding off; the boot-time guard that keeps forwarding off; and
the client's ports inside a qube with no netvm.

## SW-261127-M36P torii's qrexec policy MUST allow only calls that run in the direction of the connection they carry, and only those the file declares

Status: active
Kind: invariant
Serves: STK-261126-KZK8, STK-261126-SM33

- Outbound: a carried qube with no netvm MUST be allowed to call
  `torii.Connect` only in its own proxy, with `+socks`, `+http` or
  `+f.<forward>`, and only for those its entry declares.
- Inbound: the proxy MUST be allowed to call `qubes.ConnectTCP` into a
  carried qube, routed or not, only on the target ports its entry or a
  service lists.
- Between qubes on this machine, `qubes.ConnectTCP` MUST be allowed only
  from the qubes a `local-inbound` names, on its port.
- A qube that declares no outbound access MUST NOT be allowed any
  `torii.Connect` call, and MUST NOT be allowed `qubes.ConnectTCP`
  except where another qube's `local-inbound` names it.
- torii's deny file, read after every mesh's own file, MUST deny
  `torii.Connect` and `torii.Status` to every caller the allows do not
  name. `qubes.ConnectTCP` beyond the allows is left to Qubes' own
  policy (observation QubesConnectTCPDenied).
- A hand-written policy file read before torii's can widen any of these,
  and `torii check` names it (STK-261126-4Q75).

Why: a call that runs against the connection, such as a tunnel the qube
holds open to its proxy, would give every qube a standing channel out and
put a tunnel protocol inside a qube meant to be offline. When every call
runs in the direction of its connection, a qube that only answers can be
reached, and can reach out, only as its entry says.

Scenarios:
1. `outbound-own-proxy-only` (agent, provisional): given a qube that
   declares `socks`, when the policy is written, then it allows
   `torii.Connect+socks` to the qube's own proxy and to no other qube.
2. `inbound-listed-ports` (agent, provisional): given a qube's listed
   target ports, when the policy is written, then the proxy is allowed
   `qubes.ConnectTCP` into the qube on each of those ports and on no
   other.
3. `silent-qube-no-calls` (agent, provisional): given a qube with no
   outbound access, when the policy is written, then it has no
   `torii.Connect` allow line, and as a caller only the
   `qubes.ConnectTCP` lines another qube's `local-inbound` gives it.
4. `others-denied` (agent, provisional): given the deny file, when it is
   read, then it denies `torii.Connect` from any qube to any qube.

Changes:
- 2026-11-27: a qube with no outbound access may still call
  `qubes.ConnectTCP` where `local-inbound` names it, and the deny file
  covers torii's own services only, `qubes.ConnectTCP` resting on Qubes'
  policy; the text claimed no call at all and a deny it does not write.

## SW-261127-7C3Y The proxy MUST take a qrexec caller's identity from what dom0 sets, never from what the caller sends

Status: active
Kind: invariant
Serves: STK-261126-KZK8

- The program MUST take a `torii.Connect` caller's name and argument from
  what qrexec sets: the header qrexec-agent writes, or the environment it
  gives the service. It MUST NOT read anything the caller sends as either.
- A name or argument outside the allowed forms MUST be refused before the
  call reaches its handler.
- The program MUST refuse a caller the file does not list, or one listed
  as routed, and MUST refuse an argument other than `socks`, `http` or
  `f.<forward>`.

Why: deciding a connection against a qube's entry is only sound if the
qube cannot choose which entry it is decided against. dom0 names the
caller of every qrexec call, so taking the name from dom0's side, and
reading nothing the caller sends as its name, leaves the caller no way to
impersonate another qube.

Scenarios:
1. `caller-from-qrexec` (agent, provisional): given a call from a qube
   whose bytes start with a forged header, when the program reads it,
   then the caller is the qube in `QREXEC_REMOTE_DOMAIN` and the forged
   header arrives as data.
2. `bad-name-refused` (agent, provisional): given a caller or argument
   that is not of the allowed form, when the call arrives, then it never
   reaches the program's handler.
3. `routed-caller-refused` (agent, provisional): given a qube listed as
   routed, when it calls `torii.Connect`, then the call is refused with
   `wrong-entry`.

Changes:
- 2026-11-27: a bad name is refused before the handler, in the program on
  the socket path, not before the program; the scenario says what its test
  shows.

## SW-261127-BHHX The proxy MUST identify a routed qube by its Qubes source address, which Qubes keeps it from forging

Status: active
Kind: invariant
Serves: STK-261126-KZK8

- The proxy MUST decide a connection or datagram from a routed qube
  against the entry whose Qubes address is its source address.
- An address no listed qube has is the proxy's unlisted source: the
  proxy MUST decide it by its `unlisted` policy, or refuse it when that
  policy is absent.
- The proxy trusts the source address because Qubes drops packets on a
  netvm's `vif` interfaces whose source is not the sending qube's own
  address (observation QubesAntispoof).

Why: a routed qube's packets carry no name the proxy can read other than
the source address, and that address identifies the qube only because
Qubes, not the qube, enforces it. If the observation stopped holding, a
qube could send as another and be decided against the other's entry, so
the decision rests explicitly on it.

Scenarios:
1. `source-names-qube` (agent, provisional): given an entry with Qubes
   address 10.137.0.20, when a connection arrives from 10.137.0.20, then
   it is decided against that entry.
2. `unknown-address-unlisted` (agent, provisional): given a proxy with no
   `unlisted` policy, when a connection arrives from an address no entry
   has, then it is refused.

## SW-261127-B6Z6 The proxy qube MUST hand every routed qube's traffic to the program and MUST NOT forward any of it

Status: active
Kind: invariant
Serves: STK-261126-KZK8, STK-261126-SM33

- On a proxy qube, every TCP connection and every DNS query arriving
  from a `vif` interface MUST be redirected to the program, and every
  other IPv4 UDP datagram MUST be handed to it with its destination kept.
- Kernel forwarding MUST be off, for IPv4 and IPv6, and the forward chain
  MUST drop everything.
- Input from a `vif` interface MUST be accepted only on the program's
  ports, for what was handed to it, for ICMP, and for connections already
  established; everything else MUST be dropped.
- A TCP connection to any other port of the proxy qube is redirected like
  any other, and the program MUST refuse it as the proxy's own address.

Why: the program can only decide what it receives. Redirecting all of a
routed qube's traffic to the program, and forwarding none of it, means a
connection the program did not relay cannot happen at all, rather than
depending on a rule to refuse it.

Scenarios:
1. `all-redirected` (agent, provisional): given the proxy's rules, when
   they are read, then they redirect TCP and DNS from `vif*` to the
   program's ports and hand it other IPv4 UDP.
2. `forwarding-off` (agent, provisional): given the program has started,
   when a packet would be forwarded, then `ip_forward` is 0 and the
   packet is dropped.
3. `input-closed` (agent, provisional): given the workloads' interfaces,
   when input arrives from them, then exactly the program's ports, what
   was handed to it, ICMP and established connections are accepted, and
   the rest is dropped.

Changes:
- 2026-11-27: TCP to another port of the proxy qube is redirected and
  refused by the program, not dropped, which the rules say themselves;
  input-closed names the exact rules its test checks.

## SW-261127-JWK2 The proxy template MUST load a guard at boot that keeps the proxy qube forwarding nothing, whatever the program does

Status: active
Kind: invariant
Serves: STK-261126-SM33, STK-261126-KZK8

- Every qube of the proxy template MUST load a guard ruleset at boot,
  before Qubes sets up its network. If the guard fails to load, Qubes'
  network service, which turns forwarding on, and the program MUST NOT
  start.
- The program's rules MUST be a table of their own and MUST NOT touch the
  guard.
- In base chains at priority -400, the guard MUST:
  - drop every forwarded packet;
  - drop any packet about to leave with an input interface, other than
    IPv6 link-local and multicast control traffic, counting and logging
    it as a tripwire;
  - drop IPv6 arriving from `vif` interfaces;
  - drop the proxy qube's own connections to other qubes' Qubes
    addresses.

Why: interception protects a routed qube only while the program runs. A
program that is stopped, has crashed or is reloading, or Qubes turning
forwarding back on, must still leave nothing forwarded, so the guard is
loaded before anything else and does not depend on the program at all.

Scenarios:
1. `guard-before-network` (agent, provisional): given the guard's unit,
   when the boot order is read, then the unit is ordered before Qubes'
   network services and Qubes' network unit requires it.
2. `forward-dropped` (agent, provisional): given the guard loaded, when
   any packet reaches its forward chain, then it is dropped at priority
   -400.
3. `program-stopped` (agent, provisional): given the program stopped and
   `ip_forward` set to 1, when a routed qube sends a packet to the
   internet, then it is not forwarded.

Changes:
- 2026-11-27: without the guard, Qubes' network service and the program do
  not start, but the uplink may, which the text overstated; the tripwire's
  IPv6 link-local and multicast exception is stated.

## SW-261127-N3N8 torii-client MUST listen on loopback only and MUST make one qrexec call per connection

Status: active
Kind: invariant
Serves: STK-261126-KZK8

- In a qube with no netvm, the client MUST listen on 127.0.0.1 only, on
  the ports that dom0's feature for the qube lists.
- The client MUST turn each connection into one qrexec call, to the
  service and target listed for that port.
- The client MUST refuse the whole list, and listen on nothing, when an
  entry is outside the allowed forms, a port is zero, or a port is listed
  twice.

Why: programs in the qube need ordinary ports to reach out. Listening
only on loopback keeps those ports from becoming a way into the qube, and
one qrexec call per connection lets dom0's policy and the proxy decide
each connection on its own.

Scenarios:
1. `loopback-only` (agent, provisional): given a client configured with
   ports, when it starts, then its listeners are on 127.0.0.1 only.
2. `one-call-per-connection` (agent, provisional): given a listed port,
   when two connections arrive on it, then the client makes two qrexec
   calls with that port's service and target.
3. `bad-service-not-listened` (agent, provisional): given a list with
   one entry whose service or target is not of the allowed form, when the
   client starts, then it refuses the list whole and no port gets a
   listener.

Changes:
- 2026-11-27: a list with a bad entry is refused whole, not that entry
  alone, which is what the client does.
- 2026-11-27: the client listens on loopback with no option to listen
  elsewhere; restarting on a changed feature is a convenience, not this
  decision, and is dropped from the text.
