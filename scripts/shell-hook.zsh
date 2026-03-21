#!/usr/bin/env zsh
# sigil shell hook — source this from ~/.zshrc
# Added automatically by: sigild init
#
# Uses preexec to capture the command text and precmd to send it with the
# exit code.  This avoids the fc/history timing issues that cause missed
# commands on macOS with oh-my-zsh and SHARE_HISTORY.
# Adds < 1ms latency to every prompt redraw.

_SIGILD_SESSION_ID="$$"
_SIGILD_LAST_CMD=""

_sigild_preexec() {
    _SIGILD_LAST_CMD="$1"
}

_sigild_precmd() {
    local _sigild_exit=$?
    [[ -z "$_SIGILD_LAST_CMD" ]] && return 0
    local _sigild_cmd="$_SIGILD_LAST_CMD"
    _SIGILD_LAST_CMD=""

    local _sigild_runtime="${XDG_RUNTIME_DIR:-${TMPDIR:-/run/user/$(id -u)}}"
    local _sigild_sock="${_sigild_runtime}/sigild.sock"
    [[ ! -S "$_sigild_sock" ]] && return 0

    # Escape cmd and cwd for embedding in JSON (replace \ then ")
    local _sigild_cmd_json="${_sigild_cmd//\\/\\\\}"
    _sigild_cmd_json="${_sigild_cmd_json//\"/\\\"}"
    local _sigild_cwd_json="${PWD//\\/\\\\}"
    _sigild_cwd_json="${_sigild_cwd_json//\"/\\\"}"

    printf '{"method":"ingest","payload":{"cmd":"%s","exit_code":%d,"cwd":"%s","ts":%d,"session_id":"%s"}}\n' \
        "$_sigild_cmd_json" \
        "$_sigild_exit" \
        "$_sigild_cwd_json" \
        "$(date +%s)" \
        "$_SIGILD_SESSION_ID" \
        | nc -U -w1 "$_sigild_sock" >/dev/null 2>&1 &!
}

# Guard against double-registration
if (( ${preexec_functions[(I)_sigild_preexec]} == 0 )); then
    preexec_functions+=(_sigild_preexec)
fi
if (( ${precmd_functions[(I)_sigild_precmd]} == 0 )); then
    precmd_functions+=(_sigild_precmd)
fi
