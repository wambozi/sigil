package dev.sigil.jetbrains

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.project.Project
import com.intellij.openapi.util.Key
import com.intellij.openapi.wm.StatusBar
import com.intellij.openapi.wm.StatusBarWidget
import com.intellij.openapi.wm.WindowManager
import com.intellij.util.Consumer
import java.awt.event.MouseEvent

/**
 * Status bar widget showing "Sigil: Connected" or "Sigil: Disconnected".
 *
 * Updated from the [SigilClient] connection state callback.
 */
class SigilStatusBarWidget(private val project: Project) : StatusBarWidget, StatusBarWidget.TextPresentation {

    private var statusBar: StatusBar? = null

    @Volatile
    private var connected = false

    override fun ID(): String = ID

    override fun install(statusBar: StatusBar) {
        this.statusBar = statusBar
        // Store reference on project so SigilStartupActivity can find us.
        project.putUserData(WIDGET_KEY, this)
    }

    override fun dispose() {
        project.putUserData(WIDGET_KEY, null)
    }

    override fun getPresentation(): StatusBarWidget.WidgetPresentation = this

    // --- TextPresentation ---

    override fun getText(): String {
        return if (connected) "Sigil: Connected" else "Sigil: Disconnected"
    }

    override fun getAlignment(): Float = com.intellij.util.ui.JBUI.CurrentTheme.StatusBar.Widget.arrowGap().toFloat()

    override fun getTooltipText(): String {
        return if (connected) {
            "Connected to sigild daemon"
        } else {
            "Not connected to sigild daemon — is sigild running?"
        }
    }

    override fun getClickConsumer(): Consumer<MouseEvent>? = null

    // --- state updates ---

    fun updateState(isConnected: Boolean) {
        connected = isConnected
        ApplicationManager.getApplication().invokeLater {
            statusBar?.updateWidget(ID)
        }
    }

    companion object {
        const val ID = "SigilStatusBar"
        private val WIDGET_KEY = Key.create<SigilStatusBarWidget>("sigil.statusBarWidget")

        /** Retrieve the widget instance for the given project, if installed. */
        fun getInstance(project: Project): SigilStatusBarWidget? {
            // Try from project user data first (set during install).
            project.getUserData(WIDGET_KEY)?.let { return it }

            // Fallback: look up via WindowManager.
            val statusBar = WindowManager.getInstance().getStatusBar(project) ?: return null
            val widget = statusBar.getWidget(ID) ?: return null
            return widget as? SigilStatusBarWidget
        }
    }
}
