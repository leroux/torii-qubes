#!/bin/sh
systemctl enable torii-guard.service torii-proxy.service >/dev/null 2>&1 || true
