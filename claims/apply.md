# The file, plan and apply

Code: dom0/torii, proxy/internal/config, proxy/internal/qubesdb, proxy/internal/daemon

This area answers STK-261126-B4CP and STK-261126-SM33. It covers the
machine file, what torii owns, plan as a function of the file and a
snapshot of the machine, apply step by step, the qrexec policy apply
writes, and the config dom0 hands each proxy as features, which the
proxy takes only when it arrives whole.

## SW-261127-9BNW torii MUST read the machine file whole, and MUST refuse it whole when a key, a network or a value is wrong

Status: active
Kind: invariant
Serves: STK-261126-B4CP

- Every command that reads the machine file MUST read it whole before it
  does anything: plan, apply, check, grants and sign-in-key always; show
  and snapshot when the file exists. log and install-client do not read
  it.
- torii MUST refuse, naming where it is, a key the file format does not
  know, a qube with no `network` in its entry or in `defaults`, and a
  value outside a key's forms; nothing is then planned or changed.
- torii MUST refuse a key that an earlier version of the format used,
  naming the key that replaced it.

Why: a file read in part, or a typo silently taken as a default, would
change a qube's access in a way nobody wrote. Refusing the whole file on
any error keeps what is applied identical to what was written.

Scenarios:
1. `unknown-key-refused` (agent, provisional): given a misspelt key in a
   qube's entry, when the file is read, then it is refused naming the key
   and the qube.
2. `network-required` (agent, provisional): given a qube with no
   `network` in its entry or in `defaults`, when the file is read, then
   it is refused.
3. `earlier-key-redirected` (agent, provisional): given a key of the
   earlier format, when the file is read, then it is refused naming the
   key that replaced it.

Changes:
- 2026-11-27: names the commands that read the file; log and install-
  client never read it, and show and snapshot go on without one, which the
  text did not say.

## SW-261127-2JH3 torii plan MUST refuse, before any step, a file that names what this machine cannot be given

Status: active
Kind: invariant
Serves: STK-261126-B4CP, STK-261126-SM33

`torii plan` is a function of the file and a snapshot of the live
machine. It MUST refuse, planning nothing, when the file names:

- a qube that does not exist, since torii never creates a workload;
- one of torii's own qubes, a template or dom0 as a workload;
- a netvm, or a qube another mesh manages, as a workload;
- as a netvm, a qube that is not one;
- a disposable template without `disposables: yes`, or `disposables:
  yes` on a qube that is not a disposable template;
- a qube whose current netvm torii does not know, without
  `adopt: force`;
- a routed qube with no fixed address, such as a disposable;
- a `local-inbound` source, or a service's qube, that is not on the
  machine or cannot host it;
- a proxy whose netvm is not on the machine, or whose name a qube that
  is not torii's, or another mesh's, already has.

These MUST be named together. A template name that a qube not torii's
already has, and a proxy to remove that a qube still uses, MUST each be
refused on their own.

Why: each of these would make apply change something the owner did not
mean, or something that is not torii's. Refusing them before the first
step means a plan is either complete or empty, never a partial change
that stops halfway on a predictable error.

Scenarios:
1. `missing-qube-refused` (agent, provisional): given a file naming a
   qube the machine lacks, when plan runs, then it plans nothing and
   names the qube.
2. `netvm-not-a-workload` (agent, provisional): given a qube that
   provides network listed as a workload, when plan runs, then it is
   refused.
3. `unknown-netvm-needs-force` (agent, provisional): given a qube on a
   netvm torii does not know, when plan runs, then it is refused until
   its entry says `adopt: force`.
4. `disposable-template-needs-yes` (agent, provisional): given a
   disposable template listed as a workload, when plan runs, then it is
   refused until its entry says `disposables: yes`.

Changes:
- 2026-11-27: the list is the one plan makes, with the refusals it left
  out, and says which two are refused on their own rather than with the
  rest.

## SW-261127-G26T torii apply MUST change only what torii owns: its template, its proxy qubes, its policy files, and named settings of listed qubes

Status: active
Kind: invariant
Serves: STK-261126-B4CP, STK-261126-SM33

- torii MUST create, change and remove only its own template, the proxy
  qubes it created (tagged `torii-owned`, and with the mesh's tag for a
  named mesh), its mesh's policy file `50-torii` (or `50-torii-<mesh>`)
  and the shared deny file `59-torii-deny`.
- torii MAY download the base template its own is cloned from, and MUST
  NOT change it.
- On a qube the file lists, torii MUST set only the netvm, the
  `torii-client` service and its feature, the `torii-managed` tag and the
  adoption features it writes once, and MUST remove the earlier
  `torii-netvm-kept` tag.
- Restarting a proxy detaches every running qube behind it and attaches
  it again, listed or not.
