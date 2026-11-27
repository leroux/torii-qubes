# Deciding a connection

Code: proxy/internal/policy, proxy/internal/engine, proxy/internal/dnsx, proxy/internal/sni

This area answers STK-261126-KZK8, that a carried qube's connection is
relayed only if its entry allows it. It covers how the proxy program
decides a connection once it knows which qube the connection came from:
the zone of the address, what a name entry counts for, the TLS server
name, the HTTP proxy, UDP flows and inbound ports. How a connection
reaches the proxy, and how the proxy identifies the qube, belong to the
links area.

## SW-261127-WNYB The proxy MUST place every address in exactly one zone and MUST NOT dial a forbidden address

Status: active
Kind: invariant
Serves: STK-261126-KZK8, STK-261126-SM33

- The proxy MUST place an address, in this order of precedence:
  - in the tailnet zone if it is in 100.64.0.0/10 or fd7a:115c:a1e0::/48;
  - otherwise in the forbidden zone if it is loopback, unspecified,
    link-local, multicast, 255.255.255.255, in 0.0.0.0/8, or in Qubes'
    own 10.137.0.0/16 to 10.139.0.0/16;
  - otherwise in the LAN zone if it is in 10.0.0.0/8, 172.16.0.0/12,
    192.168.0.0/16 or fc00::/7;
  - otherwise in the internet zone.
- The proxy MUST decide a connection by the zone of the address it
  dials, never by the name the qube used.
- The proxy MUST refuse a forbidden address whatever the file says.
- On the direct path, the proxy MUST also refuse any of the proxy qube's
  own addresses. Through an exit node those addresses belong to the exit
  node's network and are not refused.

Why: a name allowed on the internet could resolve to a LAN address,
another qube or the proxy qube itself, and a decision made by name would
then open those. Deciding by the address actually dialled closes that
path, and because the ranges are fixed in the code, no mistake in the
file can widen them.

Scenarios:
1. `zones-partition` (agent, provisional): given an address in each listed
   range, when it is placed, then it lands in its zone, with the
   tailnet's ULA placed before fc00::/7.
2. `other-qube-forbidden` (agent, provisional): given a qube allowed
   `internet: all`, when it connects to 10.137.0.5, then the connection
   is refused.
3. `proxy-self-forbidden` (agent, provisional): given a qube allowed
   `internet: all`, when it connects to the proxy qube's own address,
   then the connection is refused.
4. `internet-name-to-lan` (agent, provisional): given a name allowed on
   the internet that resolves to 192.168.1.1 and a qube whose LAN
   allowlist does not name it, when the qube connects to it, then the
   connection is refused.

Changes:
- 2026-11-27: the proxy qube's own addresses are refused on the direct
  path only, not placed in the forbidden zone, and broadcast is
  255.255.255.255, as the code does; `internet-name-to-lan` says the LAN
  allowlist does not name it, as its test has it (review of the first
  implementation).
- 2026-11-27: reworded in the owner's claim form; the ruling is unchanged.

## SW-261127-2R9T The proxy MUST count a name entry only for a name the qube asked for, or resolved through the proxy to the address dialled

Status: active
Kind: invariant
Serves: STK-261126-KZK8

- A name entry MUST allow a connection only when the name it matches is
  the name the qube asked for (through SOCKS, a forward or the HTTP
  proxy), or a name the proxy's DNS resolved for that qube to the address
  dialled.
- The proxy MUST remember, per qube, each name it resolved and the
  addresses it returned, for five minutes from the last answer, whatever
  the answer's TTL.
- The proxy MUST refuse a connection to an address that no name of the
  qube's leads to and no address entry covers.

Why: an allowlist by name means nothing if a qube can claim that any
address belongs to an allowed name. Tying the name to the qube's own
resolution through the proxy is what lets names stand for addresses,
which is necessary because CDNs share addresses and sites change them.

Scenarios:
1. `resolved-name-allows` (agent, provisional): given a qube that
   resolved pypi.org through the proxy, when it connects to the address
   it got, then the connection is allowed.
2. `unresolved-address-refused` (agent, provisional): given a qube
   allowed pypi.org, when it connects to an address it never resolved,
   then the connection is refused with `ip-not-resolved-by-workload`.
3. `other-qubes-resolution-not-shared` (agent, provisional): given an
   address one qube resolved, when another qube connects to it, then the
   address counts as unresolved for the second qube.
4. `mapping-expires` (agent, provisional): given a resolved mapping whose
   time has passed, when the qube connects to the address, then the
   connection is refused as unresolved.

Changes:
- 2026-11-27: a resolved name counts for five minutes from the last answer
  whatever its TTL, which the proxy does, not for the TTL with a minute's
  floor (review of the first implementation).
- 2026-11-27: reworded in the owner's claim form; the ruling is unchanged.

## SW-261127-M7G9 The proxy MUST resolve a name, for the qube and for itself, only to addresses the qube may reach under that name

Status: active
Kind: invariant
Serves: STK-261126-KZK8

