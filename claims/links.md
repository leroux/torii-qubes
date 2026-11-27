# Reaching the proxy

Code: proxy/internal/qrexec, proxy/internal/intercept, proxy/internal/socks5, proxy/cmd/torii-client, packaging/proxy

The needs this area answers are STK-261126-KZK8 and STK-261126-SM33. It
holds how a carried qube's connection reaches the proxy program and how
the program knows which qube sent it: qrexec calls in the direction of
the connection for a qube with no netvm, interception with kernel
forwarding off for a routed qube, the boot-time guard that keeps
forwarding off, and the client's ports in the qube.
