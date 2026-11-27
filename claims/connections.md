# Deciding a connection

Code: proxy/internal/policy, proxy/internal/engine, proxy/internal/dnsx, proxy/internal/sni

The need this area answers is STK-261126-KZK8: a carried qube's
connection is relayed only if its entry allows it. It holds how the proxy
program decides one connection once it knows which qube it came from:
the zone of the address, what a name counts for, the TLS server name, the
HTTP proxy, UDP flows and inbound ports. How a connection reaches the
proxy, and how the proxy knows the qube, is the links area's.

## SW-261127-WNYB Every address is in exactly one zone, and a forbidden address is never dialled

Status: active
Kind: invariant
Serves: STK-261126-KZK8, STK-261126-SM33

An address is tailnet if it is in 100.64.0.0/10 or fd7a:115c:a1e0::/48;
otherwise forbidden if it is loopback, unspecified, link-local,
multicast, broadcast, in 0.0.0.0/8, in Qubes' own 10.137.0.0/16 to
10.139.0.0/16, or one of the proxy qube's own addresses; otherwise LAN if
it is in 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 or fc00::/7;
otherwise internet. A connection is decided by the zone of the address it
dials, never of the name it used. A forbidden address is refused whatever
the file says.

Why: a name allowed on the internet must not open the LAN, another qube
or the proxy itself by resolving there; deciding by the address dialled
closes that, and fixed ranges leave nothing to the file to get wrong.

Scenarios:
1. `zones-partition` (agent, provisional): an address in each listed range
   is placed in its zone, the tailnet's ULA before fc00::/7.
2. `other-qube-forbidden` (agent, provisional): a qube allowed
   `internet: all` is refused 10.137.0.5.
3. `proxy-self-forbidden` (agent, provisional): a qube allowed
   `internet: all` is refused the proxy qube's own address.
4. `internet-name-to-lan` (agent, provisional): a name allowed on the
   internet that resolves to 192.168.1.1 is refused for a qube whose LAN
   is `none`.

## SW-261127-2R9T A name entry counts only for a name the qube asked for, or resolved through the proxy to that address

Status: active
Kind: invariant
Serves: STK-261126-KZK8

A name entry allows a connection only when the name it matches is the
one the qube asked for (SOCKS, a forward, the HTTP proxy) or one the
proxy's DNS resolved for that qube to the address dialled. The proxy
remembers, per qube, each name it resolved and its addresses for the
answer's TTL, at least a minute. A connection to an address no name of
the qube's leads to, and no address entry covers, is refused.

Why: an allowlist by name means nothing if any address may be claimed to
belong to an allowed name; tying the name to the qube's own resolution
is what lets names, which CDNs and changing addresses make necessary,
stand for addresses.

Scenarios:
1. `resolved-name-allows` (agent, provisional): a qube that resolved
   pypi.org through the proxy reaches the address it got.
2. `unresolved-address-refused` (agent, provisional): a qube allowed
   pypi.org connecting to an address it never resolved is refused with
   `ip-not-resolved-by-workload`.
3. `other-qubes-resolution-not-shared` (agent, provisional): an address
   resolved by one qube is unresolved for another.
4. `mapping-expires` (agent, provisional): after the mapping's time has
   passed, a connection to the address is refused as unresolved.

## SW-261127-M7G9 A name resolves, for the qube and for the proxy, only to addresses the qube may reach under it

Status: active
Kind: invariant
Serves: STK-261126-KZK8

The proxy answers a routed qube's DNS. A name the entry allows in no
zone is answered with no such name. For an allowed name, the answer
holds only the addresses that the entry allows under that name in their
zone and that are not forbidden. When the proxy resolves a name itself,
for a qube with no netvm, it dials only the addresses that pass the same
test.

Why: an answer pointing a qube at an address it may not reach is a step
towards reaching it; filtering at resolution keeps a public name from
steering a qube to the LAN or another qube.

Scenarios:
1. `disallowed-name-nxdomain` (agent, provisional): a qube allowed only
   pypi.org asking for example.com gets no such name.
2. `answer-filtered` (agent, provisional): a name resolving to one public
   and one forbidden address is answered with the public one only.
3. `proxy-resolution-filtered` (agent, provisional): a SOCKS connection by
   a name resolving only to a LAN address, for a qube whose LAN is
   `none`, is not dialled.

## SW-261127-2XS6 Allow entries match by exact name or subdomain, port and protocol, TCP unless they say UDP

Status: active
Kind: invariant
Serves: STK-261126-KZK8

`example.com` matches that name alone; `*.example.com` matches every
name under it but not `example.com`; names compare without case and a
trailing dot. An address or range matches the address dialled, whatever
the names. An entry without a port matches every port; an entry without a
protocol matches TCP only, and UDP needs `/udp`.

Why: an entry must allow what its owner reads in it and no more; a
wildcard that also matched its parent, or a protocol that was implied,
would widen access the file never stated.

Scenarios:
1. `exact-name-only` (agent, provisional): `example.com` refuses
   `www.example.com`.
2. `wildcard-not-parent` (agent, provisional): `*.example.com` allows
   `a.example.com` and refuses `example.com`.
