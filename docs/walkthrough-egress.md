# Egress through tailnet exit nodes

How a workload's internet traffic and DNS are confined to an ordered set
of Tailscale exit nodes, how the active exit node is selected and
switched, and why the design fails closed at two independent layers.
Components are in [`architecture.md`](architecture.md); authorization
before this point is in [`walkthrough-connection.md`](walkthrough-connection.md);
the rules are in [`claims/egress.md`](../claims/egress.md).

```yaml
egress:
  home: { exits: [home-router, vps], failback-after: 2m }
qubes:
  browse:
    network: proxy
    egress: home
    outbound: { internet: all }
```

The property:

<!-- charter:embed STK-261126-346B -->
> **[STK-261126-346B](../claims/needs.md#stk-261126-346b-a-qube-on-an-egress-reaches-the-internet-only-through-that-egresss-exit-nodes-or-not-at-all) A qube on an egress reaches the internet only through that egress's exit nodes, or not at all**
>
> A carried qube whose entry names an egress sends its internet connections
> and their DNS only through an exit node of that egress: the first in the
> list that is healthy. While none is healthy, its internet connections are
> refused and its DNS answered with a failure; nothing is sent by another
> route. Switching exit nodes never clears the exit node.
>
> Not covered: its LAN connections, which go through the proxy's netvm
> directly; and connections open through an exit node when the egress
> switches, which break.
<!-- /charter:embed -->

The threat is a leak, not a hostile workload: an exit node going down, a
withdrawn route, the program restarting or a half-written config must
never result in a packet for `browse`'s internet destination leaving the
proxy qube by its netvm.

## The egress identity

Each proxy that carries `home` runs one extra tsnet node for it, made by
[`daemon.Identities`](../proxy/internal/daemon/daemon.go#L61) with state in
`/rw/torii/state/egress-home`, hostname `<machine>-egress-home` (with
`-<proxy>` on proxies other than the main one) and the egress's tags
(`tag:<machine>-egress` by default). Every workload on `home` shares it,
and needs no identity of its own. Sharing is acceptable only because the
node is authorized for nothing but the internet:

<!-- charter:embed SW-261127-WSWG -->
> **[SW-261127-WSWG](../claims/identity.md#sw-261127-wswg-an-egresss-identity-carries-only-the-internet-and-grants-propose-it-nothing-else) An egress's identity carries only the internet, and grants propose it nothing else**
>
> An egress has one identity per proxy that carries it, shared by every
> qube on that egress; the proxy never dials a tailnet destination on it,
> since tailnet destinations are dialled only on a qube's own identity.
> `torii grants` proposes for its tags only `autogroup:internet`, and
> names its tags in no other grant.
<!-- /charter:embed -->

The routing half is [`pathIdentity`](../proxy/internal/policy/policy.go#L274):
the exit path gets the egress identity, the tailnet path only the
workload's own. The authorization half is `torii grants`, which proposes
`autogroup:internet` and nothing else for the egress tag.

## Path selection

[`PathFor`](../proxy/internal/policy/zone.go#L166) maps an allowed address to
a path by zone: an internet-zone address of a workload with an egress
takes `PathExit`; a LAN address always takes the proxy qube's netvm:

<!-- charter:embed SW-261127-KYZW -->
> **[SW-261127-KYZW](../claims/egress.md#sw-261127-kyzw-a-qubes-lan-traffic-never-goes-through-its-egress-and-plan-says-so) A qube's LAN traffic never goes through its egress, and plan says so**
>
> On an egress, a qube's connections to the LAN zone go directly through
> its proxy's netvm; only the internet zone takes the exit path. plan
> notes each qube with both an egress and LAN access, since its LAN
> traffic is not hidden by the exit node.
<!-- /charter:embed -->

So `browse` reaching a printer on `192.168.1.20` (with `outbound.lan`
allowing it) is seen with the machine's own LAN address, and a LAN-side
observer can still place the machine. `plan` prints a note for every
workload on an egress.

## DNS

For a workload on an egress, [`Engine.AnswerDNS`](../proxy/internal/engine/engine.go#L807)
resolves A and AAAA with [`ExitResolver`](../proxy/internal/engine/engine.go#L267):
a Go `net.Resolver` whose `Dial` returns a TCP connection opened through
the egress identity, so queries are DNS over TCP via the exit node and
the proxy qube's resolver is never asked. The server is the config's
`exit_dns`; the dom0 tool does not set it, so today it is always
`DefaultExitDNS`, `1.1.1.1:53` ([`Config`](../proxy/internal/config/config.go#L40)).
The resolver still reads the proxy qube's `/etc/hosts` and may append its
search domains ([SW-261127-K5TK](../claims/egress.md#sw-261127-k5tk-an-egresss-traffic-is-dialled-only-into-its-identitys-own-network-stack-never-the-system-network)).

## Dial

[`Engine.dial`](../proxy/internal/engine/engine.go#L659) hands exit-path
connections to [`Engine.dialExit`](../proxy/internal/engine/engine.go#L248),
which dials into the egress identity's gVisor netstack,
[`TSNet.Dial`](../proxy/internal/tailnet/tsnet.go#L173)
(`DialContextTCPWithBind`, bound to the identity's Tailscale address), or
[`TSNet.DialUDP`](../proxy/internal/tailnet/tsnet.go#L208) for a UDP flow:

<!-- charter:embed SW-261127-K5TK -->
> **[SW-261127-K5TK](../claims/egress.md#sw-261127-k5tk-an-egresss-traffic-is-dialled-only-into-its-identitys-own-network-stack-never-the-system-network) An egress's traffic is dialled only into its identity's own network stack, never the system network**
>
> A connection on an egress's path, its UDP flows and the DNS of a qube
> behind the egress are dialled into the userspace network stack of the
> egress's identity, from the identity's own address; the program never
> uses tsnet's `Server.Dial`, which falls back to the system network for
> an address with no tailnet route, nor the proxy qube's netvm. DNS goes
> over TCP, through the same stack, to the egress's resolver; a name in
> the proxy qube's own hosts file is answered from it, and its search
> domains may be added to a query. Without an exit node that stack has no
> route to the internet.
<!-- /charter:embed -->

The pitfall this avoids: `tsnet.Server.Dial` falls back to the host
network stack for any destination with no tailnet route. With no exit
node selected, that fallback is a direct connection from the proxy qube,
exactly the leak the egress exists to prevent. Dialling the netstack
leaves no such fallback; with no exit node, WireGuard has no peer whose
`AllowedIPs` covers the destination and the dial fails.

Each TCP dial's outcome is reported to the egress's selector
(`Selector.Report`), the passive half of health checking. UDP dials are
connectionless and report nothing.

## Health

One [`Selector`](../proxy/internal/egress/egress.go#L92) per egress, started
by [`Engine.Start`](../proxy/internal/engine/engine.go#L286).
[`Selector.Run`](../proxy/internal/egress/egress.go#L136) waits for the
identity to reach `Running`, then calls
[`Selector.Tick`](../proxy/internal/egress/egress.go#L157) every 10 s
(`Interval`), and immediately when `Report` sees three distinct failed
destinations. Each tick checks all exit nodes concurrently, each check
bounded by 5 s (`CheckTimeout`): the netmap entry (`ExitPeer`: found,
online, offering an exit node) and, if present, a TSMP ping
([`TSNet.Ping`](../proxy/internal/tailnet/tsnet.go#L370)).

<!-- charter:embed SW-261127-8DFY -->
> **[SW-261127-8DFY](../claims/egress.md#sw-261127-8dfy-an-exit-node-is-healthy-while-listed-answering-pings-and-not-failing-every-real-connection-through-it) An exit node is healthy while listed, answering pings, and not failing every real connection through it**
>
> Every 10 seconds, each check bounded to 5, every exit node is checked:
> it is healthy while the tailnet map lists it online and offering an
> exit node, and fewer than 3 pings in a row have gone unanswered. The
> active one is also marked down for the hold-down when, within 30
> seconds, 3 or more distinct destinations failed through it and none
> succeeded; and, when the egress names a `probe`, when that probe failed
> 3 times in a row. No probe runs unless the file names one.
<!-- /charter:embed -->

The trips are in [`Selector.tripLocked`](../proxy/internal/egress/egress.go#L264)
and apply to the active node only: three or more distinct destinations
failing within 30 s (`PassiveWindow`) with no success, or, if `probe:`
is set, three consecutive probe failures. A tripped node is held down
for the failback interval. The design choices behind the thresholds: the
netmap lags by tens of seconds; a TSMP ping proves WireGuard reachability
but not that the node forwards to the internet; one dead destination must
not move everyone's public address; and a default third-party probe would
emit a fingerprintable connection from every exit node.

## Selection and failback

[`choose`](../proxy/internal/egress/egress.go#L71) is a pure function of the
ordered exit list, the per-node state, the active node and the time:

<!-- charter:embed SW-261127-BNFC -->
> **[SW-261127-BNFC](../claims/egress.md#sw-261127-bnfc-exit-nodes-are-used-in-the-files-order-failing-over-at-once-and-back-only-after-a-hold-down) Exit nodes are used in the file's order, failing over at once and back only after a hold-down**
>
> An egress uses the first of its exit nodes that is healthy. When the
> active one stops being healthy it moves at once to the first healthy
> one; it moves back to one earlier in the list only once that one has
> stayed healthy for `failback-after`, two minutes unless the file says
> otherwise. With none healthy before, the first to come back is used at
> once.
<!-- /charter:embed -->

With `home-router` down, `browse` moves to `vps` in the same tick that
marks `home-router` unhealthy. When `home-router` is healthy again, it must stay so for
`failback-after` (120 s by default, `DefaultFailbackAfter`) before
`browse` moves back. Measured on the lab (TODO.md): failover in 14 s on
Headscale and 35 s on Tailscale, failback after the hold-down.

## Switching

[`TSNet.SetExitNode`](../proxy/internal/tailnet/tsnet.go#L393) is a single
`EditPrefs` setting `ExitNodeID` to the new node and
`ExitNodeAllowLANAccess` to false:

<!-- charter:embed SW-261127-6YGC -->
> **[SW-261127-6YGC](../claims/egress.md#sw-261127-6ygc-an-egresss-exit-node-is-never-cleared-a-switch-is-one-change-from-the-old-node-to-the-new) An egress's exit node is never cleared: a switch is one change from the old node to the new**
>
> The selector sets an exit node only by naming the new one, in one
> change of the identity's preferences that also keeps LAN access through
> the exit node off; it never clears it. With no exit node healthy, the
> last one stays set and the egress reports itself unusable.
<!-- /charter:embed -->

The reason is a property of stock Tailscale, not of torii: clearing the
exit node is how a client returns to direct internet access. A clear
followed by a set has a window in which the identity would route
directly, and if the set fails the window does not close. So the code
never clears it. When every node is down, `choose` returns the current
active node, the prefs are untouched, and the selector reports itself
unusable instead. Connections open through the old node break on a
switch; the need lists that as not covered.

## Fail-closed, both layers

[`Selector.Usable`](../proxy/internal/egress/egress.go#L310) is true only when
an active node is set and healthy. While it is false,
[`Engine.exitUsable`](../proxy/internal/engine/engine.go#L215) refuses every
exit-path decision before any dial:

<!-- charter:embed SW-261127-HJNX -->
> **[SW-261127-HJNX](../claims/egress.md#sw-261127-hjnx-while-an-egress-has-no-healthy-exit-node-its-connections-are-refused-at-once-and-its-dns-fails) While an egress has no healthy exit node, its connections are refused at once and its DNS fails**
>
> Before dialling, the program refuses a connection on an egress's path
> while the egress has no healthy exit node, with `egress-no-healthy-exit`
> and the egress's identity, and dials nothing. A DNS question that would
> be answered through the egress gets SERVFAIL, not "no such name", and is
> asked nowhere.
<!-- /charter:embed -->

The two layers are independent. If the selector is wrong and calls a dead
node healthy, the netstack dial has no route and fails. If the netstack
had a route for some reason, the engine still refuses while the selector
reports no healthy node. Neither path touches the proxy qube's netvm, and
the guard forbids kernel forwarding regardless. Verified on the lab with
both exits killed: refusals in under 20 ms, `egress-no-healthy-exit`
logged, and no packet to the destination in a capture at the netvm.

## Observability

The selector emits egress events (`exit-switch`, `exit-unhealthy`,
`exit-healthy`) under the workload name `egress:<name>`;
`torii log --egress home` prints them. `torii show` lists each egress's
active node and every node's state with the reason it is down. `torii
check` reports an egress with no healthy node, and one running on a
fallback with how long ([SW-261127-SMYC](../claims/seeing.md#sw-261127-smyc-check-names-each-difference-it-looks-for-between-the-live-machine-and-the-file-and-fails-on-one)). Setting up the exit nodes
and the grant: [`tailscale.md`](tailscale.md#exit-nodes).
