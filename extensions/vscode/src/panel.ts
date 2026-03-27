import * as vscode from "vscode";
import * as path from "path";
import { SigilClient, Suggestion } from "./client";

export class SigilPanelProvider implements vscode.WebviewViewProvider {
  public static readonly viewType = "sigil.panel";

  private view?: vscode.WebviewView;
  private unreadCount = 0;

  constructor(
    private readonly extensionUri: vscode.Uri,
    private readonly client: SigilClient,
  ) {}

  resolveWebviewView(
    webviewView: vscode.WebviewView,
    _context: vscode.WebviewViewResolveContext,
    _token: vscode.CancellationToken,
  ): void {
    this.view = webviewView;

    webviewView.webview.options = {
      enableScripts: true,
      localResourceRoots: [
        vscode.Uri.joinPath(this.extensionUri, "media"),
      ],
    };

    webviewView.webview.html = this.getHtml(webviewView.webview);

    // Bridge: webview → host messages.
    webviewView.webview.onDidReceiveMessage(async (msg) => {
      switch (msg.type) {
        case "rpc": {
          await this.handleRpc(msg.method, msg.payload);
          break;
        }
        case "setting": {
          const config = vscode.workspace.getConfiguration("sigil");
          await config.update(
            msg.key,
            msg.value,
            vscode.ConfigurationTarget.Global,
          );
          break;
        }
        case "ready": {
          // Send initial data once webview is ready.
          await this.sendInitialData();
          break;
        }
      }
    });

    // Reset badge when panel becomes visible.
    webviewView.onDidChangeVisibility(() => {
      if (webviewView.visible) {
        this.unreadCount = 0;
        this.updateBadge();
      }
    });

    // Pass saved config to the webview once ready.
    this.view.webview.options = {
      ...this.view.webview.options,
      enableScripts: true,
    };
  }

  /** Forward a real-time suggestion push event to the webview. */
  pushSuggestion(sg: Suggestion): void {
    if (this.view) {
      this.view.webview.postMessage({ type: "suggestion", data: sg });
      if (!this.view.visible) {
        this.unreadCount++;
        this.updateBadge();
      }
    }
  }

  private updateBadge(): void {
    if (this.view) {
      this.view.badge = this.unreadCount > 0
        ? { tooltip: `${this.unreadCount} new suggestion(s)`, value: this.unreadCount }
        : undefined;
    }
  }

  private async sendInitialData(): Promise<void> {
    // Send settings.
    const config = vscode.workspace.getConfiguration("sigil");
    const displayMode = config.get<string>("suggestionDisplayMode", "timeline");
    const refreshInterval = config.get<number>("metricsRefreshInterval", 10000);
    this.view?.webview.postMessage({
      type: "settings",
      data: { displayMode, refreshInterval },
    });

    // Load initial suggestions.
    const suggestions = await this.client.suggestions();
    this.view?.webview.postMessage({
      type: "suggestions",
      data: suggestions,
    });
  }

  private async handleRpc(
    method: string,
    payload?: Record<string, unknown>,
  ): Promise<void> {
    switch (method) {
      case "status": {
        const data = await this.client.status();
        this.view?.webview.postMessage({ type: "status", data });
        break;
      }
      case "metrics": {
        const data = await this.client.metrics();
        this.view?.webview.postMessage({ type: "metrics", data });
        break;
      }
      case "events": {
        const data = await this.client.events(payload as Record<string, unknown>);
        this.view?.webview.postMessage({ type: "events", data });
        break;
      }
      case "event-detail": {
        const id = payload?.id as number;
        const data = await this.client.eventDetail(id);
        this.view?.webview.postMessage({ type: "event-detail", data });
        break;
      }
      case "purge-events": {
        const deleted = await this.client.purgeEvents(
          payload as { ids: number[] },
        );
        this.view?.webview.postMessage({
          type: "purge-result",
          data: { deleted },
        });
        break;
      }
      case "feedback": {
        const sgId = payload?.suggestion_id as number;
        const outcome = payload?.outcome as string;
        await this.client.feedback(sgId, outcome);
        break;
      }
      case "suggestions": {
        const data = await this.client.suggestions();
        this.view?.webview.postMessage({ type: "suggestions", data });
        break;
      }
    }
  }

