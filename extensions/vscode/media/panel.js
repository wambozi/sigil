// @ts-check
// Sigil Panel — Webview-side JavaScript.
// Communicates with the extension host via postMessage.

(function () {
  // @ts-ignore
  const vscode = acquireVsCodeApi();

  // --- State ---
  let suggestions = [];
  let displayMode = "timeline";
  let refreshInterval = 10000;
  let refreshTimer = null;
  let eventsPage = null;
  let currentFilter = { limit: 100, offset: 0 };
  let selectedEventIds = new Set();
  let expandedEventId = null;
  let connected = false;

  // --- Tab navigation ---
  document.querySelectorAll(".tab").forEach((tab) => {
    tab.addEventListener("click", () => {
      document.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"));
      document.querySelectorAll(".tab-content").forEach((c) => c.classList.remove("active"));
      tab.classList.add("active");
      const target = tab.getAttribute("data-tab");
      document.getElementById(target + "-tab").classList.add("active");

      // Trigger data load when switching tabs.
      if (target === "status") {
        requestStatus();
        requestMetrics();
      } else if (target === "events") {
        requestEvents();
      }
    });
  });

  // --- Mode toggle ---
  document.querySelectorAll(".mode-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      document.querySelectorAll(".mode-btn").forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      displayMode = btn.getAttribute("data-mode");
      vscode.postMessage({ type: "setting", key: "suggestionDisplayMode", value: displayMode });
      renderSuggestions();
    });
  });

  // --- Suggestions ---
  function renderSuggestions() {
    const list = document.getElementById("suggestions-list");
    const empty = document.getElementById("suggestions-empty");

    let filtered = suggestions;
    if (displayMode === "active-list") {
      filtered = suggestions
        .filter((s) => s.status === "pending" || s.status === "shown")
        .sort((a, b) => (b.confidence || 0) - (a.confidence || 0));
    }

    if (filtered.length === 0) {
      list.innerHTML = "";
      empty.style.display = "block";
      return;
    }

    empty.style.display = "none";
    list.innerHTML = filtered.map((sg) => {
      const time = sg.created_at ? formatTime(sg.created_at) : "";
      const conf = sg.confidence ? Math.round(sg.confidence * 100) + "%" : "";
      const statusClass = sg.status === "accepted" ? "status-accepted" : sg.status === "dismissed" ? "status-dismissed" : "";
      const statusLabel = sg.status && sg.status !== "pending" && sg.status !== "shown"
        ? `<span class="suggestion-status ${statusClass}">${sg.status}</span>`
        : "";
      const body = sg.body || sg.text || "";
      const showActions = !sg.status || sg.status === "pending" || sg.status === "shown";

      return `
        <div class="suggestion-card" data-id="${sg.id}">
          <div class="suggestion-header" data-sg-id="${sg.id}">
            <span class="suggestion-title">${esc(sg.title)}</span>
            ${conf ? `<span class="confidence-badge">${conf}</span>` : ""}
            ${statusLabel}
            <span class="suggestion-time">${time}</span>
          </div>
          <div class="suggestion-body" id="sg-body-${sg.id}">
            <div class="suggestion-meta">
              ${sg.category ? `Category: ${esc(sg.category)}` : ""}
              ${sg.action_cmd ? ` &middot; Action: <code>${esc(sg.action_cmd)}</code>` : ""}
            </div>
            <div>${esc(body)}</div>
            ${showActions ? `
              <div class="suggestion-actions">
                <button class="btn-accept" data-feedback-id="${sg.id}" data-feedback-outcome="accepted">Accept</button>
                <button class="btn-dismiss" data-feedback-id="${sg.id}" data-feedback-outcome="dismissed">Dismiss</button>
              </div>
            ` : ""}
          </div>
        </div>`;
    }).join("");

    // Bind click handlers via event delegation (CSP blocks inline onclick).
    list.querySelectorAll(".suggestion-header").forEach((header) => {
      header.addEventListener("click", () => {
        const id = header.getAttribute("data-sg-id");
        const body = document.getElementById("sg-body-" + id);
        if (body) {
          body.classList.toggle("expanded");
        }
      });
    });

    list.querySelectorAll("[data-feedback-id]").forEach((btn) => {
      btn.addEventListener("click", (e) => {
        e.stopPropagation();
        const id = parseInt(btn.getAttribute("data-feedback-id"));
        const outcome = btn.getAttribute("data-feedback-outcome");
        vscode.postMessage({ type: "rpc", method: "feedback", payload: { suggestion_id: id, outcome } });
        // Optimistic update.
        const sg = suggestions.find((s) => s.id === id);
        if (sg) {
          sg.status = outcome;
          renderSuggestions();
        }
      });
    });
  }

  // --- Status ---
  function requestStatus() {
    vscode.postMessage({ type: "rpc", method: "status" });
  }

  function requestMetrics() {
    vscode.postMessage({ type: "rpc", method: "metrics" });
  }

  function renderStatus(data) {
    if (!data) return;
    connected = data.status === "ok";
    const header = document.getElementById("connection-status");
    header.innerHTML = `
      <span class="dot ${connected ? "connected" : "disconnected"}"></span>
      <span>${connected ? "Connected" : "Disconnected"} &mdash; v${esc(data.version || "?")}</span>
    `;

    const info = document.getElementById("daemon-info");
    info.innerHTML = `
      <span class="label">Uptime</span><span class="value">${formatDuration(data.uptime_seconds || 0)}</span>
      <span class="label">Notification Level</span><span class="value">${data.notifier_level ?? "?"}</span>
      <span class="label">Routing Mode</span><span class="value">${esc(data.routing_mode || "?")}</span>
      <span class="label">Analysis Interval</span><span class="value">${esc(data.analysis_interval || "?")}</span>
      <span class="label">Events Today</span><span class="value">${data.events_today ?? 0}</span>
      <span class="label">Active Sources</span><span class="value">${(data.active_sources || []).join(", ")}</span>
    `;
  }

  function renderMetrics(data) {
    if (!data) return;
    const info = document.getElementById("resources-info");
    const sigildMB = ((data.sigild_rss_bytes || 0) / 1048576).toFixed(1);
    let html = `
      <span class="label">sigild PID</span><span class="value">${data.sigild_pid || "?"}</span>
      <span class="label">sigild RSS</span><span class="value">${sigildMB} MB</span>
    `;

    const llama = data.llama_server;
    if (llama && llama.active) {
      const llamaMB = ((llama.rss_bytes || 0) / 1048576).toFixed(1);
      const cpu = (llama.cpu_pct || 0).toFixed(1);
      html += `
        <span class="label">llama-server PID</span><span class="value">${llama.pid || "?"} ${llama.managed ? "(managed)" : "(external)"}</span>
        <span class="label">llama-server RSS</span><span class="value">${llamaMB} MB</span>
        <span class="label">llama-server CPU</span><span class="value">${cpu}%</span>
        <span class="label">Model</span><span class="value">${esc(llama.model_name || "?")}</span>
        <span class="label">Context</span><span class="value">${llama.context_tokens_used || 0} / ${llama.context_tokens_max || 0} tokens</span>
      `;
    } else {
      html += `<span class="label">Local Model</span><span class="value">Not active</span>`;
    }
    info.innerHTML = html;
  }

  // --- Events ---
  function requestEvents() {
    const filter = { ...currentFilter };
    const sourceEl = document.getElementById("source-filter");
    if (sourceEl.value) {
      filter.source = sourceEl.value;
    }
    vscode.postMessage({ type: "rpc", method: "events", payload: filter });
  }

  document.getElementById("source-filter").addEventListener("change", () => {
    currentFilter.offset = 0;
    selectedEventIds.clear();
    requestEvents();
  });

  document.getElementById("prev-page").addEventListener("click", () => {
    if (currentFilter.offset >= currentFilter.limit) {
      currentFilter.offset -= currentFilter.limit;
      selectedEventIds.clear();
      requestEvents();
    }
  });

  document.getElementById("next-page").addEventListener("click", () => {
    if (eventsPage && currentFilter.offset + currentFilter.limit < eventsPage.total) {
      currentFilter.offset += currentFilter.limit;
      selectedEventIds.clear();
      requestEvents();
    }
  });

  document.getElementById("select-all").addEventListener("change", (e) => {
    const checked = e.target.checked;
    if (eventsPage) {
      if (checked) {
        eventsPage.events.forEach((ev) => selectedEventIds.add(ev.id));
      } else {
        selectedEventIds.clear();
      }
      renderEventCheckboxes();
      updatePurgeButton();
    }
  });

  document.getElementById("purge-selected").addEventListener("click", () => {
    if (selectedEventIds.size === 0) return;
    showConfirm(`Delete ${selectedEventIds.size} selected event(s)? This cannot be undone.`, () => {
      vscode.postMessage({
        type: "rpc",
        method: "purge-events",
        payload: { ids: Array.from(selectedEventIds) },
      });
    });
  });

  document.getElementById("purge-matching").addEventListener("click", () => {
    const total = eventsPage ? eventsPage.total : 0;
    if (total === 0) return;
    const sourceEl = document.getElementById("source-filter");
    const label = sourceEl.value ? `all ${sourceEl.value} events` : "ALL events";
    showConfirm(`Delete ${total} ${label}? This cannot be undone.`, () => {
      const filter = {};
      if (sourceEl.value) {
        filter.source = sourceEl.value;
      }
      vscode.postMessage({ type: "rpc", method: "purge-events", payload: filter });
    });
  });

  function renderEvents(data) {
    eventsPage = data;
    const body = document.getElementById("events-body");
    const empty = document.getElementById("events-empty");
    const container = document.getElementById("events-table-container");
    const actions = document.querySelector(".events-actions");

    if (!data || !data.events || data.events.length === 0) {
      body.innerHTML = "";
      empty.style.display = "block";
      container.style.display = "none";
      actions.style.display = "none";
      return;
    }

    empty.style.display = "none";
    container.style.display = "block";
    actions.style.display = "flex";

    body.innerHTML = data.events.map((ev) => `
      <tr data-id="${ev.id}">
        <td class="col-check"><input type="checkbox" class="event-check" data-id="${ev.id}" ${selectedEventIds.has(ev.id) ? "checked" : ""}></td>
        <td class="col-time">${formatTime(ev.timestamp)}</td>
        <td class="col-source">${esc(ev.source)}</td>
        <td class="col-summary">${esc(ev.summary)}</td>
      </tr>
    `).join("");

    // Click to expand detail.
    body.querySelectorAll("tr").forEach((row) => {
      row.addEventListener("click", (e) => {
        if (e.target.type === "checkbox") return;
        const id = parseInt(row.getAttribute("data-id"));
        toggleEventDetail(id, row);
      });
    });

    // Checkbox handling.
    body.querySelectorAll(".event-check").forEach((cb) => {
      cb.addEventListener("change", (e) => {
        const id = parseInt(cb.getAttribute("data-id"));
        if (e.target.checked) {
          selectedEventIds.add(id);
        } else {
          selectedEventIds.delete(id);
        }
        updatePurgeButton();
      });
    });

    // Pagination.
    const page = Math.floor(data.offset / data.limit) + 1;
    const totalPages = Math.ceil(data.total / data.limit);
    document.getElementById("page-info").textContent = `Page ${page} of ${totalPages}`;
    document.getElementById("prev-page").disabled = data.offset === 0;
    document.getElementById("next-page").disabled = data.offset + data.limit >= data.total;
    document.getElementById("pagination-info").textContent = `${data.total} events`;

    updatePurgeButton();
  }

  function renderEventCheckboxes() {
    document.querySelectorAll(".event-check").forEach((cb) => {
      const id = parseInt(cb.getAttribute("data-id"));
      cb.checked = selectedEventIds.has(id);
    });
  }

  function updatePurgeButton() {
    const btn = document.getElementById("purge-selected");
    btn.disabled = selectedEventIds.size === 0;
    btn.textContent = selectedEventIds.size > 0
      ? `Purge Selected (${selectedEventIds.size})`
      : "Purge Selected";
  }

  function toggleEventDetail(id, row) {
    // Remove existing detail row if any.
    const existing = row.nextElementSibling;
    if (existing && existing.classList.contains("event-detail-row")) {
      existing.remove();
      expandedEventId = null;
      return;
    }

    // Close any other expanded detail.
    document.querySelectorAll(".event-detail-row").forEach((r) => r.remove());

    expandedEventId = id;
    // Insert a loading row.
    const detailRow = document.createElement("tr");
    detailRow.classList.add("event-detail-row");
    detailRow.innerHTML = `<td colspan="4">Loading...</td>`;
    row.after(detailRow);

    vscode.postMessage({ type: "rpc", method: "event-detail", payload: { id } });
  }

  function renderEventDetail(data) {
    if (!data) return;
    const detailRow = document.querySelector(".event-detail-row");
    if (detailRow) {
      detailRow.innerHTML = `<td colspan="4"><pre>${esc(JSON.stringify(data.payload, null, 2))}</pre></td>`;
    }
  }

  // --- Confirm modal ---
  let confirmCallback = null;

  function showConfirm(message, onConfirm) {
    document.getElementById("confirm-message").textContent = message;
    document.getElementById("confirm-modal").classList.remove("hidden");
    confirmCallback = onConfirm;
  }

  document.getElementById("confirm-yes").addEventListener("click", () => {
    document.getElementById("confirm-modal").classList.add("hidden");
    if (confirmCallback) {
      confirmCallback();
      confirmCallback = null;
    }
  });

  document.getElementById("confirm-no").addEventListener("click", () => {
    document.getElementById("confirm-modal").classList.add("hidden");
    confirmCallback = null;
  });

  // --- Message handler (host → webview) ---
  window.addEventListener("message", (event) => {
    const msg = event.data;
    switch (msg.type) {
      case "settings": {
        if (msg.data.displayMode) {
          displayMode = msg.data.displayMode;
          document.querySelectorAll(".mode-btn").forEach((btn) => {
            btn.classList.toggle("active", btn.getAttribute("data-mode") === displayMode);
          });
        }
        if (msg.data.refreshInterval) {
          refreshInterval = Math.max(2000, msg.data.refreshInterval);
        }
        startRefreshTimer();
        break;
      }
      case "suggestions": {
        suggestions = msg.data || [];
        renderSuggestions();
        break;
      }
      case "suggestion": {
        // Real-time push — prepend to list.
        if (msg.data) {
          // Normalize: push events use 'text', stored use 'body'.
          const sg = msg.data;
          if (!sg.body && sg.text) sg.body = sg.text;
          if (!sg.status) sg.status = "shown";
          suggestions.unshift(sg);
          renderSuggestions();
        }
        break;
      }
      case "status": {
        renderStatus(msg.data);
        break;
      }
      case "metrics": {
        renderMetrics(msg.data);
        break;
      }
      case "events": {
        renderEvents(msg.data);
        break;
      }
      case "event-detail": {
        renderEventDetail(msg.data);
        break;
      }
      case "purge-result": {
        // Refresh events after purge.
        selectedEventIds.clear();
        requestEvents();
        break;
      }
    }
  });

  // --- Auto-refresh for status tab ---
  function startRefreshTimer() {
    if (refreshTimer) clearInterval(refreshTimer);
    refreshTimer = setInterval(() => {
      const statusTab = document.getElementById("status-tab");
      if (statusTab && statusTab.classList.contains("active")) {
        requestStatus();
        requestMetrics();
      }
    }, refreshInterval);
  }

  // --- Helpers ---
  function esc(s) {
    if (!s) return "";
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function formatTime(ms) {
    const d = new Date(ms);
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }

  function formatDuration(seconds) {
    if (seconds < 60) return seconds + "s";
    if (seconds < 3600) return Math.floor(seconds / 60) + "m";
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    return h + "h " + m + "m";
  }

  // --- Init ---
  vscode.postMessage({ type: "ready" });
})();
