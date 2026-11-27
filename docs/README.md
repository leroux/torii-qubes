# torii's docs

torii is per-qube network policy for Qubes OS. A machine file in dom0
declares, for each qube, its link (no netvm with qrexec-only access, or a
proxy qube as netvm), its outbound allowlist by zone (internet, LAN,
tailnet), its inbound ports, its egress through Tailscale exit nodes,
and an optional tailnet identity of its own. A dom0 tool plans and
applies that file through the Admin API and qrexec policy; a proxy qube,
`sys-torii`, runs `torii-proxy`, which attributes, authorizes and relays
every connection in userspace with kernel forwarding off and holds one
tsnet node per identity; and `show` and `check` reconstruct each qube's
reach from the live machine. The [README](../README.md) is the user-facing
overview; this directory is the engineering documentation.

## What torii guarantees

The record in [`claims/`](../claims/needs.md) holds what binds. Its six
needs, and the docs that follow each:

| Need | Read |
|---|---|
| [STK-261126-KZK8: torii MUST relay a carried qube's connection only if the qube's entry allows it, and MUST refuse every other connection](../claims/needs.md#stk-261126-kzk8-torii-must-relay-a-carried-qubes-connection-only-if-the-qubes-entry-allows-it-and-must-refuse-every-other-connection) | [a connection](walkthrough-connection.md), [design](design.md#allowlists-by-name-not-address) |
| [STK-261126-B4CP: torii apply MUST change only what torii owns, and MUST make exactly the steps torii plan showed](../claims/needs.md#stk-261126-b4cp-torii-apply-must-change-only-what-torii-owns-and-must-make-exactly-the-steps-torii-plan-showed) | [plan and apply](walkthrough-apply.md), [setup](setup.md) |
| [STK-261126-4Q75: torii show MUST report each qube's reach from the live machine, and torii check MUST name every difference from the file](../claims/needs.md#stk-261126-4q75-torii-show-must-report-each-qubes-reach-from-the-live-machine-and-torii-check-must-name-every-difference-from-the-file) | [plan and apply](walkthrough-apply.md#7-reading-it-back) |
| [STK-261126-346B: torii MUST send an egress qube's internet traffic only through that egress's exit nodes, and MUST refuse it when none is healthy](../claims/needs.md#stk-261126-346b-torii-must-send-an-egress-qubes-internet-traffic-only-through-that-egresss-exit-nodes-and-must-refuse-it-when-none-is-healthy) | [an egress](walkthrough-egress.md), [the tailnet side](tailscale.md#exit-nodes) |
| [STK-261126-KHZB: torii MUST give each tailnet identity to one qube, join it only with a person's approval, and keep no tailnet credential in dom0](../claims/needs.md#stk-261126-khzb-torii-must-give-each-tailnet-identity-to-one-qube-join-it-only-with-a-persons-approval-and-keep-no-tailnet-credential-in-dom0) | [the tailnet side](tailscale.md), [architecture](architecture.md#torii-proxy) |
| [STK-261126-SM33: torii MUST NOT add a qrexec call, network path or dom0 exposure beyond those the file declares](../claims/needs.md#stk-261126-sm33-torii-must-not-add-a-qrexec-call-network-path-or-dom0-exposure-beyond-those-the-file-declares) | [architecture](architecture.md), [design](design.md#what-a-compromise-reaches) |

## The docs

| Doc | For | What it is |
|---|---|---|
| [`setup.md`](setup.md) | someone installing torii | From nothing to one service, then each pattern; every command and file key |
| [`tailscale.md`](tailscale.md) | the tailnet's admin | Tags, grants, HTTPS, sign-in, exit nodes, Headscale |
| [`salt.md`](salt.md) | someone running many machines | The states and pillar that apply a file per machine |
| [`architecture.md`](architecture.md) | a contributor or reviewer | Trust model and TCB, components, units, sockets, policy, trust boundaries |
| [`walkthrough-connection.md`](walkthrough-connection.md) | a contributor or reviewer | Interception and qrexec ingress, source attribution, DNS, authorization, SNI-checked relay, UDP, inbound |
| [`walkthrough-apply.md`](walkthrough-apply.md) | a contributor or reviewer | Parsing, snapshot, the pure planner, apply's steps, chunked config delivery, readiness, show and check |
| [`walkthrough-egress.md`](walkthrough-egress.md) | a contributor or reviewer | The egress identity, exit-path DNS and dials, health, selection, switching, the two fail-closed layers |
| [`design.md`](design.md) | anyone asking why | The problem, the choices and the alternatives, what a compromise reaches, the limits |
| [`testing.md`](testing.md) | a contributor | What each level of tests shows, and what only a real machine does |
| [`performance.md`](performance.md) | anyone sizing a machine | What was measured, and why the numbers are what they are |

## In what order

- **To use it:** the [README](../README.md), [`setup.md`](setup.md), then
  [`tailscale.md`](tailscale.md) at the steps that point there.
- **To review its security:** the needs above and their adversary
  ([`claims/needs.md`](../claims/needs.md)), [`architecture.md`](architecture.md)
  (trust model, TCB, boundaries), [`design.md`](design.md) (what a
  compromise reaches, limits), then the walkthroughs for the code paths:
  [`walkthrough-connection.md`](walkthrough-connection.md) for attribution
  and authorization, [`walkthrough-egress.md`](walkthrough-egress.md) for
  fail-closed routing, [`walkthrough-apply.md`](walkthrough-apply.md) for
  dom0.
- **To change it:** [`architecture.md`](architecture.md), the three
  walkthroughs, [`testing.md`](testing.md), then
  [`../HACKING.md`](../HACKING.md) for the dev setup. A change to what
  torii does is a change to an entry in `claims/` first.

Every link here to an entry or to code is checked by `charter check`,
which warns when something a doc names has changed since a person last
read it.
