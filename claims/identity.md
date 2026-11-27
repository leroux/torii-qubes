# Tailnet identity

Code: proxy/internal/tailnet

This area answers STK-261126-KHZB. It covers a qube's tailnet identity
and an egress's shared one: one device each, where their state lives,
how they sign in, and the grants torii proposes and never pushes.

## SW-261127-WNGN The proxy MUST run each identity as a tailnet device of its own, with its own name and state

Status: active
Kind: invariant
Serves: STK-261126-KHZB

- Every qube with a tailnet identity, every service, and every egress on
  each proxy that carries it MUST be its own tailnet node in its proxy
  qube, with its own hostname, state directory and key file in the proxy
  qube's private volume, kept under the qube's name, `<qube>.<service>`
  or `egress-<egress>`.
- The file MUST be refused when two identities would share a hostname or
  one of those names, and so MUST a proxy config that would.
- A qube's hostname is by default its name prefixed by the machine's; a
  service's is its bare name; an egress on a proxy other than the main
  one has the proxy's name added.
- torii MUST NOT read an identity's state back; a rebuilt proxy gets new
  identities and new sign-ins.

Why: the tailnet applies its access rules per device. One device for
several qubes would grant each of them the access of all, which merges
exactly the compartments torii exists to keep apart.

Scenarios:
1. `one-node-each` (agent, provisional): given several identities, when
   the proxy starts, then each gets a node with its own hostname and
   state directory; given two that would share a state, then they are
   refused.

Changes:
- 2026-11-27: a qube named egress-<egress>, or <qube>.<service>, shared
  another identity's state and key; both sides now refuse it, and the text
  says how each kind is named and that torii never reads state back.

## SW-261127-P8H7 torii MUST give a tagged identity exactly the file's tags, prefixed by the machine, and torii check MUST name any other

Status: active
Kind: invariant
Serves: STK-261126-KHZB

- A tagged identity's tags MUST be the short tags the file gives it, each
  prefixed by the machine's name (`server` on `m1` is `tag:m1-server`),
  and `tag:<machine>`.
- A service's tags MAY also be written in full, to be shared across
  machines; the file MUST refuse a full tag on a qube.
- A person's identity has no tags.
- `torii check` MUST name an identity whose tags on the tailnet, as the
  proxy reports them, differ from the file's.

Why: the tailnet's grants select devices by tag, so a qube carrying a tag
the file did not give it reaches whatever that tag reaches. Tags
therefore come only from the file, and any difference on the tailnet is
reported as a finding.

Scenarios:
1. `tags-prefixed` (agent, provisional): given a qube with a short tag,
   when its identity is configured, then it carries the machine-prefixed
   tag and the machine's own tag beside it.
2. `mismatch-named` (agent, provisional): given the proxy reporting an
   identity with other tags than the file's, when check runs, then it
   names that identity.

Changes:
- 2026-11-27: check read a field the proxy never sent, so it named no tag
  mismatch on a real machine; the proxy now reports it in that field.

## SW-261127-3TPX An identity MUST sign in by a link a person approves, or by a one-time key that only the proxy qube keeps

Status: active
Kind: invariant
Serves: STK-261126-KHZB

- A new identity asks the tailnet for a sign-in link, and `torii apply`,
  `show` and `check` MUST print it for a person to approve.
- `torii sign-in-key` MUST take a one-time key on dom0's standard input
  and write it straight into the proxy qube, as a new file under a
  directory only root can read, then restart the proxy with that
  identity's old state removed so it signs in with the key.
- dom0 MUST NOT write the key to any file.
- After the first sign-in, the identity's own state keeps it signed in.

Why: dom0 must hold no tailnet credential, because a compromised dom0
compromises everything. A sign-in link needs no secret at all, and a key
that passes through dom0 only on its way to the one qube that uses it
leaves nothing behind in dom0.

Scenarios:
1. `key-to-proxy-only` (agent, provisional): given a key on standard
   input, when sign-in-key runs, then it sends the key to the proxy
   qube's root shell as input, writes no file in dom0, and puts the key
   under the root-only key directory.
