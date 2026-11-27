# Questions

What nobody has ruled about torii's charter: a heading `## Q-ID
question`, the id from `charter next Q`, then `Asked-by:`, `Entry:` (an
id, or `ID/handle`), `Provisional:` and, once a person has answered,
`Answer: YYYY-MM-DD, who: ...`.

## Q-261127-YZQC Under an allowlist, should a connection that sends no TLS server name, plain TCP or a ClientHello without one, be refused rather than decided on the names alone?
Asked-by: agent
Entry: SW-261127-6S2F
Provisional: decided on the names alone, as the proxy does now; such a connection to an allowed name's address reaches whatever site that address serves, the shared-address limit KZK8 states. Refusing it would break plain HTTP and non-TLS protocols to allowed names unless the entry opted in per port.
Answer: 2026-11-27, owner: keep; a connection that sends no server name is decided on the names alone, a limit STK-261126-KZK8 states.