- The proxy answers a routed qube's DNS. It MUST answer a name that the
  entry allows in no zone with "no such name" (NXDOMAIN).
- For an allowed name, the answer MUST hold only the addresses that the
  entry allows under that name in their zone and that are not forbidden.
- When the proxy resolves a name itself, for a qube with no netvm, it
  MUST dial only the addresses that pass the same test.

Why: a DNS answer that points a qube at an address it may not reach is a
first step toward reaching it, for example a public name that resolves
to a LAN address or another qube's. Filtering the addresses at
resolution keeps a public name from steering a qube there, before any
connection is attempted.

Scenarios:
1. `disallowed-name-nxdomain` (agent, provisional): given a qube allowed
   only pypi.org, when it asks for example.com, then it gets "no such
   name".
2. `answer-filtered` (agent, provisional): given a name that resolves to
   one public and one forbidden address, when the qube asks for it, then
   the answer holds only the public address.
3. `proxy-resolution-filtered` (agent, provisional): given a qube whose
   LAN is `none`, when it opens a SOCKS connection by a name that
   resolves only to a LAN address, then nothing is dialled.

Changes:
- 2026-11-27: reworded in the owner's claim form; the ruling is unchanged.

## SW-261127-2XS6 The proxy MUST match an allow entry by exact name or subdomain, port and protocol, treating an entry without a protocol as TCP only

Status: active
Kind: invariant
Serves: STK-261126-KZK8

- `example.com` MUST match that name alone; `*.example.com` MUST match
  every name under it and MUST NOT match `example.com` itself.
- Names MUST compare case-insensitively and without a trailing dot.
- An address or range entry MUST match the address dialled, whatever the
  names.
- An entry without a port MUST match every port.
- An entry without a protocol MUST match TCP only; UDP MUST need `/udp`.

Why: an entry should allow exactly what its owner reads in it. A
wildcard that also matched its parent domain, or a protocol that was
implied rather than written, would widen access beyond anything the file
states.

Scenarios:
1. `exact-name-only` (agent, provisional): given the entry `example.com`,
   when the qube connects to `www.example.com`, then it is refused.
2. `wildcard-not-parent` (agent, provisional): given the entry
   `*.example.com`, when the qube connects to `a.example.com` and to
   `example.com`, then the first is allowed and the second refused.
3. `port-narrows` (agent, provisional): given the entry
   `example.com:443`, when the qube connects to port 80, then it is
   refused.
4. `udp-explicit` (agent, provisional): given the entry
   `example.com:443`, when the qube sends UDP to 443, then it is refused;
   given `example.com:443/udp`, then it is allowed.

Changes:
- 2026-11-27: reworded in the owner's claim form; the ruling is unchanged.

## SW-261127-6S2F The proxy MUST check an allowlisted connection's TLS server name before relaying any of the client's bytes

Status: active
Kind: invariant
Serves: STK-261126-KZK8

- When a zone of the qube's entry is an allowlist, the proxy MUST hold
  the client's bytes until it has read the client's first bytes. It MAY
  relay the server's bytes meanwhile.
- Unless an address entry allowed the connection, a ClientHello's server
  name MUST match a name entry of the zone reached, including the entry's
  port and protocol.
- On the tailnet, when the device behind the address is known, the
  server name MUST also name that device.
- The proxy MUST refuse a TLS record from which no ClientHello can be
  read within two seconds of its first byte.
- A connection that is not TLS, or a ClientHello with no server name, is
  decided on the names alone.

Why: without this check, a qube that resolved an allowed name could
connect to that name's address and ask for a different site in its
ClientHello, which on a shared address such as a CDN's reaches that site
(domain fronting). Reading the server name before relaying any client
byte refuses such a connection without decrypting anything, so the check
needs no CA in the qube and breaks no pinned client.

Scenarios:
1. `sni-mismatch-refused` (agent, provisional): given a qube allowed a
   name that it resolved, when it connects to that address with a server
   name that is not allowed, then the connection is refused and no client
   byte reaches upstream.
2. `unreadable-hello-refused` (agent, provisional): given a TLS record
   that is not a readable ClientHello, when the proxy reads it, then the
   connection is refused with `server-name-unreadable`.
3. `pause-does-not-skip` (agent, provisional): given a client that waits
   before sending its ClientHello, when it sends it, then the connection
   is still decided by its server name.
4. `tailnet-sni-names-device` (agent, provisional): given a tailnet
   connection to one listed device, when its server name names another
   listed device, then the connection is refused.

Changes:
- 2026-11-27: a connection an address entry allows is not checked by
  server name, and the tailnet device is checked when known, as the proxy
  does (review of the first implementation).
- 2026-11-27: reworded in the owner's claim form; the ruling is unchanged.

## SW-261127-H3W9 The proxy's HTTP proxy MUST carry only one host and port per connection

Status: active
Kind: invariant
Serves: STK-261126-KZK8

- The proxy MUST decide a connection to the client's HTTP proxy by its
  first request's host and port.
