# The tailnet side

What a tailnet admin sets up so that a machine's identities can join and be
reached: tags, grants, HTTPS, sign-in, exit nodes. Tailscale's own terms are
used throughout. "The policy" is the tailnet policy file (Access controls in
the admin console).

## How identities appear

Every qube with an identity is a device on the tailnet, held by `sys-torii`:

- **Tagged** (`identity: tagged`, and every entry under `services:`):
  a device with the file's tags, owned by the tags rather than a person.
  Tagged devices' keys don't expire by default, so they stay signed in.
- **A person's** (`identity: login`): signs in as that person, like their
  laptop. For a work qube used by one person.

The device's name is the qube's name (or the identity's, or `hostname:`). It
is reachable as `name` and `name.<tailnet>.ts.net`. Each identity is a
device of its own, with its own state in the proxy qube
([SW-261127-WNGN](../claims/identity.md#sw-261127-wngn-the-proxy-must-run-each-identity-as-a-tailnet-device-of-its-own-with-its-own-name-and-state)); where they run is in
[`architecture.md`](architecture.md#the-proxy-qube).

The machine file decides which ports a device offers. The policy decides
who may reach them, and where a device with `outbound.tailnet: yes` may go. A port
the file declares but no grant allows is dropped by the tailnet before it
reaches the machine.

## Tags

Every tag in the machine file must be in the policy's `tagOwners` before an
identity with it can join. The owners are the people (or groups) who may
approve such a device:

```jsonc
"tagOwners": {
  "tag:server":   ["autogroup:admin"],
  "tag:job":      ["autogroup:admin"],
  "tag:notes": ["autogroup:admin"],
  "tag:chat":   ["autogroup:admin"],
},
```

`torii grants` lists every tag the file uses. Keep tags per role (`tag:chat`),
not per machine. Then grants read as what they allow, and a second machine
reuses them. How the file's tags become the device's, and what `check` says
when they differ, is [SW-261127-P8H7](../claims/identity.md#sw-261127-p8h7-torii-must-give-a-tagged-identity-exactly-the-files-tags-prefixed-by-the-machine-and-torii-check-must-name-any-other).

## Grants

`torii grants` prints a proposal from the file: a grant per inbound
identity, with `autogroup:member` (every signed-in person's devices) as the
source. Review it, narrow `src` where needed, and paste it in; torii never
changes the policy itself ([SW-261127-DH6R](../claims/identity.md#sw-261127-dh6r-torii-must-only-propose-grants-for-the-tailnet-and-must-not-change-the-tailnets-policy)):

```jsonc
"grants": [
  // Everyone may use the chat and notes services.
  {"src": ["autogroup:member"], "dst": ["tag:chat"],   "ip": ["tcp:443"]},
  {"src": ["autogroup:member"], "dst": ["tag:notes"], "ip": ["tcp:443", "tcp:8443"]},

  // Only the ops group reaches the servers' admin port.
  {"src": ["group:ops"], "dst": ["tag:server"], "ip": ["tcp:22"]},

  // A job with `outbound.tailnet: yes` may reach the database, and nothing else on the tailnet.
  {"src": ["tag:job"], "dst": ["tag:db"], "ip": ["tcp:5432"]},
],
```

Notes:

- A tagged device is not a member, so `autogroup:member` doesn't let one
  machine's qubes reach another's. Name the tag as `src` for that.
- `outbound.tailnet: yes` without a grant reaches nothing. In `torii log` the
  connection is `allow tailnet-outbound` with a timeout: the machine let it
  out, the tailnet dropped it.
- Use the policy's `tests` section to pin what must and must not be
  reachable. The admin console refuses a policy that breaks them.

### Editing the policy through the API

If you script it (`POST /api/v2/tailnet/-/acl`), the body **replaces the
whole policy**. An empty or broken body resets the tailnet to Tailscale's
default, which allows everything. So:

1. `GET` the current policy and save it.
2. Build the new one into a new file. Never redirect into the file you're
   reading from.
3. Check the new file is non-empty and has your tags and grants.
4. Only then `POST` it, and `GET` it back to confirm.

## HTTPS

Inbound ports with `tls: yes` serve HTTPS with the device's own certificate,
as `tailscale serve` does. Turn it on once per tailnet: admin console → DNS →
enable MagicDNS (usually on) and **HTTPS Certificates**. Or via the API:

```sh
curl -u "$TS_API_KEY:" -X PATCH https://api.tailscale.com/api/v2/tailnet/-/settings \
     -H 'Content-Type: application/json' -d '{"httpsEnabled": true}'
```

Certificates come from Let's Encrypt, so device names such as
`chat.<tailnet>.ts.net` appear in the public Certificate Transparency
logs. The first connection to a name waits a few seconds while its
certificate is issued.

## Signing identities in

<!-- charter:embed SW-261127-3TPX -->
> **[SW-261127-3TPX](../claims/identity.md#sw-261127-3tpx-an-identity-must-sign-in-by-a-link-a-person-approves-or-by-a-one-time-key-that-only-the-proxy-qube-keeps) An identity MUST sign in by a link a person approves, or by a one-time key that only the proxy qube keeps**
>
> - A new identity asks the tailnet for a sign-in link, and `torii apply`,
>   `show` and `check` MUST print it for a person to approve.
> - `torii sign-in-key` MUST take a one-time key on dom0's standard input
>   and write it straight into the proxy qube, as a new file under a
>   directory only root can read, then restart the proxy with that
>   identity's old state removed so it signs in with the key.
> - dom0 MUST NOT write the key to any file.
> - After the first sign-in, the identity's own state keeps it signed in.
<!-- /charter:embed -->

A new identity waits for sign-in. `torii show` (and `torii apply`) prints its
link:

```
waiting for sign-in: wiki: https://login.tailscale.com/a/0123456789abcd
```

**By link** (no credential anywhere): open it in a browser signed in to the
tailnet as an owner of the identity's tags, and approve. Each link is for one
identity. A link is replaced when `sys-torii` restarts, so take the current
one from `torii show`.

**By key**, when clicking isn't practical (many identities, scripts): admin
console → Settings → Keys → Generate auth key, with:

- Reusable: **off**
- Pre-approved: **on** (if device approval is on)
- Tags: **exactly the identity's tags**
- Expiry: short (an hour is plenty)

Then, in dom0:

```sh
torii sign-in-key wiki < key.txt                 # a qube's identity
torii sign-in-key chat < key.txt               # a service
```

The key is stored in `sys-torii` (`/rw/torii/keys/`), never in dom0, and the
proxy restarts to use it. The key only joins the device once; after that the
device's own state keeps it signed in.

With an API key you can make these keys without the console:

```sh
curl -u "$TS_API_KEY:" -X POST https://api.tailscale.com/api/v2/tailnet/-/keys \
  -H 'Content-Type: application/json' \
  -d '{"capabilities":{"devices":{"create":{"reusable":false,"ephemeral":false,"preauthorized":true,"tags":["tag:server"]}}},"expirySeconds":3600}'
```

## Exit nodes

An egress (`egress: {vpn: {exits: [exit1, exit2]}}`) sends the internet
traffic and DNS lookups of the qubes that use it (`egress: vpn`) through the
first healthy of its exit nodes. It is its own tailnet device,
`<machine>-egress-vpn` tagged `tag:<machine>-egress`, one per proxy qube that
carries it; the qubes using it need no identity. An exit node is healthy
while the tailnet map lists it online and offering an exit node, it answers a
Tailscale ping (every 10 s; three missed is down), and real connections
through it are not all failing (three or more destinations failing in 30 s,
none succeeding). The egress fails over to the next healthy node in order,
and back to a preferred one once it has stayed healthy for `failback-after`
(2 minutes). With none healthy, connections are refused at once and DNS
answers SERVFAIL; nothing is sent direct, and the exit node is never cleared.
`probe: host:port` adds an end-to-end check through the active node; there is
none by default, since it would send a recognisable connection to a third
party from every exit node.

1. Run Tailscale on each exit device with `--advertise-exit-node`, and
   approve it under Machines → Edit route settings. Or let the policy approve
   it: `"autoApprovers": {"exitNode": ["tag:exit"]}`.
2. Grant the egress the internet, and nothing else:
   `{"src": ["tag:m1-egress"], "dst": ["autogroup:internet"], "ip": ["*"]}`.
   (`torii grants` proposes it.) The proxy never sends tailnet destinations
   through an egress either; a qube that needs the tailnet gets its own
   identity, which carries its tailnet traffic while the egress carries its
   internet ([SW-261127-WSWG](../claims/identity.md#sw-261127-wswg-an-egresss-identity-must-carry-only-internet-traffic-and-torii-grants-must-propose-it-nothing-else)).

How the egress chooses and changes its exit node, and how it fails closed,
is followed through the code in [`walkthrough-egress.md`](walkthrough-egress.md):
[SW-261127-8DFY](../claims/egress.md#sw-261127-8dfy-the-egress-selector-must-judge-an-exit-node-healthy-only-while-it-is-listed-answers-pings-and-is-not-failing-every-real-connection), [SW-261127-BNFC](../claims/egress.md#sw-261127-bnfc-the-egress-selector-must-use-exit-nodes-in-the-files-order-failing-over-at-once-and-back-only-after-a-hold-down), [SW-261127-HJNX](../claims/egress.md#sw-261127-hjnx-the-proxy-must-refuse-an-egresss-connections-at-once-and-fail-its-dns-while-the-egress-has-no-healthy-exit-node).

## Removing devices

Taking a qube out of the file stops its identity, but its device stays in the
admin console, offline, until you remove it (Machines → ⋯ → Remove). If the
qube comes back with its state still in `sys-torii`, it reuses the same
device.

## Headscale

Set `tailnet.control-url: https://headscale.example.org` in the machine file.
Tags and `tagOwners` work the same. To approve a link, run
`headscale auth register --user <owner>` with the link's key. Headscale's
grants are not identical to Tailscale's, so check access rules on the
coordination server you'll use. Headscale is tested at the program's level 4
([`proxy/README.md`](../proxy/README.md)), not yet on a Qubes machine.

## When something can't connect

Each line of `torii log` is one decision with the rule that made it
([SW-261127-R4CD](../claims/seeing.md#sw-261127-r4cd-the-proxy-must-log-every-connection-it-decides-with-the-verdict-and-the-rule-that-made-it)); the `reason` values below are
the `Reason` constants in [policy.go](../proxy/internal/policy/policy.go).

| Symptom | Look at |
|---|---|
| Nothing in `torii log QUBE` for a laptop's attempt | The grant; that the laptop is on the same tailnet; the device's tags in the admin console |
| `allow` with `context deadline exceeded` for outbound | No grant for that destination, or it is offline |
| `refuse not-allowlisted` | The name isn't in `outbound.internet` (or `.lan`) |
| `refuse no-lan` | The address is on the LAN, and `outbound.lan` is none |
| `refuse forbidden-address` | Another qube's address, or the proxy's own: never reachable |
| `waiting for sign-in` won't go away | The approver doesn't own the tag; or the link was replaced (take the new one from `torii show`) |
| A web app loads but won't work under the new name | The app checks its own address (a base URL or allowed origin in its config). Point it at the new name |
