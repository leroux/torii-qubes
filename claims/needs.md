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

The key words MUST, MUST NOT, SHOULD, SHOULD NOT and MAY in these
documents are to be interpreted as described in BCP 14 (RFC 2119, RFC 8174)
when, and only when, they appear in all capitals.

## STK-261126-KZK8 torii MUST relay a carried qube's connection only if the qube's entry allows it, and MUST refuse every other connection

Status: planned

- An outbound connection from a carried qube MUST be relayed only when
  its entry allows the connection's zone and a destination in that zone
  matches it by name or address, port and protocol. A name entry counts
  only for a name the qube asked for, or resolved to that address.
- A tailnet connection MUST also be allowed by the tailnet's grants.
- An inbound connection MUST reach a carried qube only on a port its
  entry or a service lists, and only from the qubes its `local-inbound`
  names or the tailnet peers the grants allow.
- Whatever the file says, the proxy MUST NOT make another qube's address
  reachable, and a carried qube MUST NOT have IPv6.
- A qube attached to a proxy but not listed, a disposable included, MUST
  get that proxy's `unlisted` policy, which by default refuses
  everything, and MUST NOT get a tailnet identity.

Not covered, and documented as limits: raw TCP or QUIC to another site at
an address an allowed name shares, including any connection that sends
no TLS server name; and data sent out over DNS by a qube allowed
`internet: all`. A qube with `network: default` or its own netvm is not
carried: torii sets only its netvm.

Why: a qube that has a netvm and is fenced in by firewall rules is one
missing rule away from reaching everything. Deciding each connection by
the destination the file declares, and refusing whatever the file does
not declare, makes access between "no network" and "full network" safe
to grant, because a mistake in the file refuses traffic instead of
leaking it.

Scenarios:
1. `allowed-name-passes` (agent, provisional): given a qube allowed
   `pypi.org:443`, when it connects to pypi.org on 443, on 80 and to
   another name, then only the connection to 443 is relayed.
2. `unresolved-address-refused` (agent, provisional): given a public
   address the qube never resolved and no address entry covers, when the
   qube connects to it, then the connection is refused.
3. `sni-must-match` (agent, provisional): given a qube allowed a name,
   when it opens a TLS connection to that name's address with a server
   name that is not allowed, then the connection is refused.
4. `udp-needs-udp` (agent, provisional): given an entry for a name
   without `/udp`, when the qube sends UDP to that name, then the flow is
   refused.
5. `unlisted-port-refused` (agent, provisional): given a port the qube's
   entry does not list, when a connection arrives for that port, then it
   never reaches the qube.
6. `other-qubes-unreachable` (agent, provisional): given a qube allowed
   `internet: all`, when it connects to another qube's address, then the
   connection is refused.
7. `unlisted-qube-refused` (agent, provisional): given a qube attached to
   the proxy but absent from the file, when it opens any connection, then
   the connection is refused.

## STK-261126-B4CP torii apply MUST change only what torii owns, and MUST make exactly the steps torii plan showed

Status: planned

- `torii plan` MUST be a function of the machine file and a snapshot of
  the live machine, and MUST list every change in the order apply makes
  it. On a machine that matches the file, the plan MUST be empty.
- `torii apply` MUST make those changes and no others, MUST stop at the
  first change that fails, and, run again, MUST make only the changes
  left.
- torii owns its template, the proxy qubes it created, its policy file
  `/etc/qubes/policy.d/50-torii.policy` (`50-torii-<mesh>.policy` for a
  named mesh), the shared deny file `59-torii-deny.policy`, and, on each
  managed qube, only its netvm, the `torii-client` service and feature,
  its torii tags and the adoption features it writes once. torii MUST NOT
  create or delete any other qube, and MUST NOT write any other policy
  file.
- A qube removed from the file MUST be left with no netvm, never its old
  one.

Why: torii runs with dom0's power over every qube, so the owner has to
be able to review what it will do before it does it. A plan that is the
whole of what apply will change, and that can touch only what torii
owns, is something the owner can actually review; any change outside the
plan, or outside torii's own objects, would bypass that review.

Scenarios:
1. `plan-is-apply` (agent, provisional): given a machine and a file,
   when apply runs, then the steps it makes are the steps plan listed,
   in the same order.
2. `second-apply-empty` (agent, provisional): given a machine apply has
   just made match the file, when plan runs, then it lists no steps.
3. `interrupted-apply-continues` (agent, provisional): given an apply
   stopped by a failed step, when apply runs again, then it makes only
   the steps not yet made.
4. `unowned-untouched` (agent, provisional): given a qube not in the file
   and a hand-written policy file, when apply runs, then neither changes.
5. `removed-qube-gets-no-netvm` (agent, provisional): given a managed
   qube removed from the file, when apply runs, then the qube ends with
   no netvm.

Changes:
- 2026-11-27: torii also owns a named mesh's policy file and the shared
  deny file, as SW-261127-G26T rules and apply writes; the need named
  `50-torii.policy` alone.

## STK-261126-4Q75 torii show MUST report each qube's reach from the live machine, and torii check MUST name every difference from the file

Status: planned

- `torii show` MUST read each managed qube's netvm, every policy file in
  dom0 (hand-written ones included), the config each proxy qube is
  running and each identity's state, never the file, and MUST report
  from them what the qube can reach and what can reach it.
- `torii check` MUST name each difference from the file: a netvm, a
  proxy that is down or runs an older config, an identity not signed in,
  and hand-written policy that widens what torii set.
- `torii log` MUST give, for each recent connection decision of a qube,
  its outcome and the rule that decided it.

Why: the owner's decisions about a qube depend on what they believe it
can reach, so that belief has to match what it can actually reach. The
file says what was intended; only the live machine shows the result,
including anything changed by hand. Reading the machine, and naming
whatever widens reach beyond the file, is what lets the owner trust the
picture.

