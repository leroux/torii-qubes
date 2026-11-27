# The file, plan and apply

Code: dom0/torii, proxy/internal/config, proxy/internal/qubesdb, proxy/internal/daemon

The needs this area answers are STK-261126-B4CP and STK-261126-SM33. It
holds the machine file, what torii owns, plan as a function of the file
and a snapshot, apply step by step, the qrexec policy it writes, and the
config handed to each proxy as features and taken only when whole.

## SW-261127-9BNW The machine file is read whole and refused whole when a key, a network or a value is wrong

Status: planned
Kind: invariant
Serves: STK-261126-B4CP

Every command reads the whole machine file before it does anything. A
key the file format does not know, a qube with no `network` in its entry
or in `defaults`, or a value outside a key's forms is refused, naming
where it is, and nothing is planned or changed. A key an earlier version
of the format used is refused with the key that replaced it.

Why: a file read in part, or a typo taken as a default, changes a
qube's access without anyone having written it; refusing the whole file
keeps what is applied the same as what was written.

Scenarios:
1. `unknown-key-refused` (agent, provisional): a misspelt key in a qube's
   entry is refused naming the key and the qube.
2. `network-required` (agent, provisional): a qube with no `network` in
   its entry or in `defaults` is refused.
3. `earlier-key-redirected` (agent, provisional): a key of the earlier
   format is refused with the key that replaced it.

## SW-261127-2JH3 plan refuses before any step a file that names what this machine cannot be given

Status: planned
Kind: invariant
Serves: STK-261126-B4CP, STK-261126-SM33

plan is a function of the file and a snapshot of the live machine, and
it refuses, naming every reason and planning nothing, when the file
names: a qube that does not exist, since torii never creates a workload;
one of torii's own qubes, a template or dom0 as a workload; a netvm, or a
qube another mesh manages, as a workload; a netvm that is not one; a
disposable template without `disposables: yes`; a qube whose current
netvm torii does not know, without `adopt: force`; a routed disposable;
or a proxy name a qube not torii's, or another mesh's, already has.

Why: every one of these would have apply change something the owner did
not mean, or something that is not torii's; refusing them before the
first step means a plan is either whole or nothing.

Scenarios:
1. `missing-qube-refused` (agent, provisional): a file naming a qube the
   machine lacks plans nothing and names the qube.
2. `netvm-not-a-workload` (agent, provisional): a qube that provides
   network is refused as a workload.
3. `unknown-netvm-needs-force` (agent, provisional): a qube on a netvm
   torii does not know is refused until its entry says `adopt: force`.
4. `disposable-template-needs-yes` (agent, provisional): a disposable
   template is refused until its entry says `disposables: yes`.

## SW-261127-G26T apply changes only what torii owns: its template, its proxy qubes, its policy files, and four settings of each workload

Status: planned
Kind: invariant
Serves: STK-261126-B4CP, STK-261126-SM33

torii creates, changes and removes only its own template, the proxy
qubes it created (tagged `torii-owned`, and with the mesh's tag for a
named mesh), its mesh's policy file `50-torii` (or `50-torii-<mesh>`) and
the shared deny file `59-torii-deny`. On a qube the file lists it sets
only the netvm, the `torii-client` service and its feature, the
`torii-managed` tag, and the adoption features written once. It never
creates or removes a workload, and a qube that only hosts a service is
not changed.

Why: a tool with dom0's power is reviewable only if what it may touch
is a short list; everything else on the machine stays the owner's.

Scenarios:
1. `fresh-machine-steps` (agent, provisional): the plan for a fresh
   machine changes only torii's own qubes, its policy files and the
   listed qubes' owned settings.
2. `service-host-untouched` (agent, provisional): a qube that hosts a
   service and is not listed gets no step.
3. `other-mesh-untouched` (agent, provisional): applying one mesh's file
   changes nothing another mesh manages.

## SW-261127-63CS apply makes the plan's steps in order, stops at the first that fails, and a second run continues

Status: planned
Kind: invariant
Serves: STK-261126-B4CP

apply makes plan's steps one at a time, in plan's order, naming each.
The first step that fails stops it, naming the step and the cause, and
no later step is made. Run again, plan starts from the machine as the
stopped run left it, so it holds only the steps not yet made; on a
machine that matches the file it holds none.

Why: a change half made must be visible and finishable; stopping at
the first failure leaves the machine where the plan says, and planning
from the live machine again needs no record of the run that stopped.

Scenarios:
1. `stops-at-first-failure` (agent, provisional): when the second of
   three steps fails, the third is not made and the error names the
   second.
2. `second-run-continues` (agent, provisional): planning again after a
   stopped run holds only the steps it did not make.
3. `second-apply-empty` (agent, provisional): planning on a machine apply
   has just made match the file holds no step.

## SW-261127-VPGK A qube adopted records its netvm once; a qube leaving the file is left with no netvm

Status: planned
Kind: invariant
Serves: STK-261126-B4CP, STK-261126-SM33

