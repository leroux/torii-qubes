# Seeing the machine

Code: dom0/torii/show.py, dom0/torii/status.py, proxy/internal/admin, proxy/internal/events

This area answers STK-261126-4Q75. It covers `torii show` and `torii
check` reading the live machine, the status service that carries a
proxy's state and log back to dom0, and the decision log with the reason
for each decision.

## SW-261127-62WV torii show MUST report each qube's reach from the live machine, never from the file

Status: active
Kind: invariant
Serves: STK-261126-4Q75

- `torii show` MUST read each qube's netvm and tags, every qrexec policy
  file in dom0 (hand-written ones included), each proxy's config as its
  features hold it, and each proxy's status. The machine file MUST only
  choose which qubes are listed.
- show MUST read a call as allowed the way qrexec reads policy, for exact
  qube names and `@anyvm`:
  - files in name order;
  - the first rule for a source, the target asked for and an argument
    decides;
  - a rule for every service, `*`, counts for each service;
  - a redirect reaches only its new target;
  - `ask` is shown as reach on a prompt;
  - an allow for every argument is shown as `*`.
- Other tokens (`@tag:`, `@type:`, `@dispvm`) and included files are not
  evaluated; `torii check` names the rules that use them.
- A qube behind no proxy MUST be shown as reaching what its netvm allows.

Why: a configuration nobody can read back is one nobody can trust. The
live machine is what actually decides a qube's reach, so show reads it
and nothing else, and anything the file did not set, such as a
hand-written policy line or a netvm changed by hand, appears as it is.

Scenarios:
1. `hand-policy-shown` (agent, provisional): given a hand-written line
   letting a qube, or `@anyvm`, reach a qube's port by an allow, an `ask`
   or the wildcard service, when show runs, then the line appears in that
   qube's local inbound with its file.
2. `first-match-wins` (agent, provisional): given a deny, or a redirect
   elsewhere, in an earlier file and an allow for the same call in a
   later one, when show runs, then the allow is hidden.
3. `live-netvm-shown` (agent, provisional): given a qube whose netvm was
   changed by hand, when show runs, then the qube is shown on the netvm
   it has, not the file's.

Changes:
- 2026-11-27: show read only files that named torii's services, matched
  only exact names and @anyvm, read a redirect by its new target and took
  `ask` for no reach; it now reads every file, the wildcard service,
  redirects by the target asked for and `ask` as reach on a prompt, and
  the text names the tokens and includes it does not evaluate.

## SW-261127-SMYC torii check MUST name each difference it looks for between the live machine and the file, and MUST fail on any

Status: active
Kind: invariant
Serves: STK-261126-4Q75

`torii check` MUST name, one line each:

- every step plan would make (drift), and every reason plan refuses the
  file;
- a proxy whose config features cannot be read, that is not running,
  that gives no status, or that runs another version than its features
  hold;
- an identity that is not running, with its sign-in link, or whose tags
  differ from the file's;
- an egress with no healthy exit node;
- a qube attached to a proxy but not listed that was refused;
- a disposable that can leave by its template's netvm;
- in any policy file other than this mesh's own and the shared deny
  file: a line allowing, or asking for, `torii.Connect`, `torii.Status`,
  every service, or `qubes.ConnectTCP` from or to a listed qube, a proxy
  or a token, marked when it uses a token check does not evaluate; and
  every `!include`.

check MUST exit 0 only when it names nothing. It does not read the qubes'
firewall rules or the rules inside a proxy qube.

Why: drift that nobody sees is drift everybody trusts. A single command
that fails on any difference is something the owner, or a script, can
run to know that the machine still matches the file.

Scenarios:
1. `drift-named` (agent, provisional): given a qube whose netvm was
   changed by hand, when check runs, then it names the qube with the step
   that would put it back.
2. `stale-config-named` (agent, provisional): given a proxy running an
   older version than its features hold, when check runs, then it names
   the proxy.
3. `widening-policy-named` (agent, provisional): given a hand-written
   line allowing `torii.Connect` from a qube, when check runs, then it
   names the line with its file.
