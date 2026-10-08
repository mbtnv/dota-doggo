#!/usr/bin/env bash
# Exercise release failure paths without SSH, registries or real databases.
set -Eeuo pipefail
test_dir=$(mktemp -d)
trap 'rm -rf -- "$test_dir"' EXIT
mkdir -p "$test_dir/bin"
export PATH="$test_dir/bin:$PATH"
OLD="ghcr.io/example/dota-doggo@sha256:$(printf '1%.0s' {1..64})"
NEW="ghcr.io/example/dota-doggo@sha256:$(printf '2%.0s' {1..64})"
export OLD NEW
cat >"$test_dir/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == login ]]; then
  cat >"$DOCKER_CONFIG/token"
  printf '%s\n' "$DOCKER_CONFIG" >"$MOCK_ROOT/login-directory"
  echo login >>"$MOCK_ROOT/calls"
  exit 0
fi
if [[ "$1" == pull ]]; then
  echo "pull $2" >>"$MOCK_ROOT/calls"
  [[ ${FAIL:-} != pull ]]
  exit
fi
[[ "$1" == compose ]]
shift
while [[ "$1" == --project-name || "$1" == --env-file || "$1" == -f ]]; do shift 2; done
printf '%s|%s\n' "$DOTA_DOGGO_IMAGE" "$*" >>"$MOCK_ROOT/calls"
case "$1" in
  config) [[ ${FAIL:-} != config ]] ;;
  stop) exit 0 ;;
  exec)
    printf 'fixture backup'
    [[ ${FAIL:-} != backup ]] ;;
  run)
    if [[ "$DOTA_DOGGO_IMAGE" == "$NEW" ]]; then
      [[ ${FAIL:-} != migrate && ${FAIL:-} != incompatible ]]
    else
      [[ ${FAIL:-} != incompatible ]]
    fi ;;
  up)
    if [[ "$*" == *' bot worker' && "$DOTA_DOGGO_IMAGE" == "$NEW" ]]; then
      [[ ${FAIL:-} != readiness ]]
    fi ;;
  *) exit 2 ;;
esac
MOCK
chmod +x "$test_dir/bin/docker"

setup() {
  export MOCK_ROOT="$test_dir/$1"
  export DOTA_DOGGO_DEPLOY_ROOT="$MOCK_ROOT"
  mkdir -p "$MOCK_ROOT/releases/new"
  printf 'BOT_TOKEN=fixture\nPOSTGRES_PASSWORD=fixture-only\n' >"$MOCK_ROOT/.env"
  : >"$MOCK_ROOT/calls"
  cp /src/deploy/deploy.sh /src/deploy/compose.yml "$MOCK_ROOT/releases/new/"
  if [[ ${2:-} == previous ]]; then
    mkdir -p "$MOCK_ROOT/releases/old"
    cp /src/deploy/compose.yml "$MOCK_ROOT/releases/old/"
    printf '%s\n' "$OLD" >"$MOCK_ROOT/releases/old/image"
    ln -s "$MOCK_ROOT/releases/old" "$MOCK_ROOT/current"
  fi
  export FAIL=
}
run_deploy() { bash "$MOCK_ROOT/releases/new/deploy.sh" "$NEW" >"$MOCK_ROOT/output" 2>&1; }
fails() { if "$@"; then echo 'Unexpected command success' >&2; exit 1; fi; }
old_current() { [[ $(readlink -f "$MOCK_ROOT/current") == "$MOCK_ROOT/releases/old" ]]; }
old_restarted() { grep -Fq "$OLD|up -d --no-deps --wait --wait-timeout 180 bot worker" "$MOCK_ROOT/calls"; }

setup first
run_deploy
[[ $(readlink -f "$MOCK_ROOT/current") == "$MOCK_ROOT/releases/new" ]]
[[ $(cat "$MOCK_ROOT/current/image") == "$NEW" ]]
[[ $(find "$MOCK_ROOT/backups" -type f | wc -l) == 1 ]]
grep -Fq "$NEW|stop bot worker" "$MOCK_ROOT/calls"
echo 'PASS initial release on a fresh database'

setup update previous
run_deploy
[[ $(find "$MOCK_ROOT/backups" -type f | wc -l) == 1 ]]
stop_line=$(grep -nF "$NEW|stop bot worker" "$MOCK_ROOT/calls" | cut -d: -f1)
dump_line=$(grep -nF "$NEW|exec -T db pg_dump" "$MOCK_ROOT/calls" | cut -d: -f1)
migrate_line=$(grep -nF "$NEW|run --rm --no-deps bot migrate" "$MOCK_ROOT/calls" | cut -d: -f1)
[[ "$stop_line" -lt "$dump_line" && "$dump_line" -lt "$migrate_line" ]]
echo 'PASS update stops processes before backup and migration'

