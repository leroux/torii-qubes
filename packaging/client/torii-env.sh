# torii: point programs at this qube's proxy ports, when dom0 enabled them.
if [ -r /run/qubes-service/torii-client ] && command -v qubesdb-read >/dev/null 2>&1; then
    _qm=$(qubesdb-read /vm-config/torii-client 2>/dev/null)
    case "$_qm" in *'"torii.Connect+http"'*)
        _p=$(printf '%s' "$_qm" | sed -n 's/.*"port":\([0-9]*\),"service":"torii.Connect+http".*/\1/p')
        [ -n "$_p" ] && export HTTP_PROXY=http://127.0.0.1:$_p HTTPS_PROXY=http://127.0.0.1:$_p \
            http_proxy=http://127.0.0.1:$_p https_proxy=http://127.0.0.1:$_p NO_PROXY=localhost,127.0.0.1 ;;
    esac
    case "$_qm" in *'"torii.Connect+socks"'*)
        _p=$(printf '%s' "$_qm" | sed -n 's/.*"port":\([0-9]*\),"service":"torii.Connect+socks".*/\1/p')
        [ -n "$_p" ] && export ALL_PROXY=socks5h://127.0.0.1:$_p ;;
    esac
    unset _qm _p
fi
