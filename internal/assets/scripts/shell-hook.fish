#!/usr/bin/env fish
# sigil shell hook — source this from ~/.config/fish/config.fish
# Added automatically by: sigild init
#
# Uses fish_postexec event to capture commands after execution.
# Sends metadata to sigild via Unix socket (non-blocking).
# Adds < 1ms latency to every prompt.

# Guard against double-registration
if functions -q _sigild_postexec
    return 0
end

set -g _SIGILD_SESSION_ID %self

function _sigild_postexec --on-event fish_postexec
    set -l _sigild_exit $status
    set -l _sigild_cmd $argv[1]

    # Skip empty commands
    test -z "$_sigild_cmd"; and return 0

    # Find socket
    set -l _sigild_runtime
    if set -q XDG_RUNTIME_DIR
        set _sigild_runtime $XDG_RUNTIME_DIR
    else if set -q TMPDIR
        set _sigild_runtime $TMPDIR
    else
        set _sigild_runtime "/run/user/"(id -u)
    end
    set -l _sigild_sock "$_sigild_runtime/sigild.sock"
    test -S "$_sigild_sock"; or return 0

    # Escape cmd and cwd for JSON embedding
    set -l _sigild_cmd_json (string replace -a '\\' '\\\\' -- "$_sigild_cmd" | string replace -a '"' '\\"')
    set -l _sigild_cwd_json (string replace -a '\\' '\\\\' -- "$PWD" | string replace -a '"' '\\"')

    # Send to sigild (non-blocking, fire-and-forget)
    printf '{"method":"ingest","payload":{"cmd":"%s","exit_code":%d,"cwd":"%s","ts":%d,"session_id":"%s"}}\n' \
        "$_sigild_cmd_json" \
        "$_sigild_exit" \
        "$_sigild_cwd_json" \
        (date +%s) \
        "$_SIGILD_SESSION_ID" \
        | nc -U -w1 "$_sigild_sock" >/dev/null 2>&1 &
end