The first apply that lists a qube records the netvm it had, in
`torii-prior-netvm` and `torii-adopted`, and never writes them again. A
qube torii manages that the file no longer lists is set to no netvm,
loses the client's service and feature and the managed tag, and keeps
its record; plan names the netvm it had before torii and the command to
restore it, and never restores it.

Why: the netvm a qube had may send directly what the file has since
sent through the proxy; restoring it silently would undo a route
without anyone choosing to, so leaving the file fails closed.

Scenarios:
1. `prior-netvm-recorded-once` (agent, provisional): the first apply
   records the qube's netvm and a later one leaves the record as it was.
2. `leaving-fails-closed` (agent, provisional): a qube removed from the
   file is planned to no netvm, and the plan names its prior netvm and
   the command to restore it.

## SW-261127-XGDY A proxy takes its config whole or not at all, and a failed check never replaces the running one

Status: planned
Kind: invariant
Serves: STK-261126-SM33, STK-261126-B4CP

dom0 hands each proxy its config as the features `vm-config.torii*`: the
JSON compressed, encoded and split into parts under QubesDB's limit, with
a head of the part count and the JSON's SHA-256. apply writes the parts
first and the head last, then removes stale parts. The proxy joins the
parts, decodes them and checks the hash and the config itself; one that
fails any check is not taken, and the proxy keeps running the config it
has, or waits for a valid one if it has none.

Why: features change one at a time, so a proxy reading while dom0 writes
sees a mix; a hash over the whole makes the mix refusable, and keeping
the running config means a bad write never opens anything.

Scenarios:
1. `chunks-roundtrip` (agent, provisional): a config split by dom0 is
   read back by the proxy as the same JSON, each part under the limit.
2. `half-written-refused` (agent, provisional): parts that do not match
   the head's hash are refused by dom0's reader and the proxy's.
3. `head-written-last` (agent, provisional): apply writes every part
   before the head, and removes stale parts after it.
4. `bad-config-kept-out` (agent, provisional): a change whose config
   fails its check leaves the proxy on its running config.

## SW-261127-QMVP apply goes on past a proxy's config only once the proxy reports running it

Status: planned
Kind: invariant
Serves: STK-261126-B4CP

After writing a proxy's config, apply starts the proxy if it is not
running, and waits until the proxy's status reports that config's
version, showing any sign-in links as they appear. If the proxy does not
report it in time, the step fails, naming what it last reported.

Why: later steps, such as moving a qube onto the proxy, rely on the
proxy already deciding by the new config; going on before it does
would route a qube through a proxy that does not know it.

Scenarios:
1. `waits-for-version` (agent, provisional): apply goes on only once the
   proxy's status reports the version written.
2. `times-out-naming-state` (agent, provisional): a proxy that keeps
   reporting an older version fails the step, naming that version.

## SW-261127-3Q23 Every proxy qube has IPv6 off, for itself and every qube behind it

Status: planned
Kind: invariant
Serves: STK-261126-KZK8

plan sets the Qubes `ipv6` feature of every proxy qube to off, which the
qubes behind it inherit, whatever the netvms above it say; it is set
again whenever it differs.

Why: the proxy decides IPv4 only and the guard drops IPv6 from the
workloads; Qubes not configuring IPv6 behind the proxy keeps qubes from
trying a path that would only be dropped.

Scenarios:
1. `ipv6-off` (agent, provisional): a proxy qube without the feature, or
   with it on, gets a step setting it off.

## SW-261127-HSHY The proxy package comes from the dom0 package's bundle, installed into torii's template with no repository

Status: planned
Kind: invariant
Serves: STK-261126-B4CP, STK-261126-SM33
Accounts: env:TORII_BUNDLE

The dom0 package carries the proxy and client packages in its bundle
directory, `/usr/share/torii` or the one `TORII_BUNDLE` names. When
torii's template has another bundle version than the tool's, plan adds a
step that copies the package into the template, installs it and its
dependencies, enables the guard and the program, and records the
version; running proxies are restarted on it, their clients detached and
attached again.

Why: a template needing a repository for torii would trust that
repository with every proxy; the package travels inside the one dom0
already installed, and a version stamp keeps template and tool in step.

Scenarios:
1. `bundle-installs` (agent, provisional): a template without the
   bundle's version gets one install step, and none once it has it.
2. `running-proxy-restarted` (agent, provisional): a new package
   restarts each running proxy, naming the clients it detaches.

## SW-261127-KWT2 Each mesh's file changes only its own qubes and its own policy file

Status: planned
Kind: invariant
Serves: STK-261126-B4CP

A file with `mesh:` set is applied on its own: its workloads carry
`torii-managed-<mesh>`, its proxies `torii-mesh-<mesh>`, and its policy
is `50-torii-<mesh>`. plan refuses a qube another mesh manages, a proxy
another mesh owns, and never removes another mesh's qubes or templates
they use; the shared deny file is the same from every mesh.

Why: an owner who keeps a test file beside the main one must be able
to apply it without the two undoing each other.

Scenarios:
1. `meshes-apart` (agent, provisional): a second mesh's plan leaves the
   first mesh's qubes, proxies and policy file alone.
