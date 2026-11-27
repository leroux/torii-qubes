# How a plan becomes the machine

A walkthrough of `torii plan` and `torii apply` in dom0, from reading
`~/torii.yaml` to the proxy running the new config, and then of `torii
show` and `torii check` reading the result back. The parts are described
in [`architecture.md`](architecture.md); the rules are in
[`claims/apply.md`](../claims/apply.md) and
[`claims/seeing.md`](../claims/seeing.md).

The need this flow answers:

<!-- charter:embed STK-261126-B4CP -->
> **[STK-261126-B4CP](../claims/needs.md#stk-261126-b4cp-apply-changes-only-what-torii-owns-and-exactly-the-steps-plan-showed) apply changes only what torii owns, and exactly the steps plan showed**
>
> plan is a function of the machine file and a snapshot of the live
> machine, and lists every change in the order apply makes it. apply makes
> those changes and no others, stops at the first that fails, and run again
> makes only the changes left. On a machine that matches the file, plan is
> empty.
>
> torii owns: its template, the proxy qubes it created, the policy file
> `/etc/qubes/policy.d/50-torii.policy`, and on each managed qube only its
> netvm, the `torii-client` service and feature, its torii tags and the
> adoption features it writes once. It
> creates or deletes no other qube and writes no other policy file. A qube
> removed from the file is left with no netvm, never its old one.
<!-- /charter:embed -->

## 1. The file

[`cmd_plan`](../dom0/torii/cli.py#L74) and [`cmd_apply`](../dom0/torii/cli.py#L94)
start the same way: [`machine.load`](../dom0/torii/machine.py#L657) reads the
file and [`machine.parse`](../dom0/torii/machine.py#L551) turns it into a
`Machine`, every default filled in and every qube's entry resolved:
[SW-261127-9BNW](../claims/apply.md#sw-261127-9bnw-the-machine-file-is-read-whole-and-refused-whole-when-a-key-a-network-or-a-value-is-wrong). A wrong key, a network that cannot be, a value of the
wrong kind: the whole file is refused, and nothing has been read from the
machine yet.

## 2. The snapshot

[`live.take`](../dom0/torii/live.py#L83) reads the machine through the Admin
API into one [`Snapshot`](../dom0/torii/live.py#L44): every qube's class,
netvm, tags and state, the features of the qubes the file names and of
the tool's own, and every qrexec policy file. Nothing after this step
reads the machine again until apply makes its first change; `torii
snapshot` prints this object, which is how the plan tests run without a
Qubes machine ([`testing.md`](testing.md)).

## 3. The plan

[`plan.make`](../dom0/torii/plan.py#L96) is a function of the `Machine`, the
`Snapshot` and the bundle's version, and of nothing else. It works in two
passes.

**First, everything this machine cannot be given**, gathered and refused
together before any step: a qube the file names that does not exist, a
template or a netvm offered as a workload, a qube another mesh manages, a
disposable template listed without `disposables: yes`, a qube whose netvm
torii does not know, which adopting would move, without `adopt: force`:

<!-- charter:embed SW-261127-2JH3 -->
> **[SW-261127-2JH3](../claims/apply.md#sw-261127-2jh3-plan-refuses-before-any-step-a-file-that-names-what-this-machine-cannot-be-given) plan refuses before any step a file that names what this machine cannot be given**
>
> plan is a function of the file and a snapshot of the live machine, and
> it refuses, planning nothing, when the file names: a qube that does not
> exist, since torii never creates a workload; one of torii's own qubes, a
> template or dom0 as a workload; a netvm, or a qube another mesh manages,
> as a workload; a netvm that is not one; a disposable template without
> `disposables: yes`, or `disposables: yes` on a qube that is not one; a
> qube whose current netvm torii does not know, without `adopt: force`; a
> routed qube with no fixed address, such as a disposable; a
> `local-inbound` source, or a service's qube, that is not on the machine
> or cannot host it; a proxy whose netvm is not on the machine, or whose
> name a qube not torii's, or another mesh's, already has. These are named
> together; a template name a qube not torii's has, and a proxy to remove
> that a qube still uses, are refused on their own.
<!-- /charter:embed -->

**Then the steps**, in the order apply will make them:

1. The proxy template: cloned from the base template if it is missing, and
   the bundle's proxy package installed in it when its recorded version is
   not the bundle's ([SW-261127-HSHY](../claims/apply.md#sw-261127-hshy-the-proxy-package-comes-from-the-dom0-packages-bundle-with-no-repository-for-torii)).
2. Each proxy qube in use: created, or restarted on a new package; IPv6
   off ([SW-261127-3Q23](../claims/apply.md#sw-261127-3q23-every-proxy-qube-has-ipv6-off-for-itself-and-every-qube-behind-it)); its prefs; its `torii-proxy` service; its
   config, from [`derive.proxy_config`](../dom0/torii/derive.py#L53), as
   features; and a step that waits until it runs that config.
3. The policy files: this mesh's, from
   [`derive.policy`](../dom0/torii/derive.py#L216), and the shared denies after
   every mesh's ([SW-261127-M36P](../claims/links.md#sw-261127-m36p-every-qrexec-call-runs-in-the-direction-of-the-connection-it-carries-and-only-the-declared-ones-are-allowed), [SW-261127-KWT2](../claims/apply.md#sw-261127-kwt2-each-meshs-file-changes-only-its-own-qubes-and-its-own-policy-file)).
4. Each qube in the file: its netvm, its managed tag, and on its first
   adoption the record of the netvm it had; its client service and
   feature, from [`derive.qube_state`](../dom0/torii/derive.py#L192).
5. Each qube that left the file:
   [`derive.reset_state`](../dom0/torii/derive.py#L208): no netvm, and none of torii's service,
   feature or tag; its adoption record stays.
6. The proxy qubes, and then the tool's old templates, nothing uses any
   more.

What it may touch at all:

<!-- charter:embed SW-261127-G26T -->
> **[SW-261127-G26T](../claims/apply.md#sw-261127-g26t-apply-changes-only-what-torii-owns-its-template-its-proxy-qubes-its-policy-files-and-named-workload-settings) apply changes only what torii owns: its template, its proxy qubes, its policy files, and named workload settings**
>
> torii creates, changes and removes only its own template, the proxy
> qubes it created (tagged `torii-owned`, and with the mesh's tag for a
> named mesh), its mesh's policy file `50-torii` (or `50-torii-<mesh>`) and
> the shared deny file `59-torii-deny`; it may also download the base
> template its own is cloned from, which it never changes. On a qube the
> file lists it sets only the netvm, the `torii-client` service and its
> feature, the `torii-managed` tag, and the adoption features written
> once, and removes the earlier `torii-netvm-kept` tag. Restarting a proxy
> detaches every running qube behind it and attaches it again, listed or
> not. It never creates or removes a workload, and a qube that only hosts
> a service is not changed.
<!-- /charter:embed -->

The adoption record and the way out are one entry, because the second
rests on the first:

<!-- charter:embed SW-261127-VPGK -->
> **[SW-261127-VPGK](../claims/apply.md#sw-261127-vpgk-a-qube-adopted-records-its-netvm-once-a-qube-leaving-the-file-is-left-with-no-netvm) A qube adopted records its netvm once; a qube leaving the file is left with no netvm**
>
> The first apply that lists a qube records the netvm it had, in
> `torii-prior-netvm` and `torii-adopted`, and never writes them again. A
> qube torii manages that the file no longer lists is set to no netvm,
> loses the client's service and feature and the managed tag, and keeps
> its record. A qube still carrying the earlier `torii-netvm-kept` tag
> keeps the netvm it has, since that was never torii's to set. When the
> record names a netvm other than none and the one it has, plan names it
> and the command to restore it; it never restores it.
<!-- /charter:embed -->

The trap is in step 5: a qube leaving the file is not given its old netvm
back. That netvm may send directly what the file had been sending through
the proxy or an egress, so plan names it, and the `qvm-prefs` command to
restore it, and leaves the choice to the owner.

[`plan.notes`](../dom0/torii/plan.py#L316) adds lines that change nothing, such
as a qube on an egress whose LAN traffic still leaves through the proxy's
netvm ([SW-261127-KYZW](../claims/egress.md#sw-261127-kyzw-a-qubes-lan-traffic-never-goes-through-its-egress-and-plan-says-so)). `torii plan` prints the steps and the notes;
a machine that already matches the file plans nothing.

## 4. apply

`torii apply` does not run a stored plan. It reads the file and the machine
again, makes the plan again, prints it, and asks; the steps you say yes to
are the ones it makes. An earlier `torii plan` on a machine that has since
changed shows other steps, and apply shows the new ones before asking.

[`Applier.run`](../dom0/torii/apply.py#L45) then makes the steps one at a time,
each by its `do_` method:

<!-- charter:embed SW-261127-63CS -->
> **[SW-261127-63CS](../claims/apply.md#sw-261127-63cs-apply-makes-the-plans-steps-in-order-stops-at-the-first-that-fails-and-a-second-run-continues) apply makes the plan's steps in order, stops at the first that fails, and a second run continues**
>
> apply makes plan's steps one at a time, in plan's order, naming each.
> The first step that fails stops it, naming the step and the cause, and
> no later step is made. Run again, plan starts from the machine as the
> stopped run left it, so it holds only the steps not yet made; on a
> machine that matches the file it holds none.
<!-- /charter:embed -->

Since every step is a difference from the snapshot, a step that was made
is not planned again: running apply after a failure makes only what is
left.

## 5. The config reaches the proxy

The proxy's config cannot travel as one feature: QubesDB refuses a value
of 3 KB or more. [`derive.encode_chunks`](../dom0/torii/derive.py#L135)
compresses the JSON and splits it into parts, with a head holding the
count and a hash of the whole.
[`Applier.do_proxy_config`](../dom0/torii/apply.py#L166) writes the parts
first, the head last and removes stale parts after it, so a reader in
between sees a hash that does not match.

In the proxy qube, the program watches QubesDB; on a change
[`waitForNewConfig`](../proxy/cmd/torii-proxy/main.go#L165) waits half a second
for dom0 to finish, then [`qubesdb.Config`](../proxy/internal/qubesdb/qubesdb.go#L42)
joins the parts and checks the hash. A config that does not check, or does
not parse, is not taken, and the program goes on running the old one:

<!-- charter:embed SW-261127-XGDY -->
> **[SW-261127-XGDY](../claims/apply.md#sw-261127-xgdy-a-proxy-takes-its-config-whole-or-not-at-all-and-a-failed-check-never-replaces-the-running-one) A proxy takes its config whole or not at all, and a failed check never replaces the running one**
>
> dom0 hands each proxy its config as the features `vm-config.torii*`: the
> JSON compressed, encoded and split into parts under QubesDB's limit, with
> a head of the part count and the JSON's SHA-256. apply writes the parts
> first and the head last, then removes stale parts. The proxy joins the
> parts, decodes them and checks the hash and the config itself; one that
> fails any check is not taken, and the proxy keeps running the config it
> has, or waits for a valid one if it has none. A valid one the program
> then fails to start on ends the process, and its service restarts it.
<!-- /charter:embed -->

A valid config with a new version restarts the program in-process
([`run`](../proxy/cmd/torii-proxy/main.go#L89)): identities keep their state
on disk and reconnect without a new sign-in, and open connections end.

## 6. apply waits for it

The step after a config is
[`Applier.do_proxy_ready`](../dom0/torii/apply.py#L181): it starts the proxy
qube if it is not running, then reads its status over `torii.Status` until
the program reports the config's version, printing each sign-in link it
sees on the way ([SW-261127-3TPX](../claims/identity.md#sw-261127-3tpx-an-identity-signs-in-by-a-link-a-person-approves-or-a-one-time-key-that-only-the-proxy-qube-keeps)):

<!-- charter:embed SW-261127-QMVP -->
> **[SW-261127-QMVP](../claims/apply.md#sw-261127-qmvp-apply-goes-on-past-a-proxys-config-only-once-the-proxy-reports-running-it) apply goes on past a proxy's config only once the proxy reports running it**
>
> After writing a proxy's config, apply starts the proxy if it is not
> running, and waits until the proxy's status reports that config's
> version, showing any sign-in links as they appear. If the proxy does not
> report it in time, the step fails, naming what it last reported.
<!-- /charter:embed -->

In the plan the proxies come before the policy and the qubes' netvms, so
a qube is pointed at a proxy only once that proxy has reported the new
config.

## 7. Reading it back

`torii show` never asks the file what a qube can reach. It takes a
snapshot, reads each proxy's status and log over `torii.Status`, and
[`show.qube_view`](../dom0/torii/show.py#L97) works out each qube's reach from
those alone, parsing every policy file, hand-written ones included
([`show.rules`](../dom0/torii/show.py#L39)). The file, if there is one, only
picks which qubes to list:

<!-- charter:embed SW-261127-62WV -->
> **[SW-261127-62WV](../claims/seeing.md#sw-261127-62wv-show-reports-each-qubes-reach-from-the-live-machine-never-from-the-file) show reports each qube's reach from the live machine, never from the file**
>
> show reads each qube's netvm and tags, every qrexec policy file in dom0,
> hand-written ones included, each proxy's config as its features hold it,
> and each proxy's status; the machine file only chooses which qubes are
> listed. A call is read as allowed as qrexec reads policy, for exact qube
> names and `@anyvm`: files in name order; the first rule for a source,
> the target asked for and an argument deciding; a rule for every
> service, `*`, counting for each; a redirect reaching only its new
> target; `ask` shown as reach on a prompt; an allow for every argument
> shown as `*`. Other tokens (`@tag:`, `@type:`, `@dispvm`) and included
> files are not evaluated; check names the rules that use them. A qube
> behind no proxy is shown as reaching what its netvm allows.
<!-- /charter:embed -->

`torii check` is the same reading set against the file.
[`show.check_problems`](../dom0/torii/show.py#L365) runs `plan.make` and reports
every step it would make as drift, then adds what a plan cannot see, among them a
proxy that is down or runs another config than its features hold, an
identity not signed in or carrying other tags ([SW-261127-P8H7](../claims/identity.md#sw-261127-p8h7-a-tagged-identity-carries-exactly-the-files-tags-prefixed-by-the-machine-and-check-names-any-other)), a
qube attached to a proxy but not in the file, and hand-written policy that
widens what torii set:

<!-- charter:embed SW-261127-SMYC -->
> **[SW-261127-SMYC](../claims/seeing.md#sw-261127-smyc-check-names-each-difference-it-looks-for-between-the-live-machine-and-the-file-and-fails-on-one) check names each difference it looks for between the live machine and the file, and fails on one**
>
> check names, one line each: every step plan would make (drift), and
> every reason plan refuses the file; a proxy whose config features cannot
> be read, that is not running, that gives no status, or that runs another
> version than its features hold; an identity not running, with its
> sign-in link, or whose tags differ from the file's; an egress with no
> healthy exit node; a qube attached to a proxy but not listed that was
> refused; a disposable that can leave by its template's netvm; and, in
> any policy file but this mesh's own and the shared deny file, a line
> allowing, or asking for, `torii.Connect`, `torii.Status`, every service,
> or `qubes.ConnectTCP` from or to a listed qube, a proxy or a token,
> marked when it uses a token check does not evaluate, and every
> `!include`. It exits 0 only when it names nothing. It does not read the
> qubes' firewall rules or the rules inside a proxy qube.
<!-- /charter:embed -->

What `torii log QUBE` prints is the decision log the proxy kept,
[SW-261127-R4CD](../claims/seeing.md#sw-261127-r4cd-every-connection-the-proxy-decides-is-logged-with-its-verdict-and-the-rule-that-made-it), read over the same read-only service
([SW-261127-B8BM](../claims/seeing.md#sw-261127-b8bm-the-status-service-is-read-only-it-reports-and-changes-nothing-in-the-proxy)).
