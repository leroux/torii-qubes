# Tailnet identity

Code: proxy/internal/tailnet

The need this area answers is STK-261126-KHZB. It holds a qube's tailnet
identity and an egress's shared one: one device each, where their state
lives, how they sign in, and the grants torii proposes and never pushes.

## SW-261127-WNGN Each identity is a tailnet device of its own, with its own name and state

Status: active
Kind: invariant
Serves: STK-261126-KHZB

Every qube with a tailnet identity, every service, and every egress on
each proxy that carries it is its own tailnet node in its proxy qube,
with its own hostname and its own state directory and key file in the
proxy qube's private volume, kept under the qube's name, `<qube>.<service>`
or `egress-<egress>`. The file is refused when two identities would
share a hostname or one of those names, and so is a proxy config that
would. A qube's hostname is by default its name prefixed by the
machine's; a service's is its bare name; an egress on a proxy other
than the main one has the proxy's name added. torii never reads an
identity's state back; a rebuilt proxy gets new identities and new
sign-ins.

Why: the tailnet's access rules apply per device; one device for
several qubes would grant each the access of all, the compartment
merging torii exists to prevent.

Scenarios:
1. `one-node-each` (agent, provisional): each identity gets a node with
   its own hostname and state directory, and two that would share a
   state are refused.

Changes:
- 2026-11-27: a qube named egress-<egress>, or <qube>.<service>, shared
  another identity's state and key; both sides now refuse it, and the text
  says how each kind is named and that torii never reads state back.

## SW-261127-P8H7 A tagged identity carries exactly the file's tags, prefixed by the machine, and check names any other

Status: active
Kind: invariant
Serves: STK-261126-KHZB

A tagged identity's tags are the short tags the file gives it, each
prefixed by the machine's name (`server` on `m1` is `tag:m1-server`),
and `tag:<machine>`; a service's tags may also be written in full, to be
shared across machines; a person's identity has none. The file refuses a full
tag on a qube. check names an identity whose tags on the tailnet differ
from the file's, as the proxy reports it.

Why: tags are what the tailnet's grants select on; a qube carrying a
tag the file did not give it reaches what that tag reaches, so tags come
only from the file, and a difference is a finding.

Scenarios:
1. `tags-prefixed` (agent, provisional): a qube's short tag becomes the
   machine-prefixed tag, with the machine's own tag beside it.
2. `mismatch-named` (agent, provisional): check names an identity the
   proxy reports with other tags than the file's.

Changes:
- 2026-11-27: check read a field the proxy never sent, so it named no tag
  mismatch on a real machine; the proxy now reports it in that field.

## SW-261127-3TPX An identity signs in by a link a person approves, or a one-time key that only the proxy qube keeps

Status: active
Kind: invariant
Serves: STK-261126-KHZB

A new identity asks the tailnet for a sign-in link, which apply, show
and check print for a person to approve. `torii sign-in-key` takes a
one-time key on dom0's standard input and writes it straight into the
proxy qube, as a new file under a directory only root can read, then
restarts the
proxy with that identity's old state removed so it signs in with the
key; dom0 writes the key to no file. After the first sign-in the
identity's own state keeps it signed in.

Why: dom0 must hold no tailnet credential, since dom0 compromised is
everything compromised; a link needs no secret at all, and a key that
passes through dom0 only on its way to the one qube that uses it leaves
nothing behind.

Scenarios:
1. `key-to-proxy-only` (agent, provisional): sign-in-key sends the key to
   the proxy qube's root shell as input, writes no file in dom0, and puts
   it under the root-only key directory.
2. `links-printed` (agent, provisional): an identity waiting for sign-in
   is listed with its link.

Changes:
- 2026-11-27: an existing key file kept its mode; the file is now removed
  and written new.

## SW-261127-DH6R torii proposes grants for the tailnet and never changes its policy

Status: active
Kind: invariant
Serves: STK-261126-KHZB

`torii grants` prints the tag owners and grants the file implies, for a
person to review and paste into the tailnet's policy: an inbound
identity's or a service's source is `autogroup:member`, to be narrowed,
and an egress's its own tags; the machine's own tag is never a
destination. The dom0 tool's own process makes no network connection,
so it cannot change the tailnet's policy or grants; it changes the
tailnet only by an identity signing in, with a link a person approves
or a key the person gives. What it runs elsewhere does reach a network:
`qvm-template` downloads the base template, and package installs run in
torii's template.

Why: the tailnet's policy is the second half of every qube's access; a
tool that wrote it would be a second, unreviewed source of the same
rules, and one reachable from dom0.

Scenarios:
1. `grants-printed` (agent, provisional): grants prints a grant per
   inbound identity and service, and the tag owners of every tag used.
2. `dom0-no-network` (agent, provisional): no module of the dom0 tool
   imports a networking library.

Changes:
- 2026-11-27: the text said dom0 could not change the tailnet's devices or
  keys, though sign-in-key signs a device in, and did not say what it runs
  elsewhere; grants no longer name the machine's tag, which every egress
  carries, as a destination; the scenario checks dynamic imports and
  download tools too.

## SW-261127-WSWG An egress's identity carries only the internet, and grants propose it nothing else

Status: active
Kind: invariant
Serves: STK-261126-KHZB, STK-261126-346B

An egress has one identity per proxy that carries it, shared by every
qube on that egress; the proxy never dials a tailnet destination on it,
since tailnet destinations are dialled only on a qube's own identity.
`torii grants` proposes for its tags only `autogroup:internet`, and
names its tags in no other grant.

Why: sharing a path out is harmless, since every qube behind one exit
node shares its address anyway; sharing tailnet authorisation is how
compartments merge, so an egress's identity has none.

Scenarios:
1. `no-tailnet-on-egress` (agent, provisional): a qube with no identity
   of its own, on an egress, is refused every tailnet address.
2. `grants-internet-only` (agent, provisional): grants proposes the
   egress's tags `autogroup:internet` and nothing else.

Changes:
- 2026-11-27: grants named the machine's tag, which the egress identities
  carry, as every inbound grant's destination; it no longer does.

## SW-261127-8PTP An identity sends Tailscale no client logs unless the file asks, and never maps ports

Status: active
Kind: invariant
Serves: STK-261126-KHZB

Client log uploads to Tailscale are off, and the control server is told
so, unless the file sets `tailnet.tailscale-logs: yes`; turning them on
restarts the program, since off is for the life of the process. The
port mapper (UPnP, NAT-PMP, PCP) is always off.

Why: log uploads tell a third party about every identity's activity,
and the port mapper only broadcasts behind Qubes' NAT; neither is sent
unless the owner asks for the first.

Scenarios:
1. `logs-off-by-default` (agent, provisional): with no setting, the
   program turns log uploads and the port mapper off.
2. `logs-on-asked` (agent, provisional): the file's setting reaches the
   proxy's config, and turning logs on later asks for a restart.
