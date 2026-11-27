# torii's docs

torii decides, for each qube on a Qubes machine, what it may connect to,
what may connect to it, and which way its traffic leaves. One file in dom0
declares it; a tool in dom0 makes Qubes match the file; a proxy qube,
`sys-torii`, decides and relays every connection and gives each qube a
tailnet identity of its own; and dom0 reads the result back from the live
machine. The [README](../README.md) is the front page; these docs are the
rest.

## What torii guarantees

The record in [`claims/`](../claims/needs.md) holds what binds. Its six
needs, and the docs that follow each:

| Need | Read |
|---|---|
| [STK-261126-KZK8: A carried qube's connection is relayed only if its entry allows it; every other connection is refused](../claims/needs.md#stk-261126-kzk8-a-carried-qubes-connection-is-relayed-only-if-its-entry-allows-it-every-other-connection-is-refused) | [a connection](walkthrough-connection.md), [design](design.md#allowlists-by-name-not-address) |
| [STK-261126-B4CP: apply changes only what torii owns, and exactly the steps plan showed](../claims/needs.md#stk-261126-b4cp-apply-changes-only-what-torii-owns-and-exactly-the-steps-plan-showed) | [plan and apply](walkthrough-apply.md), [setup](setup.md) |
| [STK-261126-4Q75: show reads each qube's reach from the live machine; check names every difference from the file](../claims/needs.md#stk-261126-4q75-show-reads-each-qubes-reach-from-the-live-machine-check-names-every-difference-from-the-file) | [plan and apply](walkthrough-apply.md#7-reading-it-back) |
| [STK-261126-346B: A qube on an egress reaches the internet only through that egress's exit nodes, or not at all](../claims/needs.md#stk-261126-346b-a-qube-on-an-egress-reaches-the-internet-only-through-that-egresss-exit-nodes-or-not-at-all) | [an egress](walkthrough-egress.md), [the tailnet side](tailscale.md#exit-nodes) |
| [STK-261126-KHZB: Each tailnet identity is one qube's, joins only by a person's approval, and dom0 holds no tailnet credential](../claims/needs.md#stk-261126-khzb-each-tailnet-identity-is-one-qubes-joins-only-by-a-persons-approval-and-dom0-holds-no-tailnet-credential) | [the tailnet side](tailscale.md), [architecture](architecture.md#torii-proxy) |
| [STK-261126-SM33: torii adds no qrexec call, network path or dom0 exposure beyond those the file declares](../claims/needs.md#stk-261126-sm33-torii-adds-no-qrexec-call-network-path-or-dom0-exposure-beyond-those-the-file-declares) | [architecture](architecture.md), [design](design.md#what-a-compromise-reaches) |

## The docs

| Doc | For | What it is |
|---|---|---|
| [`setup.md`](setup.md) | someone installing torii | From nothing to one service, then each pattern; every command and file key |
| [`tailscale.md`](tailscale.md) | the tailnet's admin | Tags, grants, HTTPS, sign-in, exit nodes, Headscale |
| [`salt.md`](salt.md) | someone running many machines | The states and pillar that apply a file per machine |
| [`architecture.md`](architecture.md) | a contributor or reviewer | The parts, what runs where, what crosses each boundary |
| [`walkthrough-connection.md`](walkthrough-connection.md) | a contributor | One connection, from the name to the last byte |
| [`walkthrough-apply.md`](walkthrough-apply.md) | a contributor or reviewer | A plan and an apply, from the file to the proxy running it |
| [`walkthrough-egress.md`](walkthrough-egress.md) | a contributor | Traffic through exit nodes, failover, failing closed |
| [`design.md`](design.md) | anyone asking why | The problem, the choices and the alternatives, what a compromise reaches, the limits |
| [`testing.md`](testing.md) | a contributor | What each level of tests shows, and what only a real machine does |
| [`performance.md`](performance.md) | anyone sizing a machine | What was measured, and why the numbers are what they are |

## In what order

- **To use it:** the [README](../README.md), [`setup.md`](setup.md), then
  [`tailscale.md`](tailscale.md) at the steps that point there.
- **To review it:** [`design.md`](design.md), [`architecture.md`](architecture.md),
  then [`walkthrough-apply.md`](walkthrough-apply.md) for what dom0 does
  and the needs above.
- **To change it:** [`architecture.md`](architecture.md), the three
  walkthroughs, [`testing.md`](testing.md), then
  [`../HACKING.md`](../HACKING.md) for the dev setup. A change to what
  torii does is a change to an entry in `claims/` first.

Every link here to an entry or to code is checked by `charter check`,
which warns when something a doc names has changed since a person last
read it.