4. `clean-is-zero` (agent, provisional): given a machine that matches the
   file, when check runs, then it names nothing and exits 0.

Changes:
- 2026-11-27: any file whose name began with 50-torii was exempt, so a
  hand-written one passed silently; now only this mesh's file and the deny
  file are, `ask`, the wildcard service, tokens and includes are named,
  and the text says what check does not read.

## SW-261127-R4CD The proxy MUST log every connection it decides, with the verdict and the rule that made it

Status: active
Kind: invariant
Serves: STK-261126-4Q75

- The proxy MUST log one event for each connection it decides, and for
  each UDP flow's first datagram, holding: the qube, the direction, how
  the connection arrived, the source and destination, the server name if
  read, the verdict and the reason; and, for an allowed connection, the
  path, the identity and the name it rested on, or the error when it
  could not be made.
- Reasons MUST be fixed values, which show and the tests read.
- A qube at its UDP flow cap MUST be logged at most once a second.
- A request too malformed to decide (a SOCKS handshake that fails, or an
  HTTP request answered 400) is answered with no event.
- The proxy MUST keep the last events of each qube in memory and append
  every event to the qube's file. `torii log` prints a qube's recent
  events, matching the qube as the workload or the source.

Why: a refusal nobody can explain gets worked around. Naming the rule
that refused a connection lets the owner fix the file, or see that the
refusal was right.

Scenarios:
1. `refusal-has-reason` (agent, provisional): given a refused connection,
   when its event is read, then it holds the verdict and the reason that
   refused it.
2. `kept-per-qube` (agent, provisional): given many events from one qube,
   when another qube's recent events are read, then they are all still
   there.

Changes:
- 2026-11-27: names what is not one event each: the flow cap's once-a-
  second line and malformed requests answered before any decision; fill is
  anchored.

## SW-261127-B8BM The status service MUST be read-only: it MUST report and MUST NOT change anything in the proxy

Status: active
Kind: invariant
Serves: STK-261126-4Q75, STK-261126-SM33

- `torii.Status` runs the program's status service, which MUST only read
  the proxy's status and recent log from its local API and print them.
- That API MUST answer only GET requests for status, the log and explain,
  and MUST have no request that changes the proxy.
- Only dom0 and the file's `status-readers` may call `torii.Status`
  (SW-261127-M36P).

Why: dom0 must be able to read the proxy without the proxy being able to
reach dom0, and a reader of status must not become a way to change what
the proxy allows. Exposing only reads keeps the one channel from the
proxy qube back to dom0 harmless in both directions.

Scenarios:
1. `api-only-reads` (agent, provisional): given the local API, when it
   receives a request other than GET, or a GET of any other path, then
   it refuses the request.

## SW-261127-Z5ST explain MUST give the decision the proxy would make now, without sending anything

Status: active
Kind: invariant
Serves: STK-261126-4Q75

- explain MUST take a qube, a destination and optionally a server name,
  and return the verdict and reason the proxy would give now, with the
  names it rests on, dialling nothing.
- It MUST decide as a connection would arrive: intercepted for a routed
  qube, and otherwise by SOCKS or, when only that is declared, the HTTP
  proxy; the egress's health and the proxy's own addresses count.
- It does not resolve a name, so a name whose addresses would all be
  refused at dial time can be explained as allowed. A fixed forward is
  explained by its destination.

Why: the owner needs to ask why a connection would be refused before
trying it, and the question about a destination must not itself become a
connection to that destination.

Scenarios:
1. `explain-agrees` (agent, provisional): given a destination, including
   an egress with no exit node up and the proxy's own address, when
   explain and a real connection are both tried, then they give the same
   verdict and reason.
2. `explain-dials-nothing` (agent, provisional): given a destination,
   when it is explained, then no connection to it is opened.

Changes:
- 2026-11-27: explain took SOCKS for every qube with no netvm, refusing an
  HTTP-only one; it now takes the way a real connection arrives, and the
  text says it does not resolve names.
