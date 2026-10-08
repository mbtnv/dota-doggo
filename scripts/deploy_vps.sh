#!/usr/bin/env bash
# GitHub runner transport; all remote commands are fixed apart from validated IDs.
set -Eeuo pipefail
[[ "$VPS_HOST" =~ ^[a-zA-Z0-9.:_-]+$ && "$VPS_USER" =~ ^[a-z_][a-z0-9_-]*$ ]] || exit 1
[[ "$VPS_PORT" =~ ^[0-9]+$ && "$VPS_PORT" -ge 1 && "$VPS_PORT" -le 65535 ]] || exit 1
[[ "$GITHUB_RUN_ID" =~ ^[0-9]+$ && "$GITHUB_RUN_ATTEMPT" =~ ^[0-9]+$ ]] || exit 1
[[ "$GITHUB_SHA" =~ ^[a-f0-9]{40}$ ]] || exit 1
[[ -n "$VPS_SSH_KEY" && -n "$VPS_KNOWN_HOSTS" && -n "$REGISTRY_TOKEN" ]] || exit 1

task_dir=$(mktemp -d)
trap 'rm -rf -- "$task_dir"' EXIT
umask 077
printf '%s\n' "$VPS_SSH_KEY" >"$task_dir/key"
printf '%s\n' "$VPS_KNOWN_HOSTS" >"$task_dir/known_hosts"
unset VPS_SSH_KEY VPS_KNOWN_HOSTS
ssh_options=(-i "$task_dir/key" -p "$VPS_PORT" -o BatchMode=yes
  -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$task_dir/known_hosts"
  -o ConnectTimeout=10 -o ServerAliveInterval=15 -o ServerAliveCountMax=2)
destination="$VPS_USER@$VPS_HOST"
release="/opt/dota-doggo/releases/$GITHUB_SHA-$GITHUB_RUN_ID-$GITHUB_RUN_ATTEMPT"
tar -czf "$task_dir/release.tgz" -C deploy compose.yml deploy.sh
# The release path intentionally expands here; every variable component is validated above.
# shellcheck disable=SC2029
ssh "${ssh_options[@]}" "$destination" \
  "umask 077 && mkdir -p '$release' && tar -xzf - -C '$release'" <"$task_dir/release.tgz"
# shellcheck disable=SC2029
printf '%s\n%s\n%s\n' "$GITHUB_ACTOR" "$REGISTRY_TOKEN" "$IMAGE" | \
  ssh "${ssh_options[@]}" "$destination" "bash '$release/deploy.sh' --github"
