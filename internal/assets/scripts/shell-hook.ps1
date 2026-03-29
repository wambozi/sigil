# Sigil OS shell hook for PowerShell
# Installed by: sigild init
# Sends terminal events to sigild via Unix socket (AF_UNIX, Windows 10 1803+)

if ($env:_SIGILD_HOOK_ACTIVE) { return }
$env:_SIGILD_HOOK_ACTIVE = "1"

$_sigild_session_id = $PID
$_sigild_socket = "$env:LOCALAPPDATA\sigil\sigild.sock"

function _sigild_send($json) {
    try {
        $socket = [System.Net.Sockets.Socket]::new(
            [System.Net.Sockets.AddressFamily]::Unix,
            [System.Net.Sockets.SocketType]::Stream,
            [System.Net.Sockets.ProtocolType]::Unspecified
        )
        $endpoint = [System.Net.Sockets.UnixDomainSocketEndPoint]::new($_sigild_socket)
        $socket.Connect($endpoint)
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($json + "`n")
        $socket.Send($bytes) | Out-Null
        $socket.Close()
    } catch {
        # Silently ignore connection failures
    }
}

# Hook into PSReadLine for command capture
if (Get-Module PSReadLine) {
    Set-PSReadLineKeyHandler -Key Enter -ScriptBlock {
        $line = $null
        $cursor = $null
        [Microsoft.PowerShell.PSConsoleReadLine]::GetBufferState([ref]$line, [ref]$cursor)

        if ($line.Trim()) {
            $ts = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
            $escaped = $line -replace '\\', '\\\\' -replace '"', '\"'
            $json = '{"method":"ingest","payload":{"cmd":"' + $escaped + '","exit_code":0,"cwd":"' + ($PWD.Path -replace '\\', '\\\\' -replace '"', '\"') + '","ts":' + [math]::Floor($ts / 1000) + ',"session_id":"' + $_sigild_session_id + '"}}'
            _sigild_send $json
        }

        # Execute the original Enter behavior
        [Microsoft.PowerShell.PSConsoleReadLine]::AcceptLine()
    }
}
