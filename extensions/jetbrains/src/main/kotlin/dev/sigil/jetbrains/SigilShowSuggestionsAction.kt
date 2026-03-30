package dev.sigil.jetbrains

import com.google.gson.JsonArray
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.diagnostic.Logger
import com.intellij.openapi.ui.popup.JBPopupFactory
import javax.swing.DefaultListModel
import javax.swing.JList

/**
 * Action accessible via Find Action (Ctrl+Shift+A / Cmd+Shift+A) that
 * fetches suggestion history from sigild and displays it in a popup list.
 */
class SigilShowSuggestionsAction : AnAction() {
    private val log = Logger.getInstance(SigilShowSuggestionsAction::class.java)

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val client = project.getUserData(SIGIL_CLIENT_KEY)

        if (client == null || !client.connected) {
            JBPopupFactory.getInstance()
                .createMessage("Sigil: Not connected to daemon")
                .showInFocusCenter()
            return
        }

        // Fetch suggestions on a pooled thread to avoid blocking the EDT.
        ApplicationManager.getApplication().executeOnPooledThread {
            val resp = client.send("suggestions")

            ApplicationManager.getApplication().invokeLater {
                if (resp == null || resp.get("ok")?.asBoolean != true) {
                    val error = resp?.get("error")?.asString ?: "No suggestions available"
                    JBPopupFactory.getInstance()
                        .createMessage("Sigil: $error")
                        .showInFocusCenter()
                    return@invokeLater
                }

                val payload = resp.get("payload")
                val suggestions: JsonArray = when {
                    payload?.isJsonArray == true -> payload.asJsonArray
                    else -> {
                        JBPopupFactory.getInstance()
                            .createMessage("Sigil: No suggestions yet")
                            .showInFocusCenter()
                        return@invokeLater
                    }
                }

                if (suggestions.size() == 0) {
                    JBPopupFactory.getInstance()
                        .createMessage("Sigil: No suggestions yet")
                        .showInFocusCenter()
                    return@invokeLater
                }

                val model = DefaultListModel<String>()
                val suggestionList = mutableListOf<Triple<Int, String, String>>()

                for (element in suggestions) {
                    val sg = element.asJsonObject
                    val id = sg.get("id")?.asInt ?: continue
                    val title = sg.get("title")?.asString ?: "Untitled"
                    val status = sg.get("status")?.asString ?: "pending"
                    val category = sg.get("category")?.asString ?: ""
                    val body = sg.get("body")?.asString ?: ""

                    val icon = when (status) {
                        "accepted" -> "\u2713"
                        "dismissed" -> "\u2717"
                        "shown" -> "\u25C9"
                        else -> "\u25CB"
                    }
                    model.addElement("$icon $title [$status] $category")
                    suggestionList.add(Triple(id, title, body))
                }

                val list = JList(model)
                JBPopupFactory.getInstance()
                    .createListPopupBuilder(list)
                    .setTitle("Sigil Suggestions")
                    .setItemChosenCallback {
                        val index = list.selectedIndex
                        if (index >= 0 && index < suggestionList.size) {
                            val (sgId, sgTitle, sgBody) = suggestionList[index]
                            showSuggestionDetail(project, client, sgId, sgTitle, sgBody)
                        }
                    }
                    .createPopup()
                    .showInFocusCenter()
            }
        }
    }

    private fun showSuggestionDetail(
        project: com.intellij.openapi.project.Project,
        client: SigilClient,
        id: Int,
        title: String,
        body: String,
    ) {
        val handler = SigilNotificationHandler(project)
        val payload = com.google.gson.JsonObject().apply {
            addProperty("id", id)
            addProperty("title", title)
            addProperty("text", body)
        }
        handler.showSuggestion(payload)
    }
}
