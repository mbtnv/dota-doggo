#!/usr/bin/env bash
# Linux VPS release transaction. .env is provisioned locally, never sourced.
set -Eeuo pipefail
umask 077

root=${DOTA_DOGGO_DEPLOY_ROOT:-/opt/dota-doggo}
release=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
[[ -d "$root" && -f "$root/.env" && -f "$release/compose.yml" ]] || {
  echo 'Deployment directory, .env or compose.yml is missing' >&2
  exit 1
}
exec 9>"$root/.deploy.lock"
flock -n 9 || { echo 'Another deployment is running' >&2; exit 1; }

registry_config=
link_dir=
cleanup() {
  if [[ -n "$registry_config" ]]; then rm -rf -- "$registry_config"; fi
  if [[ -n "$link_dir" ]]; then rm -rf -- "$link_dir"; fi
}
trap cleanup EXIT

if [[ ${1:-} == --github && $# == 1 ]]; then
  IFS= read -r actor
  IFS= read -r token
  IFS= read -r image
  [[ "$actor" =~ ^[a-zA-Z0-9_.-]+(\[bot\])?$ && -n "$token" ]] || exit 1
elif [[ $# == 1 ]]; then
  image=$1
else
  echo 'Usage: deploy.sh ghcr.io/owner/repo@sha256:DIGEST | deploy.sh --github' >&2
  exit 1
fi
valid_image() { [[ "$1" =~ ^ghcr\.io/[a-z0-9._-]+/[a-z0-9._-]+@sha256:[a-f0-9]{64}$ ]]; }
valid_image "$image" || { echo 'Expected a GHCR image pinned by digest' >&2; exit 1; }
if [[ ${1:-} == --github ]]; then
  registry_config=$(mktemp -d)
  export DOCKER_CONFIG="$registry_config"
  printf '%s' "$token" | docker login ghcr.io --username "$actor" --password-stdin >/dev/null
  unset token
fi

export DOTA_DOGGO_ENV_FILE="$root/.env"
compose() {
  local directory=$1 selected_image=$2
  shift 2
  DOTA_DOGGO_IMAGE="$selected_image" docker compose --project-name dota-doggo \
    --env-file "$root/.env" -f "$directory/compose.yml" "$@"
}
previous=
previous_image=
if [[ -e "$root/current" ]]; then
  previous=$(readlink -f -- "$root/current")
  [[ -f "$previous/image" && -f "$previous/compose.yml" ]] || exit 1
  previous_image=$(cat -- "$previous/image")
  valid_image "$previous_image" || exit 1
  [[ "$release" != "$previous" ]] || { echo 'Use a new release directory for each deployment' >&2; exit 1; }
fi

# Pull and validate before stopping a working release.
compose "$release" "$image" config --quiet
docker pull "$image"
mkdir -p -- "$root/backups"
printf '%s\n' "$image" >"$release/image"
stopped=false
backup=
backup_complete=false
on_failure() {
  local status=$1
  trap - ERR INT TERM HUP
  set +e
  echo 'Deployment failed' >&2
  if [[ -n "$backup" && "$backup_complete" == false ]]; then rm -f -- "$backup"; fi
  if [[ "$stopped" == true ]]; then
    compose "$release" "$image" stop bot worker
    if [[ -n "$previous" ]]; then
      # Never restore data automatically or run an incompatible old binary.
      if compose "$previous" "$previous_image" run --rm --no-deps bot migrate &&
         compose "$previous" "$previous_image" up -d --no-deps --wait --wait-timeout 180 bot worker; then
        echo 'Previous Go release restored' >&2
      else
        echo 'Previous release is not compatible or not ready; inspect the backup and logs' >&2
      fi
    fi
  fi
  exit "$status"
}
trap 'on_failure "$?"' ERR
trap 'on_failure 130' INT
trap 'on_failure 143' TERM
trap 'on_failure 129' HUP

compose "$release" "$image" up -d --wait --wait-timeout 120 db
# Stop the old polling/worker before migrations or new processes start.
stopped=true
compose "$release" "$image" stop bot worker
backup="$root/backups/$(date -u +%Y%m%dT%H%M%S)-$(basename -- "$release").dump"
compose "$release" "$image" exec -T db pg_dump -U postgres -d dota_doggo -Fc >"$backup"
backup_complete=true
compose "$release" "$image" run --rm --no-deps bot migrate
compose "$release" "$image" up -d --no-deps --wait --wait-timeout 180 bot worker

# A successful release is the only one eligible for a subsequent rollback.
link_dir=$(mktemp -d "$root/.current.XXXXXXXX")
ln -s -- "$release" "$link_dir/current"
mv -Tf -- "$link_dir/current" "$root/current"
trap - ERR INT TERM HUP
echo "Deployment ready: $image"