- torii MUST NOT create or remove a workload, and MUST NOT change a qube
  that only hosts a service.

Why: a tool with dom0's power can be reviewed only if the set of things
it may touch is short and stated. Everything outside that set stays the
owner's, so a plan never surprises the owner with a change to a qube or
file torii does not own.

Scenarios:
1. `fresh-machine-steps` (agent, provisional): given a fresh machine,
   when plan runs, then its steps change only torii's own qubes, its
   policy files and the listed qubes' owned settings.
2. `service-host-untouched` (agent, provisional): given a qube that hosts
   a service and is not listed, when plan runs, then it gets no step.

Changes:
- 2026-11-27: names what else it touches: the base template it downloads,
  the earlier tag it removes, and the qubes a proxy's restart detaches and
  attaches again, listed or not.

## SW-261127-63CS torii apply MUST make plan's steps in order, stop at the first failure, and continue on the next run

Status: active
Kind: invariant
Serves: STK-261126-B4CP

`torii apply` MUST make plan's steps one at a time, in plan's order,
naming each. The first step that fails MUST stop it, naming the step and
the cause, and no later step is made. Run again, plan starts from the
machine as the stopped run left it, so it holds only the steps not yet
made; on a machine that matches the file it holds none.

Why: a change left half made has to be visible and finishable. Stopping
at the first failure leaves the machine where the plan says it is, and
because plan always starts from the live machine, the next run finishes
the work without needing any record of the run that stopped.

Scenarios:
1. `stops-at-first-failure` (agent, provisional): given three steps,
   when the second fails, then the third is not made and the error names
   the second.
2. `second-run-continues` (agent, provisional): given a stopped run, when
   plan runs again, then it holds only the steps the first run did not
   make.
3. `second-apply-empty` (agent, provisional): given a machine apply has
   just made match the file, when plan runs, then it holds no step.

## SW-261127-VPGK torii MUST record an adopted qube's netvm once, and MUST leave a qube that leaves the file with no netvm

Status: active
Kind: invariant
Serves: STK-261126-B4CP, STK-261126-SM33

- The first apply that lists a qube MUST record the netvm it had, in
  `torii-prior-netvm` and `torii-adopted`, and MUST NOT write them again.
- A qube torii manages that the file no longer lists MUST be set to no
  netvm, MUST lose the client's service and feature and the managed tag,
  and MUST keep its record.
- A qube still carrying the earlier `torii-netvm-kept` tag MUST keep the
  netvm it has, since that netvm was never torii's to set.
- When the record names a netvm other than none and other than the one
  the qube has, plan MUST name it and the command to restore it, and
  MUST NOT restore it.

Why: the netvm a qube had before torii may send directly what the file
has since routed through the proxy or an egress. Restoring it silently
would undo that routing without anyone choosing to, so a qube leaving
the file fails closed and the owner decides whether to restore it.

Scenarios:
1. `prior-netvm-recorded-once` (agent, provisional): given a qube listed
   for the first time, when apply runs and later runs again, then the
   first run records the netvm and the second leaves the record as it
   was.
2. `leaving-fails-closed` (agent, provisional): given a qube removed from
   the file, when plan runs, then it plans the qube to no netvm and names
   its prior netvm and the command to restore it.

Changes:
- 2026-11-27: a qube with the earlier kept tag keeps its netvm, and the
  restore command is named only when the record holds one to name, which
  the text left out.

## SW-261127-XGDY A proxy MUST take its config whole or not at all, and a config that fails a check MUST NOT replace the running one

Status: active
Kind: invariant
Serves: STK-261126-SM33, STK-261126-B4CP

- dom0 MUST hand each proxy its config as the features
  `vm-config.torii*`: the JSON compressed, encoded and split into parts
  under QubesDB's limit, with a head holding the part count and the
  JSON's SHA-256.
- apply MUST write the parts first and the head last, then remove stale
  parts.
- The proxy MUST join the parts, decode them, and check the hash and the
  config itself. A config that fails any check MUST NOT be taken: the
  proxy keeps running the config it has, or waits for a valid one if it
  has none.
- A valid config that the program then fails to start on ends the
  process, and its service restarts it.

Why: features change one at a time, so a proxy that reads while dom0 is
writing sees a mix of old and new parts. A hash over the whole config
lets the proxy recognize and refuse such a mix, and keeping the running
config means that a bad or partial write never opens anything.

Scenarios:
1. `chunks-roundtrip` (agent, provisional): given a config split by dom0,
   when the proxy reads it back, then it gets the same JSON and every
   part is under the limit.
2. `half-written-refused` (agent, provisional): given parts that do not
   match the head's hash, including a whole new write under the old head,
   when dom0's reader and the proxy's read them, then both refuse them.
3. `head-written-last` (agent, provisional): given a config change, when
   apply writes it, then every part is written before the head and stale
   parts are removed after it.
