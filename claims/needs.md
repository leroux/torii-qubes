# What torii guarantees

Qubes decides a qube's network access by its netvm alone: with one, the
qube has a full network stack and the Qubes firewall limits it by
address; without one, it has no network at all. torii lets a qube have a
declared set of connections in between, and must do so without weakening
the isolation the qube was made for.

Terms. The machine file is `~/torii.yaml` in dom0. A managed qube is one
listed under its `qubes:`; its entry is what the file says under its
name. A carried qube is one whose traffic goes through a proxy qube:
`network: none` (each connection a qrexec call) or `network: proxy` (the
proxy qube is its netvm). The zone of a connection is decided by the
address actually dialled: tailnet (the tailnet's addresses), LAN (this
machine's private ranges outside Qubes' own), or internet (every other
public address).

Adversary. Any code in any qube other than dom0 may be hostile: a carried
qube, the qubes it talks to, and the hosts on the internet and the
tailnet. dom0 and the owner who writes the file are trusted. The tailnet's
coordination server is trusted to say who is on the tailnet, and its
grants are the owner's.

## STK-261126-KZK8 A carried qube's connection is relayed only if its entry allows it; every other connection is refused

Status: planned

Outbound, a connection from a carried qube is relayed only when its entry
allows the connection's zone and a destination in that zone matches it by
name or address, port and protocol, a name counting only if the qube
asked for it or resolved it to that address; tailnet connections must
also be allowed by the grants. Inbound, a connection reaches a carried
qube only on a port its entry or a service lists, and only from the qubes
`local-inbound` names or the tailnet peers the grants allow.

Whatever the file says, no other qube's address is reachable through the
proxy, and a carried qube has no IPv6. A qube attached to a proxy but not
listed, a disposable included, gets that proxy's `unlisted` policy, which
by default refuses everything, and never a tailnet identity.

Not covered, and documented as limits: raw TCP or QUIC to another site at
an address an allowed name shares, including any connection that sends
no TLS server name; data sent out over DNS by a qube
allowed `internet: all`. A qube with `network: default` or its own netvm
is not carried: torii sets only its netvm.

Why: a qube with a netvm and a wall of rules is one missing rule from
everything; deciding each connection by its declared destination, and
refusing whatever was not declared, is what makes the in-between access
safe to give.

Scenarios:
1. `allowed-name-passes` (agent, provisional): a qube allowed
   `pypi.org:443` reaches pypi.org on 443, and is refused pypi.org on 80
   and every other name.
2. `unresolved-address-refused` (agent, provisional): a connection to a
   public address the qube never resolved, which no address entry covers,
   is refused.
3. `sni-must-match` (agent, provisional): a TLS connection to an allowed
   name's address whose server name is not allowed is refused.
4. `udp-needs-udp` (agent, provisional): an entry without `/udp` refuses
   UDP to that name.
5. `unlisted-port-refused` (agent, provisional): a connection to a port
   the qube's entry does not list never reaches the qube.
6. `other-qubes-unreachable` (agent, provisional): a qube allowed
   `internet: all` is refused another qube's address.
7. `unlisted-qube-refused` (agent, provisional): a qube attached to the
   proxy but absent from the file is refused every connection.

## STK-261126-B4CP apply changes only what torii owns, and exactly the steps plan showed

Status: planned

plan is a function of the machine file and a snapshot of the live
machine, and lists every change in the order apply makes it. apply makes
those changes and no others, stops at the first that fails, and run again
makes only the changes left. On a machine that matches the file, plan is
empty.

torii owns: its template, the proxy qubes it created, the policy file
`/etc/qubes/policy.d/50-torii.policy`, and on each managed qube only its
netvm, the `torii-client` service and feature, its torii tags and the
adoption features it writes once. It
creates or deletes no other qube and writes no other policy file. A qube
removed from the file is left with no netvm, never its old one.

Why: a tool with dom0's power must be reviewable before it acts; a plan
that is the whole of what will happen, limited to what torii owns, is
what the owner reviews.

Scenarios:
1. `plan-is-apply` (agent, provisional): the steps apply makes on a
   machine are the steps plan listed for it, in order.
2. `second-apply-empty` (agent, provisional): plan on a machine apply has
   just made match the file is empty.
3. `interrupted-apply-continues` (agent, provisional): an apply stopped by
   a failed step, run again, makes only the steps left.
4. `unowned-untouched` (agent, provisional): a qube not in the file and a
   hand-written policy file are unchanged by apply.
5. `removed-qube-gets-no-netvm` (agent, provisional): a qube removed from
   the file ends the next apply with no netvm.

## STK-261126-4Q75 show reads each qube's reach from the live machine; check names every difference from the file

Status: planned

