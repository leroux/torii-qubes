#!/bin/bash
# Run in dom0. Gives one development qube access to dom0:
#
#   1. a dom0 shell: qrexec service qubes.VMShell in dom0, allowed from the
#      dev qube. This is what `d0` and `qvm-run --pass-io dom0 ...` use.
#   2. the Admin API (unless --shell-only): the dev qube is added to the
#      admin-*-rwx and admin-policy-rwx includes, so qvm-ls, qvm-prefs,
#      qvm-features, qvm-create and the qrexec PolicyClient work from the dev
#      qube itself, against the live system.
#
# Either one gives the dev qube full control of the machine. Use a dev qube
# you would trust with dom0, on a machine you can afford to break.
#
#   setup-dev-access.sh DEVQUBE               shell + Admin API
#   setup-dev-access.sh DEVQUBE --shell-only  shell only
#   setup-dev-access.sh DEVQUBE --ask         like the default, but the shell
#                                             asks for confirmation on every call
#   setup-dev-access.sh DEVQUBE --uninstall   remove what this script added
#   add -n / --dry-run to any of them to print the changes without making them
#
# Get it into dom0 from the dev qube (adjust the path), and read it first:
#   qvm-run --pass-io DEVQUBE 'cat PATH/TO/setup-dev-access.sh' > setup-dev-access.sh
set -euo pipefail

MARK="# dom0 dev access"
POLICY=/etc/qubes/policy.d/30-dev-access.policy
SHELL_SVC=/etc/qubes-rpc/qubes.VMShell
INC=/etc/qubes/policy.d/include

usage() { sed -n '2,/^set -euo/p' "$0" | sed '$d; s/^# \{0,1\}//'; exit "${1:-0}"; }

VM="" MODE=install SHELL_ONLY=0 ACTION=allow DRY=0
for a in "$@"; do
    case "$a" in
        --shell-only) SHELL_ONLY=1 ;;
        --ask)        ACTION=ask ;;
        --uninstall)  MODE=uninstall ;;
        -n|--dry-run) DRY=1 ;;
        -h|--help)    usage ;;
        -*)           echo "unknown option: $a" >&2; usage 2 ;;
        *)            [ -z "$VM" ] || usage 2; VM=$a ;;
    esac
done
[ -n "$VM" ] || usage 2
[ "$(hostname)" = dom0 ] || { echo "Run this in dom0." >&2; exit 1; }
qvm-check -q "$VM" 2>/dev/null || { echo "No qube named $VM." >&2; exit 1; }

run() {  # run CMD...: do it, or print it with --dry-run
    if [ "$DRY" = 1 ]; then printf '   would: %s\n' "$*"; else "$@"; fi
}
write_file() {  # write_file PATH MODE: stdin to PATH, as root
    if [ "$DRY" = 1 ]; then printf '   would write %s:\n' "$1"; sed 's/^/     | /'; return; fi
    sudo install -m "$2" /dev/stdin "$1"
}
add_line() {  # add_line FILE LINE: append LINE unless FILE already has it
    if sudo grep -qxF -- "$2" "$1"; then echo "   already in $1: $2"; return; fi
    if [ "$DRY" = 1 ]; then echo "   would add to $1: $2"; return; fi
    printf '%s\n' "$2" | sudo tee -a "$1" >/dev/null && echo "   added to $1: $2"
}
del_line() {  # del_line FILE LINE: remove every line equal to LINE
    sudo grep -qxF -- "$2" "$1" 2>/dev/null || return 0
    if [ "$DRY" = 1 ]; then echo "   would remove from $1: $2"; return; fi
    sudo grep -vxF -- "$2" "$1" | sudo tee "$1.dev-access-tmp" >/dev/null
    sudo chmod --reference="$1" "$1.dev-access-tmp"; sudo chown --reference="$1" "$1.dev-access-tmp"
    sudo mv "$1.dev-access-tmp" "$1" && echo "   removed from $1: $2"
}