for failure in config pull; do
  setup "$failure" previous
  export FAIL="$failure"
  fails run_deploy
  old_current
  fails grep -Fq '|stop' "$MOCK_ROOT/calls"
  echo "PASS $failure failure keeps the running release"
done
for failure in backup migrate readiness; do
  setup "$failure" previous
  export FAIL="$failure"
  fails run_deploy
  old_current
  old_restarted
  if [[ "$failure" == backup ]]; then
    [[ -z $(find "$MOCK_ROOT/backups" -type f -print) ]]
    fails grep -Fq "$NEW|run" "$MOCK_ROOT/calls"
  else
    [[ $(find "$MOCK_ROOT/backups" -type f | wc -l) == 1 ]]
  fi
  fails grep -Fq 'pg_restore' "$MOCK_ROOT/calls"
  echo "PASS $failure failure rolls back the compatible application"
done

setup incompatible previous
export FAIL=incompatible
fails run_deploy
old_current
fails old_restarted
[[ $(find "$MOCK_ROOT/backups" -type f | wc -l) == 1 ]]
echo 'PASS incompatible schema leaves processes stopped and retains backup'

setup first-failure
export FAIL=readiness
fails run_deploy
[[ ! -e "$MOCK_ROOT/current" ]]
fails grep -Fq 'down' "$MOCK_ROOT/calls"
echo 'PASS first-release failure preserves database and has no previous release'

setup lock previous
(
  exec 8>"$MOCK_ROOT/.deploy.lock"
  flock -n 8
  fails run_deploy
)
[[ ! -s "$MOCK_ROOT/calls" ]]
echo 'PASS concurrent deployment rejected before touching Docker'

setup github
printf '%s\n%s\n%s\n' 'github-actions[bot]' 'fixture-token-sentinel' "$NEW" | \
  bash "$MOCK_ROOT/releases/new/deploy.sh" --github >"$MOCK_ROOT/output" 2>&1
[[ ! -e $(cat "$MOCK_ROOT/login-directory") ]]
fails grep -Fq 'fixture-token-sentinel' "$MOCK_ROOT/output" "$MOCK_ROOT/calls"
echo 'PASS temporary registry credentials removed without logging the token'

# Verify the runner transport without opening a real SSH connection.
cat >"$test_dir/bin/ssh" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$MOCK_ROOT/ssh-calls"
for ((i=1; i<=$#; i++)); do
  if [[ ${!i} == -i ]]; then
    ((i+=1))
    printf '%s\n' "${!i}" >"$MOCK_ROOT/key-path"
    [[ $(stat -c %a "${!i}") == 600 ]]
  fi
done
if [[ ${!#} == *'tar -xzf'* ]]; then
  tar -tzf - >"$MOCK_ROOT/archive-files"
else
  cat >"$MOCK_ROOT/ssh-stdin"
fi
MOCK
chmod +x "$test_dir/bin/ssh"
setup transport
export VPS_HOST=example.test VPS_USER=deployer VPS_PORT=2222
export GITHUB_SHA=0123456789012345678901234567890123456789
export GITHUB_RUN_ID=123 GITHUB_RUN_ATTEMPT=1 GITHUB_ACTOR=example
export VPS_SSH_KEY=fixture-private-key VPS_KNOWN_HOSTS=fixture-known-host
export REGISTRY_TOKEN=fixture-registry-token IMAGE="$NEW"
transport() { (cd /src && bash scripts/deploy_vps.sh); }
transport
grep -Fxq 'compose.yml' "$MOCK_ROOT/archive-files"
grep -Fxq 'deploy.sh' "$MOCK_ROOT/archive-files"
[[ $(wc -l <"$MOCK_ROOT/archive-files") == 2 ]]
grep -Fq 'StrictHostKeyChecking=yes' "$MOCK_ROOT/ssh-calls"
grep -Fq 'UserKnownHostsFile=' "$MOCK_ROOT/ssh-calls"
grep -Fxq "$REGISTRY_TOKEN" "$MOCK_ROOT/ssh-stdin"
grep -Fxq "$IMAGE" "$MOCK_ROOT/ssh-stdin"
fails grep -Fq "$REGISTRY_TOKEN" "$MOCK_ROOT/ssh-calls"
fails grep -Fq "$VPS_SSH_KEY" "$MOCK_ROOT/ssh-calls"
[[ ! -e $(cat "$MOCK_ROOT/key-path") ]]
echo 'PASS SSH transport pins host key and sends registry credentials only over stdin'

: >"$MOCK_ROOT/ssh-calls"
export VPS_HOST='example.test; invalid'
fails transport
[[ ! -s "$MOCK_ROOT/ssh-calls" ]]
echo 'PASS invalid SSH configuration rejected before connecting'
