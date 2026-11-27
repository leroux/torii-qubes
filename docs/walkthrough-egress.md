# How traffic leaves through an egress

A walkthrough of a qube whose internet goes out through tailnet exit
nodes: how its connections and its DNS are sent there and nowhere else,
how the exit node is chosen and changed, and what happens when none is
up. The parts are in [`architecture.md`](architecture.md), the deciding
of a connection before this point in
[`walkthrough-connection.md`](walkthrough-connection.md), and the rules in
[`claims/egress.md`](../claims/egress.md).

The example:

```yaml
egress:
  home: { exits: [home-router, vps] }
qubes:
  browse:
    network: proxy
    egress: home
    outbound: { internet: all }
```

What it is for:

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

## The identity it leaves on

`browse` has no tailnet identity of its own, and needs none. The proxy
carrying it holds one identity for the egress `home`, a tailnet device
made by [`daemon.Identities`](../proxy/internal/daemon/daemon.go#L61) beside
the qubes' own, and every qube on `home` shares it. Sharing it is
harmless only because it carries nothing but the internet:

<!-- charter:embed SW-261127-WSWG -->
> **[SW-261127-WSWG](../claims/identity.md#sw-261127-wswg-an-egresss-identity-carries-only-the-internet-and-grants-propose-it-nothing-else) An egress's identity carries only the internet, and grants propose it nothing else**
>
> An egress has one identity per proxy that carries it, shared by every
> qube on that egress; the proxy never dials a tailnet destination on it,
> since tailnet destinations are dialled only on a qube's own identity.
> `torii grants` proposes for its tags only `autogroup:internet`, and
> names its tags in no other grant.
<!-- /charter:embed -->

In the decision this is [`pathIdentity`](../proxy/internal/policy/policy.go#L274):
the exit path gets the egress's identity, the tailnet path only the
qube's own.

## Which way each connection goes

The zone of the address dialled decides the path,
[`PathFor`](../proxy/internal/policy/zone.go#L166): an internet address of a
qube on an egress takes the exit path; a LAN address always goes through
the proxy's netvm:

<!-- charter:embed SW-261127-KYZW -->
> **[SW-261127-KYZW](../claims/egress.md#sw-261127-kyzw-a-qubes-lan-traffic-never-goes-through-its-egress-and-plan-says-so) A qube's LAN traffic never goes through its egress, and plan says so**
>
> On an egress, a qube's connections to the LAN zone go directly through
> its proxy's netvm; only the internet zone takes the exit path. plan
> notes each qube with both an egress and LAN access, since its LAN
> traffic is not hidden by the exit node.
<!-- /charter:embed -->

So `browse` printing to a printer on `192.168.1.20` does so from the
machine's own network, not from the exit node's; `torii plan` says so in a
note, for every qube on an egress.

## The name

`browse` asks for `example.org`. [`Engine.AnswerDNS`](../proxy/internal/engine/engine.go#L807)
decides the name as for any qube, then, the path being the exit path,
resolves it with [`ExitResolver`](../proxy/internal/engine/engine.go#L267): DNS
over TCP, dialled through the egress's identity like any connection on it.
The local resolver is never asked. Today that resolver is the config's
`exit_dns`, which the dom0 tool does not set, so it is always the
default, `1.1.1.1:53` ([`Config`](../proxy/internal/config/config.go#L40)),
reached through the exit node.

## The dial

[`Engine.dial`](../proxy/internal/engine/engine.go#L659) sends an exit-path
connection to [`Engine.dialExit`](../proxy/internal/engine/engine.go#L248),
which dials into the egress identity's own network stack,
[`TSNet.Dial`](../proxy/internal/tailnet/tsnet.go#L173), bound to the
identity's tailnet address:

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

This is the trap the code is shaped against. tsnet's `Server.Dial` looks
like the obvious call, and for an address with no tailnet route it falls
back to the system network: with no exit node set, that would send the
connection straight out of the proxy qube. Dialling into the netstack
instead means there is no such fallback to get wrong; with no exit node,
WireGuard has nowhere to send the packets. A UDP flow on the exit path
goes the same way, [`TSNet.DialUDP`](../proxy/internal/tailnet/tsnet.go#L208).

Every TCP dial also reports how it went to the egress's selector,
`Report`, which is half of how an exit node is judged.

## Choosing the exit node

Each egress has a [`Selector`](../proxy/internal/egress/egress.go#L92),
started by [`Engine.Start`](../proxy/internal/engine/engine.go#L286). Once the
identity has signed in, [`Selector.Run`](../proxy/internal/egress/egress.go#L136)
calls [`Selector.Tick`](../proxy/internal/egress/egress.go#L157) every ten
seconds, and at once when real connections start failing. A tick checks
every exit node at the same time, each within five seconds:

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

The two halves of "not failing every real connection" are in
[`Selector.tripLocked`](../proxy/internal/egress/egress.go#L264): three
distinct destinations failing within thirty seconds with none succeeding,
or, if the egress has a `probe`, three failed probes in a row, mark the
active node down for the hold-down. One dead website does not move
anyone's address.

Then [`choose`](../proxy/internal/egress/egress.go#L71) picks the node:

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

With `home-router` down and `vps` up, `browse` leaves through `vps`. When
`home-router` comes back, it must stay healthy for `failback-after`, two
minutes by default, before `browse` moves back, so a flapping home line
does not change the qube's address every minute.

## Switching without a gap

The switch itself is one call,
[`TSNet.SetExitNode`](../proxy/internal/tailnet/tsnet.go#L393), one change of
the identity's prefs from the old node to the new:

<!-- charter:embed SW-261127-6YGC -->
> **[SW-261127-6YGC](../claims/egress.md#sw-261127-6ygc-an-egresss-exit-node-is-never-cleared-a-switch-is-one-change-from-the-old-node-to-the-new) An egress's exit node is never cleared: a switch is one change from the old node to the new**
>
> The selector sets an exit node only by naming the new one, in one
> change of the identity's preferences that also keeps LAN access through
> the exit node off; it never clears it. With no exit node healthy, the
> last one stays set and the egress reports itself unusable.
<!-- /charter:embed -->

The trap again: for a stock Tailscale client, clearing the exit node is
exactly the switch back to going out directly. So the code never clears
it, not even when every node is down; `choose` keeps the active one, and
the selector reports itself unusable instead. Connections open through
the old node when it switches break; that is stated in the need, above,
as not covered.

## When none is up

While no exit node is healthy,
[`Selector.Usable`](../proxy/internal/egress/egress.go#L310) is false, and the
engine refuses before dialling,
[`Engine.exitUsable`](../proxy/internal/engine/engine.go#L215):

<!-- charter:embed SW-261127-HJNX -->
> **[SW-261127-HJNX](../claims/egress.md#sw-261127-hjnx-while-an-egress-has-no-healthy-exit-node-its-connections-are-refused-at-once-and-its-dns-fails) While an egress has no healthy exit node, its connections are refused at once and its DNS fails**
>
> Before dialling, the program refuses a connection on an egress's path
> while the egress has no healthy exit node, with `egress-no-healthy-exit`
> and the egress's identity, and dials nothing. A DNS question that would
> be answered through the egress gets SERVFAIL, not "no such name", and is
> asked nowhere.
<!-- /charter:embed -->

So failing closed holds twice over: the engine refuses an exit-path
connection while the egress is unusable, and if it dialled one anyway,
the network stack would have nowhere to send it.

## Seeing it

The selector logs each switch, and each node going down and coming back,
as events of the egress's own; `torii log --egress home` prints them, and
`torii show` gives each egress's active node and the state of the others.
`torii check` names an egress with no healthy exit node
([SW-261127-SMYC](../claims/seeing.md#sw-261127-smyc-check-names-each-difference-it-looks-for-between-the-live-machine-and-the-file-and-fails-on-one)). How to set up the exit nodes themselves is in
[`tailscale.md`](tailscale.md#exit-nodes).
