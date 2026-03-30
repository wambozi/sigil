package dev.sigil.jetbrains

import com.intellij.openapi.util.Key

/**
 * Project-level user-data keys shared across Sigil plugin components.
 */
val SIGIL_CLIENT_KEY = Key.create<SigilClient>("sigil.client")