- Each later request on that connection MUST name the same host and port;
  otherwise the proxy MUST refuse the request and close the connection.
- The proxy MUST decide a `CONNECT` like any connection by name, with the
  TLS server name checked as SW-261127-6S2F rules.

Why: HTTP keeps connections alive across requests. If the proxy decided
only the first request, every later request on the same connection could
go to any host, so an allowed first request would open everything after
it.

Scenarios:
1. `host-change-refused` (agent, provisional): given a connection whose
   first request was allowed, when a second request names another host,
   then it is refused with `http-host-changed`.
2. `disallowed-host-forbidden` (agent, provisional): given a host the
   entry does not allow, when the qube requests it, then the answer is
   403 and nothing is sent upstream.

Changes:
- 2026-11-27: reworded in the owner's claim form; the ruling is unchanged.

## SW-261127-PXX9 The proxy MUST decide a connection against the entry of the qube it came from, and only by a way that entry offers

Status: active
Kind: invariant
Serves: STK-261126-KZK8

- The proxy MUST decide a connection against the entry of the qube the
  links area names as its source, and MUST refuse a connection whose
  source names no qube.
- The proxy MUST decide an intercepted connection only for a routed
  qube, and SOCKS, a forward or the HTTP proxy only for a qube with no
  netvm; SOCKS and the HTTP proxy only when the qube's entry declares
  them.
- The proxy MUST decide a routed qube that is attached but not listed by
  its proxy's `unlisted` policy, which MAY allow internet destinations
  and MUST NOT allow the LAN, the tailnet or a tailnet identity.

Why: a qube's entry is the whole of its access only if no connection is
ever decided against another qube's entry, and no connection arrives by
a way its own entry does not offer. Either gap would let a qube borrow
access the file gave to something else.

Scenarios:
1. `unknown-source-refused` (agent, provisional): given an address that
   names no qube, when a connection arrives from it, then it is refused
   with `unknown-workload`.
2. `wrong-entry-refused` (agent, provisional): given a routed qube, when
   it opens a SOCKS connection, then it is refused with `wrong-entry`.
3. `undeclared-socks-refused` (agent, provisional): given a qube whose
   entry sets `socks: no`, when it opens a SOCKS connection, then it is
   refused.
4. `unlisted-no-tailnet` (agent, provisional): given an unlisted qube
   whose proxy allows it the internet, when it connects to a tailnet
   address, then it is refused.

Changes:
- 2026-11-27: reworded in the owner's claim form; the ruling is unchanged.

## SW-261127-T7GG The proxy MUST relay an inbound connection to a qube only on a port its entry or a service lists

Status: active
Kind: invariant
Serves: STK-261126-KZK8

- The proxy MUST relay a connection arriving at a qube's tailnet
  identity only when its port is one that the entry's `tailnet.inbound`
  or a service lists, and MUST send it to the qube's port mapped behind
  it.
- An identity MUST NOT listen on any other port, so a connection to one
  is refused.
- Who on the tailnet may connect at all is decided by the grants.

Why: access to a port takes two layers: the file decides what a qube
offers, and the tailnet's grants decide who may use it. A port the file
does not offer must therefore stay unreachable however the grants are
written.

Scenarios:
1. `listed-port-mapped` (agent, provisional): given an identity listing
   `{port: 443, target: 8443}`, when a connection arrives on 443, then it
   reaches the qube's port 8443.
2. `unlisted-inbound-refused` (agent, provisional): given a port the
   identity does not list, when a connection arrives on it, then it is
   refused with `inbound-port-not-declared`.

Changes:
- 2026-11-27: an identity listens only on its declared ports, which is how
  the proxy refuses the rest (review of the first implementation).
- 2026-11-27: reworded in the owner's claim form; the ruling is unchanged.

## SW-261127-QQYS The proxy MUST decide UDP once per flow and MUST cap each qube's open flows

Status: active
Kind: bound
Serves: STK-261126-KZK8

- The proxy MUST decide the first datagram from a routed qube to a
  destination as it decides any connection, with UDP as the protocol.
- The datagrams after it MUST follow that decision until the flow is
  idle.
- A qube at its cap of open flows, refused flows included, MUST NOT open
  a new one, and the proxy MUST log that refusal at most once a second.

Why: deciding every datagram would repeat work that the flow's first
datagram already settled. The cap exists because all qubes behind a
proxy share it, and without a limit one qube could exhaust the proxy for
all of them.

Scenarios:
1. `flow-follows-first` (agent, provisional): given a flow whose first
   datagram was allowed, or refused, when more datagrams follow, then
   they are relayed without a new decision, or dropped.
2. `idle-flow-redecided` (agent, provisional): given a flow that went
   idle, when another datagram arrives, then it is decided again.
3. `cap-refuses` (agent, provisional): given a qube at its flow cap, when
   it sends to a new destination, then no flow opens and one event says
   so.

Changes:
- 2026-11-27: reworded in the owner's claim form; the ruling is unchanged.