show reads, never from the file: each managed qube's netvm, every policy
file in dom0 including hand-written ones, the config each proxy qube is
running, and each identity's state; and reports from them what the qube
can reach and what can reach it. check names each difference from the
file: a netvm, a proxy that is down or runs an older config, an identity
not signed in, and hand-written policy that widens what torii set. log
gives, for each recent connection decision of a qube, its outcome and the
rule that decided it.

Why: what the owner believes a qube can reach must be what it can reach;
the live machine is the only evidence, and anything that widens it
without the file must be shown.

Scenarios:
1. `hand-policy-shown` (agent, provisional): a hand-written policy line
   granting a qube `qubes.ConnectTCP` to another appears in show and in
   check.
2. `netvm-drift-named` (agent, provisional): a qube whose netvm was changed
   by hand is named by check with the netvm the file wants.
3. `stale-config-named` (agent, provisional): a proxy running a config
   older than the file's is named by check.
4. `log-names-rule` (agent, provisional): a refused connection is in the
   qube's log with the rule that refused it.

## STK-261126-346B A qube on an egress reaches the internet only through that egress's exit nodes, or not at all

Status: planned

A carried qube whose entry names an egress sends its internet connections
and their DNS only through an exit node of that egress: the first in the
list that is healthy. While none is healthy, its internet connections are
refused and its DNS answered with a failure; nothing is sent by another
route. Switching exit nodes never clears the exit node.

Not covered: its LAN connections, which go through the proxy's netvm
directly; and connections open through an exit node when the egress
switches, which break.

Why: a qube routed through exit nodes is so routed to hide where it is;
one connection sent directly is the leak the routing existed to prevent.

Scenarios:
1. `first-healthy-exit` (agent, provisional): with the first exit node
   down and the second up, a qube's internet connection leaves through
   the second.
2. `no-exit-refused` (agent, provisional): with every exit node down, an
   internet connection is refused and no packet leaves for its
   destination.
3. `no-exit-dns-fails` (agent, provisional): with every exit node down, a
   DNS query from the qube is answered with a failure.

## STK-261126-KHZB Each tailnet identity is one qube's, joins only by a person's approval, and dom0 holds no tailnet credential

Status: planned

A qube's identity is a tailnet device of its own, with its own tags; no
two qubes share one, and an unlisted qube never has one. An egress's
shared identity carries only internet connections, never a tailnet
destination. An identity joins through a sign-in link a person approves,
or a one-time auth key given only to the proxy qube; its state lives only
in the proxy qube. dom0 stores no key or auth secret, and torii changes
nothing in the tailnet: the grants it suggests are printed.

Why: a tailnet that sees one device per machine grants every qube the
access of all; a credential in dom0, or grants a tool pushes, put the
tailnet's trust where nobody reviews it.

Scenarios:
1. `identity-per-qube` (agent, provisional): two qubes with tagged
   identities are two tailnet devices, each with only its own tags.
2. `egress-identity-internet-only` (agent, provisional): a tailnet
   destination is never dialled on an egress's identity.
3. `no-secret-in-dom0` (agent, provisional): after an identity signs in,
   by link or by key, no key or auth secret is in dom0.
4. `grants-printed` (agent, provisional): grants prints a policy and makes
   no call to the tailnet.

## STK-261126-SM33 torii adds no qrexec call, network path or dom0 exposure beyond those the file declares

Status: planned

The policy torii writes allows only: a carried qube with outbound access
to call `torii.Connect` in its own proxy; a proxy to call
`qubes.ConnectTCP` into a qube on its listed inbound ports; and dom0 and
the file's `status-readers` to call `torii.Status`. A proxy qube can call
nothing in dom0, and a carried qube with no outbound access can call
nothing. A qube with `network: none` has no netvm.

A proxy qube's kernel forwards no packet, whether the proxy program runs,
has stopped or is reloading; a config half delivered or invalid never
replaces the running one. dom0 relays no traffic and parses nothing a
qube sends but the status the owner reads. torii changes Qubes only
through the Admin API, features, policy files and services.

Accepted, and stated: a compromised proxy qube holds every identity on it
and sees the traffic it relays, as any netvm does; a qube that must be
kept apart from the others gets a dedicated proxy.

Why: torii is given dom0's power to configure compartments; every path
it opens that the file does not name is a way through them that the
owner never reviewed.

Scenarios:
1. `policy-exactly-declared` (agent, provisional): the policy apply writes
   for a file holds one allow line per declared call and no other.
2. `proxy-cannot-call-dom0` (agent, provisional): no line of the policy
   apply writes lets a proxy qube call into dom0.
3. `no-outbound-no-calls` (agent, provisional): a qube with
   `network: none` and no outbound access may make no qrexec call.
4. `forwarding-off-when-stopped` (agent, provisional): with the proxy
   program stopped, a routed qube's packet to the internet is not
   forwarded.
5. `bad-config-kept-out` (agent, provisional): a config whose hash does
   not match leaves the proxy on its running config.