  private getHtml(webview: vscode.Webview): string {
    const mediaPath = vscode.Uri.joinPath(this.extensionUri, "media");
    const cssUri = webview.asWebviewUri(
      vscode.Uri.joinPath(mediaPath, "panel.css"),
    );
    const jsUri = webview.asWebviewUri(
      vscode.Uri.joinPath(mediaPath, "panel.js"),
    );

    const nonce = getNonce();

    return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <meta http-equiv="Content-Security-Policy"
    content="default-src 'none'; style-src ${webview.cspSource} 'unsafe-inline'; script-src 'nonce-${nonce}';">
  <link rel="stylesheet" href="${cssUri}">
  <title>Sigil</title>
</head>
<body>
  <nav class="tab-bar">
    <button class="tab active" data-tab="suggestions">Suggestions</button>
    <button class="tab" data-tab="status">Status</button>
    <button class="tab" data-tab="events">Events</button>
  </nav>

  <div id="suggestions-tab" class="tab-content active">
    <div class="toolbar">
      <div class="mode-toggle">
        <button class="mode-btn active" data-mode="timeline">Timeline</button>
        <button class="mode-btn" data-mode="active-list">Active</button>
      </div>
    </div>
    <div id="suggestions-list" class="list"></div>
    <div id="suggestions-empty" class="empty-state">No suggestions yet</div>
  </div>

  <div id="status-tab" class="tab-content">
    <div id="connection-status" class="status-header">
      <span class="dot disconnected"></span>
      <span>Disconnected</span>
    </div>
    <div id="daemon-info" class="info-grid"></div>
    <h3>Resources</h3>
    <div id="resources-info" class="info-grid"></div>
  </div>

  <div id="events-tab" class="tab-content">
    <div class="filter-bar">
      <select id="source-filter">
        <option value="">All Sources</option>
        <option value="file">File</option>
        <option value="process">Process</option>
        <option value="git">Git</option>
        <option value="terminal">Terminal</option>
        <option value="hyprland">Hyprland</option>
      </select>
      <div class="pagination-info" id="pagination-info"></div>
    </div>
    <div id="events-table-container">
      <table id="events-table">
        <thead>
          <tr>
            <th class="col-check"><input type="checkbox" id="select-all"></th>
            <th class="col-time">Time</th>
            <th class="col-source">Source</th>
            <th class="col-summary">Summary</th>
          </tr>
        </thead>
        <tbody id="events-body"></tbody>
      </table>
    </div>
    <div class="events-actions">
      <div class="pagination">
        <button id="prev-page" disabled>&laquo; Prev</button>
        <span id="page-info">Page 1</span>
        <button id="next-page" disabled>Next &raquo;</button>
      </div>
      <div class="purge-actions">
        <button id="purge-selected" class="danger" disabled>Purge Selected</button>
        <button id="purge-matching" class="danger">Purge All Matching</button>
      </div>
    </div>
    <div id="events-empty" class="empty-state">No events</div>
  </div>

  <div id="confirm-modal" class="modal hidden">
    <div class="modal-content">
      <p id="confirm-message"></p>
      <div class="modal-actions">
        <button id="confirm-yes" class="danger">Delete</button>
        <button id="confirm-no">Cancel</button>
      </div>
    </div>
  </div>

  <script nonce="${nonce}" src="${jsUri}"></script>
</body>
</html>`;
  }
}

function getNonce(): string {
  let text = "";
  const chars =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  for (let i = 0; i < 32; i++) {
    text += chars.charAt(Math.floor(Math.random() * chars.length));
  }
  return text;
}