3. `port-narrows` (agent, provisional): `example.com:443` refuses port 80.
4. `udp-explicit` (agent, provisional): `example.com:443` refuses UDP to
   443, and `example.com:443/udp` allows it.

## SW-261127-6S2F Under an allowlist, a TLS server name must be allowed too, and nothing is relayed before it is read

Status: active
Kind: invariant
Serves: STK-261126-KZK8

When a zone of the qube's entry is an allowlist, the client's bytes are
held until its first bytes have been read; the server's side may speak
meanwhile. A ClientHello's server name must match a name entry of the
zone reached, with its port and protocol; on the tailnet it must also
name the device behind the address. A TLS record from which no
ClientHello can be read within two seconds of its first byte is refused.
A connection that is not TLS, or a ClientHello with no server name, is
decided on the names alone.

Why: a qube that resolved an allowed name could otherwise send a TLS
connection for another site at the same address (domain fronting);
reading the name before relaying anything closes that without decrypting
a byte.

Scenarios:
1. `sni-mismatch-refused` (agent, provisional): a connection to an allowed
   name's address whose server name is not allowed is refused, and none
   of the client's bytes reach upstream.
2. `unreadable-hello-refused` (agent, provisional): a TLS record that is
   not a readable ClientHello is refused with `server-name-unreadable`.
3. `pause-does-not-skip` (agent, provisional): a client that waits before
   sending its ClientHello is still decided by its server name.
4. `tailnet-sni-names-device` (agent, provisional): on the tailnet, a
   server name naming another listed device than the one dialled is
   refused.

## SW-261127-H3W9 The HTTP proxy carries one host per connection

Status: active
Kind: invariant
Serves: STK-261126-KZK8

A connection to the client's HTTP proxy is decided by its first
request's host and port; each later request on it must name the same
host and port, or the connection is refused and closed. CONNECT is
decided as any connection by name, with the TLS server name checked as
SW-261127-6S2F rules.

Why: deciding a kept-alive connection by its first request alone would
let every later request go anywhere.

Scenarios:
1. `host-change-refused` (agent, provisional): a second request on one
   connection to another host is refused with `http-host-changed`.
2. `disallowed-host-forbidden` (agent, provisional): a request to a host
   the entry does not allow is answered 403 and nothing is sent upstream.

## SW-261127-PXX9 A connection is decided against the entry of the qube it came from, by the way it arrived

Status: active
Kind: invariant
Serves: STK-261126-KZK8

A connection is decided against the entry of the qube the links area
names as its source; a source it names as no qube is refused. An
intercepted connection is decided only for a routed qube, and SOCKS, a
forward or the HTTP proxy only for a qube with no netvm, and SOCKS or the
HTTP proxy only when its entry declares them. A routed qube attached but
not listed is decided by its proxy's `unlisted` policy, which allows at
most internet destinations and never the LAN, the tailnet or a tailnet
identity.

Why: an entry is the whole of a qube's access only if no connection is
ever decided against another's, and none arrives by a way its entry does
not offer.

Scenarios:
1. `unknown-source-refused` (agent, provisional): a connection from an
   address that names no qube is refused with `unknown-workload`.
2. `wrong-entry-refused` (agent, provisional): a SOCKS connection from a
   routed qube is refused with `wrong-entry`.
3. `undeclared-socks-refused` (agent, provisional): a SOCKS connection
   from a qube whose entry sets `socks: no` is refused.
4. `unlisted-no-tailnet` (agent, provisional): an unlisted qube whose
   proxy allows it the internet is refused a tailnet address.

## SW-261127-T7GG An inbound connection reaches a qube only on a port its entry or a service lists

Status: active
Kind: invariant
Serves: STK-261126-KZK8

A connection arriving at a qube's tailnet identity is relayed only when
its port is one the entry's `tailnet.inbound` or a service lists, and
goes to the qube's port behind it. Every other port is refused. Who on
the tailnet may connect at all is the grants'.

Why: the file says what a qube offers; a port it does not offer must
not be reachable however the grants are written.

Scenarios:
1. `listed-port-mapped` (agent, provisional): a connection to port 443 of
   an identity listing `{port: 443, target: 8443}` reaches the qube's
   8443.
2. `unlisted-inbound-refused` (agent, provisional): a connection to a port
   the identity does not list is refused with `inbound-port-not-declared`.

## SW-261127-QQYS UDP is decided per flow, and each qube has a cap on open flows

Status: active
Kind: bound
Serves: STK-261126-KZK8

The first datagram from a routed qube to a destination is decided as any
connection is, with UDP as its protocol; the datagrams after it follow
that decision until the flow is idle. A qube at its cap of open flows
opens no new one, refused flows counting, and the refusal is logged at
most once a second.

Why: deciding every datagram costs what a flow's first already settled;
the cap keeps one qube from exhausting the proxy that all its qubes share.

Scenarios:
1. `flow-follows-first` (agent, provisional): datagrams after an allowed
   first one are relayed without a new decision, and after a refused one
   are dropped.
2. `idle-flow-redecided` (agent, provisional): a datagram after the flow
   went idle is decided again.
3. `cap-refuses` (agent, provisional): a qube at its flow cap gets no new
   flow, and one event says so.
