# What torii guarantees

Qubes decides a qube's network access by its netvm alone: with one, the
qube has a full network stack and the firewall limits it by address;
without one, it has no network at all. Most qubes need something between:
a few connections in, a few out, maybe a particular way out. Getting that
by hand spreads rules across netvms, firewall settings, qrexec policy,
nftables and the tailnet's console, and nothing adds them up.

Each need below is a guarantee torii makes to the owner of the machine,
whatever the code that meets it. The first says what a qube gets; the
next two, how the owner says it and reads it back; the next two, the two
things a qube's traffic may be given, a way out and a tailnet identity;
the last, what torii must never cost.

## STK-261126-KZK8 A qube gets exactly the connections its entry lists, in each direction, and nothing else

Status: planned

A qube's entry lists what it may connect to, by name or address and port,
in three zones: internet, LAN and tailnet; and the ports that may reach
it. torii gives it those, with or without a netvm, and refuses everything
else. A qube attached to torii but not listed, a disposable included, gets
nothing unless the file says otherwise.

Why: all or nothing is the gap torii fills; a qube with a netvm and a
wall of rules gets more than it needs the moment a rule is missing, and a
gap in either direction is a way out of the compartment.

Scenarios:
1. `allowed-name-passes` (agent, provisional): a qube allowed pypi.org
   connects to it and is refused any other name.
2. `unlisted-port-refused` (agent, provisional): a connection to a port
   the qube's entry does not list never reaches the qube.
3. `unlisted-qube-gets-nothing` (agent, provisional): a qube attached to
   the proxy but absent from the file is refused every connection.
4. `offline-qube-reaches-out` (agent, provisional): a qube with no netvm
   reaches the one name its entry allows.

## STK-261126-B4CP One file in dom0 declares every qube's connectivity, and apply makes the machine match it

Status: planned

The machine file is the one place a qube's connectivity is declared. plan
shows every change apply would make, in order, before any is made; apply
makes them, stops at the first failure, and run again continues where it
stopped; on a machine that already matches, it changes nothing.

Why: rules spread across five places cannot be reviewed or trusted; a
tool that changes dom0 must say what it will do before it does it, and be
safe to run twice.

Scenarios:
1. `plan-shows-every-change` (agent, provisional): plan on a machine that
   differs from the file lists each change apply makes, in apply's order.
2. `second-apply-changes-nothing` (agent, provisional): apply on a machine
   it has just made match the file plans and makes no change.
3. `interrupted-apply-continues` (agent, provisional): an apply stopped by
   a failed step, run again, makes only the changes left.

## STK-261126-4Q75 The owner can read back what each qube can reach, where that differs from the file, and why each connection was decided

Status: planned

show reads, from the live machine and not from the file, what each qube
can reach and what can reach it. check names each place the live machine
and the file disagree. log says, for each recent connection to or from a
qube, whether it was allowed and by which rule.

Why: a configuration nobody can read back is one nobody can trust; the
live machine is the truth, a difference from the file is what the owner
must be told, and a refusal nobody can explain gets worked around.

Scenarios:
1. `show-reads-the-machine` (agent, provisional): a hand-written policy
   file that widens a qube's access shows in show though the file does
   not allow it.
2. `check-names-drift` (agent, provisional): a qube whose netvm was changed
   by hand is named by check with the netvm the file wants.
3. `log-says-why` (agent, provisional): a refused connection is in the
   qube's log with the rule that refused it.

## STK-261126-346B A qube's traffic leaves the way its entry routes it, or not at all

Status: planned

A qube's entry says which way its traffic goes out: its own netvm, a
proxy qube in front of a netvm of the owner's choosing such as a VPN, or
an egress of tailnet exit nodes tried in order. When that way is down, a
connection fails; it never goes out another way.

Why: a route chosen for privacy that silently falls back to the open
network is worse than none, because the owner believes the traffic is
hidden.

Scenarios:
1. `exits-in-order` (agent, provisional): a qube behind an egress of two
   exit nodes goes out through the first that is up.
2. `no-exit-refuses` (agent, provisional): with every exit node of an
   egress down, a connection from a qube behind it is refused and no
   packet leaves for its destination.

## STK-261126-KHZB Each qube can be its own tailnet device, without merging compartments or keeping a credential in dom0

Status: planned

A qube with a tailnet identity is its own device, with its own tags, so
the tailnet's access rules apply to it alone; qubes sharing a way out
never share a tailnet identity that grants access. A new identity joins
through a sign-in link a person approves; dom0 never holds a key, and the
grants torii suggests are printed for a person to add, never pushed.

Why: one tailnet device per machine merges every qube's access into one;
and a credential in dom0, or grants pushed by a tool, would put the
tailnet's trust in the one place a compromise reaches everything.

Scenarios:
1. `identity-per-qube` (agent, provisional): two qubes with tagged
   identities are two tailnet devices with their own tags.
2. `no-key-in-dom0` (agent, provisional): signing an identity in leaves no
   key or auth secret in dom0.
3. `grants-proposed` (agent, provisional): grants prints a policy for the
   tailnet and changes nothing on it.

## STK-261126-SM33 torii leaves Qubes' isolation no weaker than it found it

Status: planned

A compromise of any part reaches no more than that part was given: a
qube, only what its entry lists; the proxy qube, only the inbound ports
of the qubes it carries, and no call into dom0. dom0 handles no traffic
and its code stays small. torii changes Qubes only through its supported
interfaces, and only for the qubes the file lists.

Why: torii exists to give compartments exactly what they need; a tool
that weakened the compartments, or dom0, to do it would defeat the reason
anyone runs Qubes.

Scenarios:
1. `offline-has-no-netvm` (agent, provisional): a qube with network none
   ends apply with no netvm.
2. `proxy-cannot-call-dom0` (agent, provisional): the policy apply writes
   lets the proxy qube make no qrexec call into dom0.
3. `unlisted-qube-untouched` (agent, provisional): a qube absent from the
   file keeps every setting it had.