Scenarios:
1. `hand-policy-shown` (agent, provisional): given a hand-written policy
   line granting a qube `qubes.ConnectTCP` to another, when show and
   check run, then both name it.
2. `netvm-drift-named` (agent, provisional): given a qube whose netvm was
   changed by hand, when check runs, then it names the qube and the netvm
   the file wants.
3. `stale-config-named` (agent, provisional): given a proxy running a
   config older than the file's, when check runs, then it names the
   proxy.
4. `log-names-rule` (agent, provisional): given a refused connection,
   when the qube's log is read, then it holds the connection with the
   rule that refused it.

## STK-261126-346B torii MUST send an egress qube's internet traffic only through that egress's exit nodes, and MUST refuse it when none is healthy

Status: planned

- A carried qube whose entry names an egress MUST send its internet
  connections, and their DNS, only through an exit node of that egress:
  the first in the list that is healthy.
- While no exit node is healthy, its internet connections MUST be
  refused and its DNS queries MUST be answered with a failure; torii
  MUST NOT send any of it by another route.
- Switching exit nodes MUST NOT clear the exit node.

Not covered: the qube's LAN connections, which go directly through the
proxy's netvm; and connections open through an exit node when the egress
switches, which break.

Why: a qube is routed through exit nodes to hide where it is, so a
single connection sent directly reveals exactly what the routing exists
to hide. Refusing traffic while no exit node is healthy, instead of
falling back to the direct route, keeps a failure from becoming that
leak.

Scenarios:
1. `first-healthy-exit` (agent, provisional): given the first exit node
   down and the second up, when the qube opens an internet connection,
   then it leaves through the second.
2. `no-exit-refused` (agent, provisional): given every exit node down,
   when the qube opens an internet connection, then it is refused and no
   packet leaves for its destination.
3. `no-exit-dns-fails` (agent, provisional): given every exit node down,
   when the qube sends a DNS query, then it is answered with a failure.

## STK-261126-KHZB torii MUST give each tailnet identity to one qube, join it only with a person's approval, and keep no tailnet credential in dom0

Status: planned

- A qube's identity MUST be a tailnet device of its own, with its own
  tags. Two qubes MUST NOT share one, and an unlisted qube MUST NOT have
  one.
- An egress's shared identity MUST carry only internet connections,
  never a tailnet destination.
- An identity MUST join through a sign-in link a person approves, or a
  one-time auth key given only to the proxy qube, and its state MUST
  live only in the proxy qube.
- dom0 MUST NOT store a key or auth secret, and torii MUST NOT change
  anything in the tailnet: the grants it suggests are printed.

Why: a tailnet that sees one device per machine gives every qube on the
machine the access of all of them, which undoes the separation between
qubes. A credential stored in dom0, or grants that a tool pushes, would
place the tailnet's trust somewhere no person reviews it; requiring a
person to approve each identity and to apply the grants keeps that
decision with the owner.

Scenarios:
1. `identity-per-qube` (agent, provisional): given two qubes with tagged
   identities, when both sign in, then they are two tailnet devices,
   each with only its own tags.
2. `egress-identity-internet-only` (agent, provisional): given a qube on
   an egress, when it connects to a tailnet destination, then the
   egress's identity is not used to dial it.
3. `no-secret-in-dom0` (agent, provisional): given an identity that has
   signed in, by link or by key, when dom0 is searched, then it holds no
   key or auth secret.
4. `grants-printed` (agent, provisional): given a machine file, when
   `torii grants` runs, then it prints a policy and makes no call to the
   tailnet.

## STK-261126-SM33 torii MUST NOT add a qrexec call, network path or dom0 exposure beyond those the file declares

Status: planned

- The policy torii writes MUST allow only: a carried qube with outbound
  access calling `torii.Connect` in its own proxy; a proxy calling
  `qubes.ConnectTCP` into a qube on its listed inbound ports; and dom0
  and the file's `status-readers` calling `torii.Status`.
- A proxy qube MUST NOT be able to call anything in dom0, and a carried
  qube with no outbound access MUST NOT be able to call anything.
- A qube with `network: none` MUST NOT have a netvm.
- A proxy qube's kernel MUST NOT forward any packet, whether the proxy
  program is running, stopped or reloading. A config that is half
  delivered or invalid MUST NOT replace the running one.
- dom0 MUST NOT relay traffic, and MUST NOT parse anything a qube sends
  except the status the owner reads.
- torii MUST change Qubes only through the Admin API, features, policy
  files and services.

Accepted, and stated: a compromised proxy qube holds every identity on it
and sees the traffic it relays, as any netvm does; a qube that must be
kept apart from the others gets a dedicated proxy.

Why: torii uses dom0's power to configure the compartments Qubes keeps
apart. Any qrexec call, network path or dom0 exposure it adds without
the file naming it is a path between compartments that the owner never
reviewed, so the set of paths has to be exactly what the file declares.

Scenarios:
1. `policy-exactly-declared` (agent, provisional): given a machine file,
   when apply writes the policy, then it holds one allow line per
   declared call and no other.
2. `proxy-cannot-call-dom0` (agent, provisional): given the policy apply
   writes, when its lines are read, then none lets a proxy qube call
   into dom0.
3. `no-outbound-no-calls` (agent, provisional): given a qube with
   `network: none` and no outbound access, when it makes any qrexec
   call, then the call is denied.
4. `forwarding-off-when-stopped` (agent, provisional): given the proxy
   program stopped, when a routed qube sends a packet to the internet,
   then it is not forwarded.
5. `bad-config-kept-out` (agent, provisional): given a config whose hash
   does not match, when the proxy reads it, then the proxy stays on its
   running config.
