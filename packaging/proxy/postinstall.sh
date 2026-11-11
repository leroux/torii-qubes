#!/bin/sh
systemctl enable lintel-guard.service lintel-proxy.service >/dev/null 2>&1 || true
