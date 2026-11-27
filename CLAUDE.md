# torii

Per-qube network policy for Qubes OS. Start with `README.md`, then
`docs/README.md`; what torii guarantees is in `claims/` (needs.md first).

Write everything here (the record, docs, commit messages and replies) as
@WRITING.md describes.

## How claims are written

The owner chose this form on 2026-11-27. Write every entry in `claims/` to it.

- **Title:** the component that carries the obligation, with a BCP 14
  keyword, as one claim a reviewer could disagree with: "The proxy MUST
  check an allowlisted connection's TLS server name before relaying it".
- **Body:** RFC 2119 bullets, one requirement per bullet, by default.
  Use connected prose instead when the entry is a single requirement, or
  when its clauses only make sense together (a sequence, a cause).
- **Key words:** MUST, MUST NOT, SHOULD, SHOULD NOT and MAY, in capitals
  only, with their BCP 14 meanings (RFC 2119, RFC 8174), as `needs.md`
  states.
- **Why:** explain the problem or threat, how the rule meets it, and what
  the rule costs or avoids.
- **Scenarios:** one numbered line each, in given / when / then form, with
  a concrete input and an observable outcome.
