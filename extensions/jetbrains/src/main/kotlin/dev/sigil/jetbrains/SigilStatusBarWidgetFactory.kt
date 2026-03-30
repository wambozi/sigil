package dev.sigil.jetbrains

import com.intellij.openapi.project.Project
import com.intellij.openapi.wm.StatusBar
import com.intellij.openapi.wm.StatusBarWidget
import com.intellij.openapi.wm.StatusBarWidgetFactory

/**
 * Factory that registers the Sigil connection status widget in the IDE status bar.
 */
class SigilStatusBarWidgetFactory : StatusBarWidgetFactory {
    override fun getId(): String = SigilStatusBarWidget.ID

    override fun getDisplayName(): String = "Sigil Connection Status"

    override fun isAvailable(project: Project): Boolean = true

    override fun createWidget(project: Project): StatusBarWidget {
        return SigilStatusBarWidget(project)
    }

    override fun disposeWidget(widget: StatusBarWidget) {
        // No-op; widget lifecycle is managed by the status bar.
    }

    override fun canBeEnabledOn(statusBar: StatusBar): Boolean = true
}
