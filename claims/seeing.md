# Seeing the machine

Code: dom0/torii/show.py, dom0/torii/status.py, proxy/internal/admin, proxy/internal/events

The need this area answers is STK-261126-4Q75. It holds show and check
reading the live machine, the status service that carries a proxy's
state and log back to dom0, and the decision log with its reasons.

## SW-261127-62WV show reports each qube's reach from the live machine, never from the file

Status: active
Kind: invariant
Serves: STK-261126-4Q75

show reads each qube's netvm and tags, every qrexec policy file in dom0,
hand-written ones included, each proxy's config as its features hold it,
and each proxy's status; the machine file only chooses which qubes are
listed. A call is read as allowed the way qrexec reads policy: files in
name order, the first rule for a source, target and argument deciding,
and an allow for every argument shown as `*`. A qube behind no proxy is
shown as reaching what its netvm allows.

Why: a configuration nobody can read back is one nobody can trust; the
live machine is what decides, and anything the file did not set, a
hand-written policy line or a changed netvm, must show as it is.

Scenarios:
1. `hand-policy-shown` (agent, provisional): a hand-written policy line
   allowing `qubes.ConnectTCP` into a qube shows in that qube's
   local inbound, with the file that allows it.
2. `first-match-wins` (agent, provisional): a deny in an earlier file
   hides an allow for the same call in a later one.
3. `live-netvm-shown` (agent, provisional): a qube whose netvm was changed
   by hand is shown on the netvm it has, not the file's.

## SW-261127-SMYC check names every difference between the live machine and the file, and fails when there is one

Status: active
Kind: invariant
Serves: STK-261126-4Q75

check names, one line each: every step plan would make (drift), and
every reason plan refuses the file; a proxy whose config features cannot
be read, that is not running, that gives no status, or that runs another
version than its features hold; an identity not running, with its
sign-in link, or whose tags differ from the file's; an egress with no
healthy exit node; a qube attached to a proxy but not listed that was
refused; a disposable that can leave by its template's netvm; and a
hand-written policy line allowing `torii.Connect` or `torii.Status`, or
`qubes.ConnectTCP` into a listed qube or from a proxy. It exits 0 only
when it names nothing.

Why: drift unseen is drift trusted; one command that fails on any
difference is what the owner, or a script, can run to know the machine
still says what the file says.

Scenarios:
1. `drift-named` (agent, provisional): a qube whose netvm was changed by
   hand is named with the step that would put it back.
2. `stale-config-named` (agent, provisional): a proxy running an older
   version than its features hold is named.
3. `widening-policy-named` (agent, provisional): a hand-written line
   allowing `torii.Connect` from a qube is named with its file.
4. `clean-is-zero` (agent, provisional): on a machine that matches the
   file, check names nothing.

## SW-261127-R4CD Every decision the proxy makes is logged with its verdict and the rule that made it

Status: active
Kind: invariant
Serves: STK-261126-4Q75

The proxy logs one event for each decision: the qube, the direction,
how it arrived, the source and destination, the server name if read,
the verdict, the reason, and for an allowed one the path, the identity
and the name it rested on, or the error when it could not be made. The
reasons are fixed values that show and tests read. The last events of
each qube are kept in memory and every event appended to the qube's
file. `torii log` prints a qube's recent events, as the workload or the
source.

Why: a refusal nobody can explain gets worked around; naming the rule
that refused it lets the owner fix the file, or know the refusal was
right.

Scenarios:
1. `refusal-has-reason` (agent, provisional): a refused connection's
   event holds the verdict and the reason that refused it.
2. `kept-per-qube` (agent, provisional): the log keeps each qube's last
   events, and one qube's events do not push out another's.

## SW-261127-B8BM The status service is read-only: it reports, and changes nothing in the proxy

Status: active
Kind: invariant
Serves: STK-261126-4Q75, STK-261126-SM33

`torii.Status` runs the program's status service, which only reads the
proxy's status and recent log from its local API and prints them. That
API answers only GET requests for status, the log and explain; it has no
request that changes the proxy. Who may call `torii.Status` is dom0 and
the file's `status-readers` (SW-261127-M36P).

Why: dom0 must read the proxy without the proxy reaching dom0, and a
reader of status must not become a way to change what the proxy allows.

Scenarios:
1. `api-only-reads` (agent, provisional): a request other than GET to the
   local API, and a GET of any other path, is refused.

## SW-261127-Z5ST explain gives the decision the proxy would make, without sending anything

Status: active
Kind: invariant
Serves: STK-261126-4Q75

explain takes a qube, a destination and optionally a server name, and
returns the verdict and reason the proxy would give it now, with the
names it rests on, deciding as a real connection is decided and dialling
nothing.

Why: the owner needs to ask why before trying, and a question about a
destination must not itself be a connection to it.

Scenarios:
1. `explain-agrees` (agent, provisional): explain gives the same verdict
   and reason as a real connection to the same destination.
2. `explain-dials-nothing` (agent, provisional): explaining a destination
   opens no connection to it.
