package dev.sigil.jetbrains

import com.google.gson.JsonObject
import com.intellij.notification.NotificationAction
import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.diagnostic.Logger
import com.intellij.openapi.project.Project

/**
 * Displays sigild suggestions as IDE notification balloons.
 *
 * Accept/Dismiss actions send feedback RPC back to the daemon. All UI
 * interactions are dispatched to the EDT via [ApplicationManager.invokeLater].
 */
class SigilNotificationHandler(private val project: Project) {
    private val log = Logger.getInstance(SigilNotificationHandler::class.java)

    fun showSuggestion(payload: JsonObject) {
        val id = payload.get("id")?.asInt ?: return
        val title = payload.get("title")?.asString ?: "Sigil Suggestion"
        val text = payload.get("text")?.asString ?: payload.get("body")?.asString ?: ""
        val actionCmd = payload.get("action_cmd")?.asString

        val body = if (actionCmd.isNullOrBlank()) text else "$text\n\nAction: $actionCmd"

        ApplicationManager.getApplication().invokeLater {
            val notificationGroup = NotificationGroupManager.getInstance()
                .getNotificationGroup("Sigil Suggestions")

            val notification = notificationGroup.createNotification(
                title,
                body,
                NotificationType.INFORMATION,
            )

            notification.addAction(NotificationAction.createSimple("Accept") {
                sendFeedback(id, "accepted")
                notification.expire()
            })

            notification.addAction(NotificationAction.createSimple("Dismiss") {
                sendFeedback(id, "dismissed")
                notification.expire()
            })

            notification.notify(project)
        }
    }

    private fun sendFeedback(suggestionId: Int, outcome: String) {
        ApplicationManager.getApplication().executeOnPooledThread {
            try {
                val client = project.getUserData(SIGIL_CLIENT_KEY) ?: return@executeOnPooledThread
                client.send("feedback", mapOf(
                    "suggestion_id" to suggestionId,
                    "outcome" to outcome,
                ))
            } catch (e: Exception) {
                log.warn("Sigil: failed to send feedback: ${e.message}")
            }
        }
    }
}
