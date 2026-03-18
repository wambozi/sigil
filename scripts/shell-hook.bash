#!/usr/bin/env bash
# sigil shell hook — source this from ~/.bashrc
# Added automatically by: sigild init
#
# Uses PROMPT_COMMAND to capture each executed command.
# Sends metadata to sigild via Unix socket (non-blocking).

_SIGILD_SESSION_ID="$$"

_sigild_prompt_cmd() {
    local _exit=$?
    local _cmd
    _cmd="$(HISTTIMEFORMAT='' history 1 | sed 's/^[[:space:]]*[0-9]*[[:space:]]*//')"
    [[ -z "$_cmd" ]] && return 0

    local _runtime="${XDG_RUNTIME_DIR:-${TMPDIR:-/run/user/$(id -u)}}"
    local _sock="${_runtime}/sigild.sock"
    [[ ! -S "$_sock" ]] && return 0

    local _cmd_json="${_cmd//\\/\\\\}"
    _cmd_json="${_cmd_json//\"/\\\"}"
    local _cwd_json="${PWD//\\/\\\\}"
    _cwd_json="${_cwd_json//\"/\\\"}"

    printf '{"method":"ingest","payload":{"cmd":"%s","exit_code":%d,"cwd":"%s","ts":%d,"session_id":"%s"}}\n' \
        "$_cmd_json" \
        "$_exit" \
        "$_cwd_json" \
        "$(date +%s)" \
        "$_SIGILD_SESSION_ID" \
        | nc -U -w0 "$_sock" 2>/dev/null &
}

# Prepend to PROMPT_COMMAND without clobbering existing entries
if [[ "$PROMPT_COMMAND" != *"_sigild_prompt_cmd"* ]]; then
    PROMPT_COMMAND="_sigild_prompt_cmd${PROMPT_COMMAND:+; $PROMPT_COMMAND}"
fi
