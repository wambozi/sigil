package dev.sigil.jetbrains

import com.google.gson.Gson
import com.google.gson.JsonObject
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.diagnostic.Logger
import java.io.BufferedReader
import java.io.InputStreamReader
import java.io.OutputStreamWriter
import java.net.StandardProtocolFamily
import java.net.UnixDomainSocketAddress
import java.nio.channels.Channels
import java.nio.channels.SocketChannel
import java.nio.file.Path

/**
 * Unix domain socket client for the sigild daemon.
 *
 * Maintains a persistent subscription connection for push events and
 * supports one-shot RPC on separate short-lived connections. Reconnects
 * automatically with exponential backoff (1 s to 30 s).
 */
class SigilClient(
    private val onSuggestion: (JsonObject) -> Unit,
    private val onConnectionChange: (Boolean) -> Unit,
) {
    private val log = Logger.getInstance(SigilClient::class.java)
    private val gson = Gson()

    @Volatile
    private var disposed = false

    @Volatile
    private var channel: SocketChannel? = null

    @Volatile
    var connected: Boolean = false
        private set

    private var reconnectDelay = INITIAL_RECONNECT_DELAY

    // --- public API ---

    /** Start the subscription connection on a pooled thread. */
    fun connect() {
        ApplicationManager.getApplication().executeOnPooledThread {
            doConnect()
        }
    }

    /** Send an RPC request on a new short-lived connection and return the response. */
    fun send(method: String, payload: Map<String, Any>? = null): JsonObject? {
        val socketPath = resolveSocketPath()
        val address = UnixDomainSocketAddress.of(socketPath)
        var ch: SocketChannel? = null
        try {
            ch = SocketChannel.open(StandardProtocolFamily.UNIX)
            ch.connect(address)

            val writer = OutputStreamWriter(Channels.newOutputStream(ch), Charsets.UTF_8)
            val request = JsonObject().apply {
                addProperty("method", method)
                if (payload != null) {
                    add("payload", gson.toJsonTree(payload))
                }
            }
            writer.write(gson.toJson(request) + "\n")
            writer.flush()

            val reader = BufferedReader(InputStreamReader(Channels.newInputStream(ch), Charsets.UTF_8))
            val line = reader.readLine() ?: return null
            return gson.fromJson(line, JsonObject::class.java)
        } catch (e: Exception) {
            log.warn("Sigil RPC '$method' failed: ${e.message}")
            return null
        } finally {
            try {
                ch?.close()
            } catch (_: Exception) {
            }
        }
    }

    /** Tear down the client permanently. */
    fun disconnect() {
        disposed = true
        try {
            channel?.close()
        } catch (_: Exception) {
        }
        channel = null
    }

    // --- internals ---

    private fun doConnect() {
        if (disposed) return

        val socketPath = resolveSocketPath()
        val address = UnixDomainSocketAddress.of(socketPath)

        try {
            val ch = SocketChannel.open(StandardProtocolFamily.UNIX)
            ch.connect(address)
            channel = ch
            reconnectDelay = INITIAL_RECONNECT_DELAY
            connected = true
            onConnectionChange(true)

            // Subscribe to suggestions topic.
            val writer = OutputStreamWriter(Channels.newOutputStream(ch), Charsets.UTF_8)
            val subscribeReq = JsonObject().apply {
                addProperty("method", "subscribe")
                add("payload", JsonObject().apply {
                    addProperty("topic", "suggestions")
                })
            }
            writer.write(gson.toJson(subscribeReq) + "\n")
            writer.flush()

            // Read loop — blocks until socket closes.
            val reader = BufferedReader(InputStreamReader(Channels.newInputStream(ch), Charsets.UTF_8))
            while (!disposed) {
                val line = reader.readLine() ?: break
                if (line.isBlank()) continue
                handleLine(line)
            }
        } catch (e: Exception) {
            log.info("Sigil socket connection lost: ${e.message}")
        } finally {
            connected = false
            onConnectionChange(false)
            scheduleReconnect()
        }
    }

    private fun handleLine(line: String) {
        try {
            val msg = gson.fromJson(line, JsonObject::class.java)
            // Push events have an "event" field; responses have "ok".
            val event = msg.get("event")?.asString
            if (event == "suggestions") {
                val payload = msg.getAsJsonObject("payload")
                if (payload != null) {
                    onSuggestion(payload)
                }
            }
            // Subscription acks and other responses are ignored.
        } catch (e: Exception) {
            log.debug("Sigil: failed to parse line: ${e.message}")
        }
    }

    private fun scheduleReconnect() {
        if (disposed) return
        val delay = reconnectDelay
        reconnectDelay = (reconnectDelay * 2).coerceAtMost(MAX_RECONNECT_DELAY)

        ApplicationManager.getApplication().executeOnPooledThread {
            try {
                Thread.sleep(delay)
            } catch (_: InterruptedException) {
                return@executeOnPooledThread
            }
            doConnect()
        }
    }

    companion object {
        private const val INITIAL_RECONNECT_DELAY = 1000L
        private const val MAX_RECONNECT_DELAY = 30_000L

        /** Resolve the sigild socket path using XDG conventions. */
        fun resolveSocketPath(): Path {
            val xdgRuntime = System.getenv("XDG_RUNTIME_DIR")
            return if (!xdgRuntime.isNullOrBlank()) {
                Path.of(xdgRuntime, "sigild.sock")
            } else {
                Path.of("/tmp/sigild.sock")
            }
        }
    }
}
