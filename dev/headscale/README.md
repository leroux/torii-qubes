# A test tailnet on a local Headscale

Everything on one Qubes machine, no Tailscale account:

| Qube | What |
|---|---|
| `lintel-headscale` | Headscale (control, `198.18.0.1:8080`) and Tailscale's `derper` (relay, TLS on `:3340`, STUN on `:3478`). It is the netvm of the qubes that use this tailnet, so traffic to it never crosses Qubes' qube-to-qube block. |
| `sys-lintel-test` | The test mesh's proxy qube (`lintel-test.yaml`: `mesh: test`), created by `lintel apply`. Netvm `lintel-headscale`. |
| `lintel-peer` | A test peer (stands in for a laptop): `tailscaled` in userspace, an exit node. Netvm `lintel-headscale`. |

The address `198.18.0.1` is on `lintel-headscale`'s loopback: proxy qubes refuse to
open connections to Qubes' own `10.137.0.0/16`, so the control server cannot be
reached at its Qubes address.

## Why the relay

Qubes blocks direct traffic between qubes, and the proxy's guard refuses Qubes
addresses, so devices in different qubes cannot use a direct WireGuard path: they
talk through the relay. Tailscale clients only speak TLS to a relay (the relay
map's `insecurefortests` is honoured in Go test binaries only), so `derper` serves
TLS with a certificate for `198.18.0.1`, and the relay map pins it:
`certname: sha256-raw:<hash>`. No CA is installed anywhere.

## Set up (in dom0, from a dev qube with this checkout)

1. Binaries into a folder `hs/` in the dev qube: Headscale 0.29.7 from its release
   (check `checksums.txt`), and `derper` built at the proxy's tailscale version:
   `go mod init x && go get tailscale.com/cmd/derper@v1.104.5 && CGO_ENABLED=0 go build tailscale.com/cmd/derper`.
2. The qube: `qvm-create -C AppVM -t <a Fedora template> -l orange --prop provides_network=True --prop netvm=sys-firewall lintel-headscale`.
3. Copy `hs/` (binaries and this folder's `config.yaml`, `derp.yaml`, `policy.hujson`,
   `rc.local`) to `/home/user/hs` in it, make a certificate for `198.18.0.1` in
   `/home/user/hs/certs/198.18.0.1.{crt,key}` (`openssl`, SAN `IP:198.18.0.1`), install
   `rc.local` as `/rw/config/rc.local` and run it.
4. `derper` prints `CertName: sha256-raw:...` at start: put it in `derp.yaml`, restart
   Headscale. `headscale users create lintel`.
5. The test mesh: copy `lintel-test.yaml` to dom0 and `lintel -f ~/lintel-test.yaml apply`.
   Approve each identity's sign-in link: `headscale auth register --user lintel --auth-id <hskey-authreq-...>`
   (the ids are in `lintel -f ~/lintel-test.yaml show`).
6. The peer: netvm `lintel-headscale`; `tailscaled --tun=userspace-networking --socks5-server=127.0.0.1:1055`,
   then `tailscale up --login-server http://198.18.0.1:8080 --authkey <key> --hostname lintel-peer --advertise-exit-node`,
   the key from `headscale preauthkeys create --user 1 --tags tag:lintel-peer,tag:lintel-exit`
   (Headscale takes tags from the key, and refuses advertised tags with a key).

The grants are in `policy.hujson`; `headscale policy check -f policy.hujson` checks it.