# Admin API: the documented way is a line per qube in the include files that
# 90-admin-default.policy and 90-admin-policy-default.policy pull in.
# local = admin calls about one qube (@anyvm), and about dom0 itself (@adminvm,
# e.g. `qvm-prefs dom0`); global = calls about the whole system, sent to dom0.
ADMIN_LINES=(
    "$INC/admin-local-rwx|$VM @anyvm allow target=dom0"
    "$INC/admin-local-rwx|$VM @adminvm allow target=dom0"
    "$INC/admin-global-rwx|$VM @adminvm allow target=dom0"
    "$INC/admin-global-rwx|$VM @anyvm allow target=dom0"
    "$INC/admin-policy-rwx|$VM @adminvm allow target=dom0"
)

if [ "$MODE" = uninstall ]; then
    echo "== Removing dom0 dev access for $VM"
    for e in "${ADMIN_LINES[@]}"; do del_line "${e%%|*}" "${e#*|}"; done
    if [ -f "$POLICY" ]; then
        run sudo rm -f "$POLICY"
        if ! sudo grep -rqs 'qubes.VMShell' /etc/qubes/policy.d/*.policy \
           && sudo grep -qF "$MARK" "$SHELL_SVC" 2>/dev/null; then
            run sudo rm -f "$SHELL_SVC"   # ours, and nothing else allows it any more
        fi
    fi
    echo "Done. $VM keeps qubes-core-admin-client in its template; that grants nothing."
    exit 0
fi

echo "== dom0 shell for $VM (policy: $ACTION)"
if [ -e "$SHELL_SVC" ] && ! sudo grep -qF "$MARK" "$SHELL_SVC"; then
    # Someone else's dom0 qubes.VMShell (an earlier hand setup): keep it, it is
    # only a shell either way, and don't remove it on --uninstall.
    echo "   keeping the existing $SHELL_SVC:"; sed 's/^/     | /' "$SHELL_SVC"
else
    printf '#!/bin/sh\n%s: a shell in dom0 for qrexec callers that\n# /etc/qubes/policy.d allows (see 30-dev-access.policy). Remove with --uninstall.\nexec /bin/bash\n' \
        "$MARK" | write_file "$SHELL_SVC" 0755
fi
{
    echo "$MARK (setup-dev-access.sh)."
    echo "# Remove with: setup-dev-access.sh $VM --uninstall"
    echo "qubes.VMShell  *  $VM  dom0  $ACTION"
} | write_file "$POLICY" 0644

if [ "$SHELL_ONLY" = 0 ]; then
    echo "== Admin API for $VM"
    for e in "${ADMIN_LINES[@]}"; do add_line "${e%%|*}" "${e#*|}"; done

    # The qvm-* tools and python3-qubesadmin come from qubes-core-admin-client,
    # which has to be in the template: an AppVM's /usr resets on restart.
    TEMPLATE=$(qvm-prefs "$VM" template 2>/dev/null || true)
    if [ -z "$TEMPLATE" ]; then
        echo "   $VM has no template: install qubes-core-admin-client in it yourself."
    elif qvm-run -q --pass-io --no-autostart "$TEMPLATE" 'command -v qvm-ls' >/dev/null 2>&1; then
        echo "   $TEMPLATE already has qubes-core-admin-client."
    else
        echo "   installing qubes-core-admin-client in $TEMPLATE (restart $VM afterwards)"
        run qvm-start --skip-if-running "$TEMPLATE"
        run qvm-run --pass-io -u root "$TEMPLATE" \
            'if command -v dnf >/dev/null; then dnf install -y qubes-core-admin-client;
             else apt-get update && apt-get install -y qubes-core-admin-client; fi'
        run qvm-shutdown --wait "$TEMPLATE"
    fi
fi

echo
echo "Done. Check from $VM:"
echo "    echo 'hostname; id' | qrexec-client-vm dom0 qubes.VMShell    # dom0 shell"
[ "$SHELL_ONLY" = 0 ] && echo "    qvm-ls dom0                                                  # Admin API"
exit 0
