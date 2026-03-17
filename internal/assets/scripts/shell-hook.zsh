#!/usr/bin/env zsh
# sigil shell hook — source this from ~/.zshrc
# Added automatically by: sigild init
#
# Sends each executed command to sigild via Unix socket (non-blocking).
# Adds < 1ms latency to every prompt redraw.

_SIGILD_SESSION_ID="$$"

_sigild_precmd() {
    local _sigild_exit=$?
    local _sigild_cmd
    _sigild_cmd="$(fc -ln -1 2>/dev/null)"
    _sigild_cmd="${_sigild_cmd##[[:space:]]}"
    [[ -z "$_sigild_cmd" ]] && return 0

    local _sigild_sock="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/sigild.sock"
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
        | nc -U -w0 "$_sigild_sock" 2>/dev/null &
}

# Guard against double-registration
if (( ${precmd_functions[(I)_sigild_precmd]} == 0 )); then
    precmd_functions+=(_sigild_precmd)
fi
