#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
checkout=$(pwd -P)
[[ -f .deploy.env ]] || { echo 'Copy .deploy.env.example to .deploy.env first.' >&2; exit 1; }
# Only this file supplies deployment settings, never Task's development .env.
unset DEPLOY_HOST DEPLOY_DIR DEPLOY_URL
source .deploy.env
: "${DEPLOY_HOST:?Set DEPLOY_HOST in .deploy.env}"
: "${DEPLOY_DIR:?Set DEPLOY_DIR in .deploy.env}"
: "${DEPLOY_URL:?Set DEPLOY_URL in .deploy.env}"
[[ $DEPLOY_HOST =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.@-]*$ ]] || { echo 'Use local or an SSH host alias.' >&2; exit 1; }
[[ $DEPLOY_DIR == /* && $DEPLOY_DIR != / ]] || { echo 'DEPLOY_DIR must be an absolute installation directory, not /.' >&2; exit 1; }
[[ $DEPLOY_URL == https://* ]] || { echo 'DEPLOY_URL must use HTTPS.' >&2; exit 1; }

on_host() {
    if [[ $DEPLOY_HOST == local ]]; then
        "$@"
    else
        local command
        printf -v command '%q ' "$@"
        ssh -o BatchMode=yes "$DEPLOY_HOST" "$command"
    fi
}

for command in task rsync curl; do command -v "$command" >/dev/null; done
[[ $(uname -sm) == 'Linux x86_64' ]] || { echo 'Build on Linux x86_64 (the binary is built for the build machine).' >&2; exit 1; }

# Check the target before building or stopping anything. No changes here.
on_host bash -s -- "$DEPLOY_DIR" "$checkout" <<'HOST'
set -euo pipefail
dest=$(realpath -m -- "$1")
checkout=$2
[[ $dest != / && $dest != "$checkout" && $dest != "$checkout/"* && $checkout != "$dest/"* ]] || { echo 'Installation and checkout directories must not overlap.' >&2; exit 1; }
for path in "$1" "$dest/data" "$dest/backups"; do
    [[ ! -L $path ]] || { echo "Refusing symlinked deployment path: $path" >&2; exit 1; }
done
[[ $(uname -sm) == 'Linux x86_64' ]] || { echo 'The target must be Linux x86_64.' >&2; exit 1; }
for command in rsync systemctl; do command -v "$command" >/dev/null; done
[[ $(systemctl show -p WorkingDirectory --value poople.service) == "$dest" ]] || { echo 'Install poople.service with WorkingDirectory matching DEPLOY_DIR first.' >&2; exit 1; }
sudo -n true
HOST

task check # Includes the production build.
task race
test -x bin/poople

on_host bash -s -- "$DEPLOY_DIR" <<'HOST'
set -euo pipefail
umask 077
dest=$1
mkdir -p "$dest/backups"
sudo -n systemctl stop poople.service
# Copy the entire stopped data directory, including SQLite WAL files.
if [[ -d $dest/data ]]; then
    backup=$(mktemp -d "$dest/backups/$(date -u +%Y%m%d-%H%M%S).XXXXXX")
    cp -a -- "$dest/data" "$backup/data"
    echo "Backup: $backup/data"
fi
HOST

dest=$DEPLOY_DIR
[[ $DEPLOY_HOST == local ]] || dest="$DEPLOY_HOST:$DEPLOY_DIR"
# Only the binary is replaced; data, backups and .env stay untouched.
rsync -a -s -e 'ssh -o BatchMode=yes' bin/poople "$dest/"

# A backup/copy failure exits above and leaves the service stopped.
on_host sudo -n systemctl enable --now poople.service
status=$(curl --fail --silent --show-error --retry 10 --retry-delay 1 --retry-all-errors \
    --connect-timeout 2 --max-time 10 --output /dev/null --write-out '%{http_code}' "${DEPLOY_URL%/}/")
[[ $status == 200 ]] || { echo "Health check returned HTTP $status." >&2; exit 1; }
echo "Deployed to $DEPLOY_HOST:$DEPLOY_DIR ($DEPLOY_URL)"