2. `links-printed` (agent, provisional): given an identity waiting for
   sign-in, when show runs, then the identity is listed with its link.

Changes:
- 2026-11-27: an existing key file kept its mode; the file is now removed
  and written new.

## SW-261127-DH6R torii MUST only propose grants for the tailnet, and MUST NOT change the tailnet's policy

Status: active
Kind: invariant
Serves: STK-261126-KHZB

- `torii grants` MUST print the tag owners and grants the file implies,
  for a person to review and paste into the tailnet's policy. An inbound
  identity's or a service's source is `autogroup:member`, to be narrowed;
  an egress's is its own tags; the machine's own tag MUST NOT be a
  destination.
- The dom0 tool's own process MUST NOT make a network connection, so it
  cannot change the tailnet's policy or grants. It changes the tailnet
  only when an identity signs in, with a link a person approves or a key
  the person gives.
- What the tool runs elsewhere does reach a network: `qvm-template`
  downloads the base template, and package installs run in torii's
  template.

Why: the tailnet's policy is the second half of every qube's access. A
tool that wrote it would be a second, unreviewed source of the same
rules, and one reachable from dom0, so torii prints a proposal and
leaves applying it to a person.

Scenarios:
1. `grants-printed` (agent, provisional): given a machine file, when
   `torii grants` runs, then it prints a grant per inbound identity and
   service, and the tag owners of every tag used.
2. `dom0-no-network` (agent, provisional): given the dom0 tool's modules,
   when their imports are read, then none imports a networking library.

Changes:
- 2026-11-27: the text said dom0 could not change the tailnet's devices or
  keys, though sign-in-key signs a device in, and did not say what it runs
  elsewhere; grants no longer name the machine's tag, which every egress
  carries, as a destination; the scenario checks dynamic imports and
  download tools too.

## SW-261127-WSWG An egress's identity MUST carry only internet traffic, and torii grants MUST propose it nothing else

Status: active
Kind: invariant
Serves: STK-261126-KHZB, STK-261126-346B

- An egress has one identity per proxy that carries it, shared by every
  qube on that egress.
- The proxy MUST NOT dial a tailnet destination on an egress's identity;
  tailnet destinations MUST be dialled only on a qube's own identity.
- `torii grants` MUST propose only `autogroup:internet` for the egress's
  tags, and MUST NOT name those tags in any other grant.

Why: sharing a path out is harmless, because every qube behind one exit
node shares its public address anyway. Sharing tailnet authorization is
how compartments merge, so the identity the qubes share is given none.

Scenarios:
1. `no-tailnet-on-egress` (agent, provisional): given a qube on an egress
   with no identity of its own, when it connects to a tailnet address,
   then the connection is refused.
2. `grants-internet-only` (agent, provisional): given an egress, when
   `torii grants` runs, then it proposes the egress's tags
   `autogroup:internet` and nothing else.

Changes:
- 2026-11-27: grants named the machine's tag, which the egress identities
  carry, as every inbound grant's destination; it no longer does.

## SW-261127-8PTP The proxy MUST NOT send Tailscale client logs unless the file asks, and MUST NOT map ports

Status: active
Kind: invariant
Serves: STK-261126-KHZB

- Client log uploads to Tailscale MUST be off, and the control server
  MUST be told so, unless the file sets `tailnet.tailscale-logs: yes`.
- Turning log uploads on MUST restart the program, because turning them
  off lasts for the life of the process.
- The port mapper (UPnP, NAT-PMP, PCP) MUST always be off.

Why: log uploads tell a third party about every identity's activity, and
behind Qubes' NAT the port mapper only broadcasts. The
proxy therefore sends neither, and the owner can turn on only the logs,
which Tailscale support sometimes needs.

Scenarios:
1. `logs-off-by-default` (agent, provisional): given no setting, when the
   program starts, then it turns log uploads and the port mapper off.
2. `logs-on-asked` (agent, provisional): given the file sets
   `tailscale-logs: yes`, when the config is built and later changed,
   then the setting reaches the proxy's config and turning logs on asks
   for a restart.
