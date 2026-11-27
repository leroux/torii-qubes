# Observations

Facts about systems torii does not own, that its decisions rest on.

## QubesAntispoof

Subject: qubes
Status: unsure
Supports: SW-261127-BHHX

A Qubes netvm drops packets arriving on a qube's `vif` interface whose
source address is not that qube's own; a qube cannot send as another.
