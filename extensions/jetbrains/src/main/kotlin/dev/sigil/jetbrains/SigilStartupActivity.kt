package dev.sigil.jetbrains

import com.intellij.openapi.diagnostic.Logger
import com.intellij.openapi.project.Project
import com.intellij.openapi.startup.ProjectActivity
import com.intellij.openapi.util.Disposer

/**
 * Connects to sigild when a project is opened.
 *
 * Creates a [SigilClient], subscribes to the suggestions push topic,
 * and routes incoming suggestions to [SigilNotificationHandler].
 * Disconnects automatically when the project is closed.
 */
class SigilStartupActivity : ProjectActivity {
    private val log = Logger.getInstance(SigilStartupActivity::class.java)

    override suspend fun execute(project: Project) {
        val handler = SigilNotificationHandler(project)
        val statusWidget = SigilStatusBarWidget.getInstance(project)

        val client = SigilClient(
            onSuggestion = { payload -> handler.showSuggestion(payload) },
            onConnectionChange = { connected ->
                statusWidget?.updateState(connected)
            },
        )

        // Store client on project for use by actions.
        project.putUserData(SIGIL_CLIENT_KEY, client)

        log.info("Sigil: connecting to daemon for project '${project.name}'")
        client.connect()

        // Disconnect when the project is disposed.
        Disposer.register(project) {
            log.info("Sigil: disconnecting from daemon for project '${project.name}'")
            client.disconnect()
            project.putUserData(SIGIL_CLIENT_KEY, null)
        }
    }
}