4. `bad-config-kept-out` (agent, provisional): given a change whose
   config fails its check, when the proxy reads it, then the proxy stays
   on its running config.

Changes:
- 2026-11-27: a valid config the program fails to start on ends the
  process rather than keeping the old one, which the text did not say; the
  scenario for half-written parts now includes the case only the hash
  catches.

## SW-261127-QMVP torii apply MUST NOT go past a proxy's config until the proxy reports running it

Status: active
Kind: invariant
Serves: STK-261126-B4CP

After writing a proxy's config, `torii apply` MUST start the proxy if it
is not running, and MUST wait until the proxy's status reports that
config's version, showing any sign-in links as they appear. If the proxy
does not report the version in time, the step MUST fail, naming what the
proxy last reported.

Why: later steps, such as moving a qube onto the proxy, rely on the
proxy already deciding by the new config; going on before it does would
route a qube through a proxy that does not yet know the qube's entry. A
restart for a new package attaches the proxy's clients again before
this wait, on the config the proxy had.

Scenarios:
1. `waits-for-version` (agent, provisional): given a config written,
   when the proxy's status reports that version, then apply goes on, and
   not before.
2. `times-out-naming-state` (agent, provisional): given a proxy that keeps
   reporting an older version, when the wait runs out, then the step
   fails naming that version.

Changes:
- 2026-11-27: the Why says that a package restart reattaches clients
  before the wait, on the config the proxy had.

## SW-261127-3Q23 torii plan MUST turn IPv6 off on every proxy qube, for itself and every qube behind it

Status: active
Kind: invariant
Serves: STK-261126-KZK8

`torii plan` MUST set the Qubes `ipv6` feature of every proxy qube to
off, which the qubes behind it inherit, whatever the netvms above it
say, and MUST set it again whenever it differs.

Why: the proxy decides IPv4 only, and the guard drops IPv6 arriving from
the workloads. When Qubes does not configure IPv6 behind the proxy, the
qubes there do not try a path that would only be dropped.

Scenarios:
1. `ipv6-off` (agent, provisional): given a proxy qube without the
   feature, or with it on, when plan runs, then it gets a step setting it
   off.

## SW-261127-HSHY torii MUST install the proxy package from the dom0 package's bundle, with no repository for torii

Status: active
Kind: invariant
Serves: STK-261126-B4CP, STK-261126-SM33
Accounts: env:TORII_BUNDLE

- The dom0 package carries the proxy and client packages in its bundle
  directory, `/usr/share/torii`, or the one `TORII_BUNDLE` names.
- When torii's template has a bundle version other than the tool's, plan
  MUST add a step that installs the package's dependencies from the
  template distribution's own repositories, copies the package into the
  template, installs it, enables the guard and the program, and records
  the version.
- Running proxies MUST then be restarted on the new package, their
  clients detached and attached again.

Why: a template that needed a repository for torii would trust that
repository with every proxy. The distribution's repositories are ones
the template already trusts, torii's own package travels inside the dom0
package the owner already installed, and the recorded version keeps the
template and the tool in step.

Scenarios:
1. `bundle-installs` (agent, provisional): given a template without the
   bundle's version, when plan runs, then it holds one install step, and
   once the template has the version, none.
2. `running-proxy-restarted` (agent, provisional): given a new package
   and running proxies, when plan runs, then it restarts each one, naming
   the clients it detaches.

Changes:
- 2026-11-27: the dependencies come from the template distribution's
  repositories; only torii's own package needs none, which the title
  overstated.

## SW-261127-KWT2 A mesh's file MUST change only its own qubes and its own policy file

Status: active
Kind: invariant
Serves: STK-261126-B4CP

- A file with `mesh:` set is applied on its own: its workloads carry
  `torii-managed-<mesh>`, its proxies `torii-mesh-<mesh>`, and its policy
  is `50-torii-<mesh>`.
- plan MUST refuse a qube another mesh manages and a proxy another mesh
  owns, and MUST NOT remove another mesh's qubes or a template their
  proxies use. The shared deny file is the same from every mesh.
- Meshes share torii's template: a package one mesh's apply installs is
  the template's for every mesh, and each mesh's next apply restarts its
  own proxies still running the older one.

Why: an owner who keeps a test file beside the main one has to be able to
apply either without the two undoing each other's qubes and policy.

Scenarios:
1. `meshes-apart` (agent, provisional): given two meshes, when the second
   mesh's plan runs, then it leaves the first mesh's qubes, proxies and
   policy file alone.
2. `shared-template-kept` (agent, provisional): given a template another
   mesh's proxies use, when plan runs, then the template is not removed.

Changes:
- 2026-11-27: meshes share the template and its package, which the text
  did not say; a scenario holds a template in use from removal.
