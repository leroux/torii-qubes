# How a connection is decided

A walkthrough of one outbound connection through the proxy program, from
the moment a qube asks for a name to the last byte relayed, then the other
ways a connection arrives and the way in. The parts it passes through are
described once, in [`architecture.md`](architecture.md); the rules it
meets are in [`claims/connections.md`](../claims/connections.md) and
[`claims/links.md`](../claims/links.md).

The example: a qube `build`, `network: proxy` (its netvm is `sys-torii`),
whose entry says

```yaml
build:
  network: proxy
  outbound: { internet: [pypi.org:443, files.pythonhosted.org:443] }
```

and in which someone runs `pip install`.

## 1. The name

pip asks for `pypi.org`. The query leaves `build` for its netvm's DNS
address and arrives on a `vif*` interface of `sys-torii`, where the rules
[`intercept.Rules`](../proxy/internal/intercept/intercept.go#L41) wrote send
all DNS to the program's DNS port, and every TCP connection to its
intercept port; nothing is forwarded ([SW-261127-B6Z6](../claims/links.md#sw-261127-b6z6-a-routed-qubes-traffic-reaches-the-program-and-nothing-else-interception-with-forwarding-off)).
[`intercept.ServeDNSUDP`](../proxy/internal/intercept/intercept.go#L181) hands
the query to [`Engine.AnswerDNS`](../proxy/internal/engine/engine.go#L807).

`AnswerDNS` first names the qube by the packet's source address,
[`Config.ByAddress`](../proxy/internal/config/config.go#L523): an address no
entry has gets `REFUSED`. It then asks whether `build` may ask for the name
at all ([`DecideName`](../proxy/internal/policy/policy.go#L243)); a name no
entry could allow gets `NXDOMAIN`. It resolves the name upstream and keeps
only the addresses `build` may reach under it, each decided by its zone:

<!-- charter:embed SW-261127-M7G9 -->
> **[SW-261127-M7G9](../claims/connections.md#sw-261127-m7g9-a-name-resolves-for-the-qube-and-for-the-proxy-only-to-addresses-the-qube-may-reach-under-it) A name resolves, for the qube and for the proxy, only to addresses the qube may reach under it**
>
> The proxy answers a routed qube's DNS. A name the entry allows in no
> zone is answered with no such name. For an allowed name, the answer
> holds only the addresses that the entry allows under that name in their
> zone and that are not forbidden. When the proxy resolves a name itself,
> for a qube with no netvm, it dials only the addresses that pass the same
> test.
<!-- /charter:embed -->

What it answers it remembers: the cache,
[`dnsx.Cache`](../proxy/internal/dnsx/dnsx.go#L95), holds for each qube which
names it resolved to which address, for at least
[`nameMemory`](../proxy/internal/engine/engine.go#L84), five minutes, whatever
the answer's TTL. That memory is what a name entry counts on later.

## 2. The connection arrives

pip connects to one of those addresses on 443. The SYN is redirected to the
intercept port; [`intercept.ServeTCP`](../proxy/internal/intercept/intercept.go#L158)
reads where it was going from the socket
([`OriginalDst`](../proxy/internal/intercept/intercept.go#L112)) and calls
[`Engine.HandleIntercepted`](../proxy/internal/engine/engine.go#L779) with the
source and the destination.

The qube is named again by its source address, which Qubes keeps it from
forging: [SW-261127-BHHX](../claims/links.md#sw-261127-bhhx-a-routed-qube-is-named-by-its-qubes-source-address-which-qubes-keeps-it-from-forging). Then `classify` asks the qube's identity, if
it has one, whether the address is on the tailnet, and the cache supplies
the names `build` resolved to it, `ResolvedFrom`.

## 3. The decision

[`Engine.decide`](../proxy/internal/engine/engine.go#L202) calls the pure
[`DecideOutbound`](../proxy/internal/policy/policy.go#L131), which decides
against the entry of the qube it came from, and only by the way its entry
says it arrives: an intercepted connection from a qube with no netvm is
refused as `wrong-entry` ([SW-261127-PXX9](../claims/connections.md#sw-261127-pxx9-a-connection-is-decided-against-the-entry-of-the-qube-it-came-from-by-the-way-it-arrived)).

Off the tailnet, the address decides the zone,
[`ZoneOf`](../proxy/internal/policy/zone.go#L44):

<!-- charter:embed SW-261127-WNYB -->
> **[SW-261127-WNYB](../claims/connections.md#sw-261127-wnyb-every-address-is-in-exactly-one-zone-and-a-forbidden-address-is-never-dialled) Every address is in exactly one zone, and a forbidden address is never dialled**
>
> An address is tailnet if it is in 100.64.0.0/10 or fd7a:115c:a1e0::/48;
> otherwise forbidden if it is loopback, unspecified, link-local,
> multicast, 255.255.255.255, in 0.0.0.0/8, or in Qubes' own 10.137.0.0/16
> to 10.139.0.0/16; otherwise LAN if it is in 10.0.0.0/8, 172.16.0.0/12,
> 192.168.0.0/16 or fc00::/7; otherwise internet. A connection is decided by
> the zone of the address it dials, never of the name it used. A forbidden
> address is refused whatever the file says, and so, on the direct path, is
> any address of the proxy qube's own; through an exit node those are the
> exit node's network.
<!-- /charter:embed -->

`build` allows the internet by list, so
[`Permit`](../proxy/internal/policy/zone.go#L80) walks the list. An address
entry matches by address; a name entry matches only through a name:

<!-- charter:embed SW-261127-2R9T -->
> **[SW-261127-2R9T](../claims/connections.md#sw-261127-2r9t-a-name-entry-counts-only-for-a-name-the-qube-asked-for-or-resolved-through-the-proxy-to-that-address) A name entry counts only for a name the qube asked for, or resolved through the proxy to that address**
>
> A name entry allows a connection only when the name it matches is the
> one the qube asked for (SOCKS, a forward, the HTTP proxy) or one the
> proxy's DNS resolved for that qube to the address dialled. The proxy
> remembers, per qube, each name it resolved and its addresses for five
> minutes from the last answer, whatever the answer's TTL. A connection to an address no name of
> the qube's leads to, and no address entry covers, is refused.
<!-- /charter:embed -->

`pypi.org:443` matches `pypi.org` on 443 over TCP; it would not match
`www.pypi.org`, port 80, or UDP ([SW-261127-2XS6](../claims/connections.md#sw-261127-2xs6-allow-entries-match-by-exact-name-or-subdomain-port-and-protocol-tcp-unless-they-say-udp),
[`Match`](../proxy/internal/policy/policy.go#L221)). An address nobody resolved
is refused as `ip-not-resolved-by-workload`, which is why a qube on an allowlist cannot
reach a site by typing its address.

Two refusals are added around the pure decision, in `decide`: the proxy
qube's own addresses on the direct path
([`Engine.forbidden`](../proxy/internal/engine/engine.go#L177)), and the exit
path while the qube's egress has no healthy exit node
([`Engine.exitUsable`](../proxy/internal/engine/engine.go#L215), followed in
[the egress walkthrough](walkthrough-egress.md)).

## 4. The dial

[`Engine.dial`](../proxy/internal/engine/engine.go#L659) dials by the decision's
path ([`PathFor`](../proxy/internal/policy/zone.go#L166)): the internet and the
LAN through the proxy qube's own network, so its netvm; a tailnet address
on the qube's own identity; the exit path on the egress's identity. A name
the program resolves itself (SOCKS, the HTTP proxy, a forward) is dialled
only at the addresses the qube may reach under it, the same filter as the
DNS answer.

## 5. The server name, before anything is relayed

The connection is open upstream, and nothing of pip's has been sent.
[`Engine.carry`](../proxy/internal/engine/engine.go#L741) holds the client's
side until its first bytes are read:

<!-- charter:embed SW-261127-6S2F -->
> **[SW-261127-6S2F](../claims/connections.md#sw-261127-6s2f-under-an-allowlist-a-tls-server-name-must-be-allowed-too-and-nothing-is-relayed-before-it-is-read) Under an allowlist, a TLS server name must be allowed too, and nothing is relayed before it is read**
>
> When a zone of the qube's entry is an allowlist, the client's bytes are
> held until its first bytes have been read; the server's side may speak
> meanwhile. Unless an address entry allowed the connection, a
> ClientHello's server name must match a name entry of the zone reached,
> with its port and protocol; on the tailnet, when the device behind the
> address is known, it must also name that device. A TLS record from which no
> ClientHello can be read within two seconds of its first byte is refused.
> A connection that is not TLS, or a ClientHello with no server name, is
> decided on the names alone.
<!-- /charter:embed -->

[`sni.Peek`](../proxy/internal/sni/sni.go#L40) reads the ClientHello with
`crypto/tls`, so a hello split across records does not hide the name. If
the name is `pypi.org`, the decision is made again with it, and the bytes
read go upstream first; the rest is copied as it comes.

Two traps here. The server's side flows from the start, so a protocol in
which the server speaks first still works. And a connection that sends no
server name at all is decided on the names alone: raw TCP to an allowed
name's address reaches whatever that address serves. That is a limit,
stated in the need [STK-261126-KZK8](../claims/needs.md#stk-261126-kzk8-a-carried-qubes-connection-is-relayed-only-if-its-entry-allows-it-every-other-connection-is-refused) and kept by the owner in their
answer to Q-261127-YZQC in
[`claims/questions.md`](../claims/questions.md).

## 6. The log

Every decision, allowed or refused, at any of these steps, is one event:
[`fill`](../proxy/internal/engine/engine.go#L1023) copies the verdict, the reason
and the path into it and [`Log.Emit`](../proxy/internal/events/events.go#L61)
keeps it ([SW-261127-R4CD](../claims/seeing.md#sw-261127-r4cd-every-connection-the-proxy-decides-is-logged-with-its-verdict-and-the-rule-that-made-it)). That is what `torii log build` reads back
in dom0, through `torii.Status`.

## The other ways in

**A qube with no netvm** reaches the same decision by another door.
`torii-client` in the qube listens on loopback and turns each connection
into one `torii.Connect` call ([SW-261127-N3N8](../claims/links.md#sw-261127-n3n8-in-a-qube-with-no-netvm-the-client-listens-on-loopback-only-one-qrexec-call-per-connection)); dom0's policy names
the caller, and
[`Engine.HandleQrexec`](../proxy/internal/engine/engine.go#L534) takes the
qube dom0 named, never one the call states ([SW-261127-7C3Y](../claims/links.md#sw-261127-7c3y-a-qrexec-caller-is-the-qube-dom0-names-never-one-the-caller-states)). By the
service argument it is a SOCKS request, the HTTP proxy or a fixed forward.
Each brings the name it asks for, which counts as the qube's name for the
decision; no DNS step comes first.

**The HTTP proxy** decides a `CONNECT` like a SOCKS request and carries it
with the same server-name check
([`Engine.httpConnect`](../proxy/internal/engine/http.go#L53)). A plain
request is decided by its URL's host, and every later request on the
connection must name the same host and port, or the connection ends
([`Engine.httpForward`](../proxy/internal/engine/http.go#L97)):
[SW-261127-H3W9](../claims/connections.md#sw-261127-h3w9-the-http-proxy-carries-one-host-per-connection).

**UDP** from a routed qube arrives by TPROXY and is decided once a flow,
the first datagram of a source and destination
([`Engine.HandleUDP`](../proxy/internal/engine/udp.go#L69)); the rest follow
until the flow is idle, and each qube may hold a capped number of flows,
refused ones included: [SW-261127-QQYS](../claims/connections.md#sw-261127-qqys-udp-is-decided-per-flow-and-each-qube-has-a-cap-on-open-flows). An allowlist entry counts for
UDP only if it says `/udp`; QUIC's server name is not read.

## The way in

An inbound connection reaches the qube's identity on the tailnet, at a
port its entry lists: [`Engine.Start`](../proxy/internal/engine/engine.go#L286)
listens only on those.
[`DecideInbound`](../proxy/internal/policy/policy.go#L290) checks the port again,
and the program dials the qube with a `qubes.ConnectTCP` call, for a routed
qube too ([`Engine.handleInbound`](../proxy/internal/engine/engine.go#L482)):

<!-- charter:embed SW-261127-T7GG -->
> **[SW-261127-T7GG](../claims/connections.md#sw-261127-t7gg-an-inbound-connection-reaches-a-qube-only-on-a-port-its-entry-or-a-service-lists) An inbound connection reaches a qube only on a port its entry or a service lists**
>
> A connection arriving at a qube's tailnet identity is relayed only when
> its port is one the entry's `tailnet.inbound` or a service lists, and
> goes to the qube's port behind it. An identity listens on no other
> port, and a connection to one would be refused. Who on the tailnet may
> connect at all is the grants'.
<!-- /charter:embed -->

Who on the tailnet may open that port is the grants' to decide, not the
file's; `torii grants` proposes them ([SW-261127-DH6R](../claims/identity.md#sw-261127-dh6r-torii-proposes-grants-for-the-tailnet-and-never-changes-its-policy)).

## Asking without sending

`torii-proxy explain`, run in the proxy qube against its admin socket,
asks [`Engine.Explain`](../proxy/internal/engine/engine.go#L903) to make the
same decision for a connection described, not made, and dials nothing:
[SW-261127-Z5ST](../claims/seeing.md#sw-261127-z5st-explain-gives-the-decision-the-proxy-would-make-without-sending-anything). It does not resolve a name, so it can call a name
allowed whose addresses the dial would all refuse. dom0 has no command
for it yet: `torii.Status` carries the status and the log only.
