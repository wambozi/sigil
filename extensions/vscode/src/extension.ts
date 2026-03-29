import * as vscode from "vscode";
import * as net from "net";
import * as os from "os";
import * as path from "path";

// --- Types matching the sigild socket protocol ---

interface SigilRequest {
  method: string;
  payload?: Record<string, unknown>;
}

interface SigilResponse {
  ok: boolean;
  payload?: unknown;
  error?: string;
}

interface SigilPushEvent {
  event: string;
  payload?: Record<string, unknown>;
}

interface Suggestion {
  id: number;
  title: string;
  text: string;
  confidence: number;
  action_cmd?: string;
}

interface StoredSuggestion {
  id: number;
  category: string;
  confidence: number;
  title: string;
  body: string;
  action_cmd?: string;
  status: string;
}

// --- Socket client ---

class SigilClient {
  private socket: net.Socket | null = null;
  private buffer = "";
  private reconnectDelay = 1000;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private disposed = false;

  private onSuggestion: (sg: Suggestion) => void;
  private onConnectionChange: (connected: boolean) => void;

  constructor(
    private socketPath: string,
    onSuggestion: (sg: Suggestion) => void,
    onConnectionChange: (connected: boolean) => void,
  ) {
    this.onSuggestion = onSuggestion;
    this.onConnectionChange = onConnectionChange;
  }

  connect(): void {
    if (this.disposed) {
      return;
    }

    this.socket = net.createConnection(this.socketPath);

    this.socket.on("connect", () => {
      this.reconnectDelay = 1000;
      this.onConnectionChange(true);
      // Subscribe to suggestions topic.
      this.writeRaw({
        method: "subscribe",
        payload: { topic: "suggestions" },
      });
    });

    this.socket.on("data", (data: Buffer) => {
      this.buffer += data.toString();
      const lines = this.buffer.split("\n");
      // Keep the last incomplete line in the buffer.
      this.buffer = lines.pop() || "";
      for (const line of lines) {
        if (line.trim()) {
          this.handleLine(line);
        }
      }
    });

    this.socket.on("error", () => {
      // Handled by 'close' event.
    });

    this.socket.on("close", () => {
      this.onConnectionChange(false);
      this.scheduleReconnect();
    });
  }

  private handleLine(line: string): void {
    try {
      const msg = JSON.parse(line);
      // Push events have an "event" field; responses have "ok".
      if (typeof msg.event === "string" && msg.event === "suggestions") {
        this.onSuggestion(msg.payload as Suggestion);
      }
      // We ignore subscription ack and other responses.
    } catch {
      // Ignore parse errors.
    }
  }

  private writeRaw(obj: SigilRequest): void {
    if (this.socket && !this.socket.destroyed) {
      this.socket.write(JSON.stringify(obj) + "\n");
    }
  }

  /** Send an RPC request on a new short-lived connection. */
  send(method: string, payload?: Record<string, unknown>): Promise<SigilResponse> {
    return new Promise((resolve, reject) => {
      const conn = net.createConnection(this.socketPath);
      let buf = "";

      conn.on("connect", () => {
        conn.write(JSON.stringify({ method, payload }) + "\n");
      });

      conn.on("data", (data: Buffer) => {
        buf += data.toString();
        const idx = buf.indexOf("\n");
        if (idx !== -1) {
          try {
            resolve(JSON.parse(buf.substring(0, idx)));
          } catch {
            reject(new Error("Invalid JSON response"));
          }
          conn.destroy();
        }
      });

      conn.on("error", (err: Error) => reject(err));

      conn.on("close", () => {
        if (!buf.includes("\n")) {
          reject(new Error("Connection closed before response"));
        }
      });
    });
  }

  private scheduleReconnect(): void {
    if (this.disposed) {
      return;
    }
    this.reconnectTimer = setTimeout(() => {
      this.connect();
    }, this.reconnectDelay);
    this.reconnectDelay = Math.min(this.reconnectDelay * 2, 30000);
  }

  disconnect(): void {
    this.disposed = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
    }
    if (this.socket) {
      this.socket.destroy();
      this.socket = null;
    }
  }
}

// --- Extension lifecycle ---

let client: SigilClient | undefined;
let statusBarItem: vscode.StatusBarItem;

function getSocketPath(): string {
  const configured = vscode.workspace
    .getConfiguration("sigil")
    .get<string>("socketPath");
  if (configured) {
    return configured;
  }
  if (process.platform === "win32") {
    const appData =
      process.env.LOCALAPPDATA ||
      path.join(os.homedir(), "AppData", "Local");
    return path.join(appData, "sigil", "sigild.sock");
  }
  const runtime =
    process.env.XDG_RUNTIME_DIR || `/run/user/${os.userInfo().uid}`;
  return path.join(runtime, "sigild.sock");
}

async function showSuggestionToast(sg: Suggestion): Promise<void> {
  const body = sg.action_cmd
    ? `${sg.text}\n\nAction: ${sg.action_cmd}`
    : sg.text;

  const action = await vscode.window.showInformationMessage(
    `${sg.title}: ${body}`,
    "Accept",
    "Dismiss",
  );

  if (!client) {
    return;
  }

  if (action === "Accept") {
    try {
      await client.send("feedback", {
        suggestion_id: sg.id,
        outcome: "accepted",
      });
    } catch {
      // Daemon may be unavailable; non-fatal.
    }
  } else if (action === "Dismiss") {
    try {
      await client.send("feedback", {
        suggestion_id: sg.id,
        outcome: "dismissed",
      });
    } catch {
      // Daemon may be unavailable; non-fatal.
    }
  }
}

export function activate(context: vscode.ExtensionContext): void {
  // Status bar item (P2).
  statusBarItem = vscode.window.createStatusBarItem(
    vscode.StatusBarAlignment.Right,
    100,
  );
  statusBarItem.text = "$(circle-slash) Sigil: Disconnected";
  statusBarItem.show();
  context.subscriptions.push(statusBarItem);

  const socketPath = getSocketPath();

  client = new SigilClient(
    socketPath,
    (sg: Suggestion) => {
      showSuggestionToast(sg);
    },
    (connected: boolean) => {
      if (connected) {
        statusBarItem.text = "$(check) Sigil: Connected";
      } else {
        statusBarItem.text = "$(circle-slash) Sigil: Disconnected";
      }
    },
  );

  client.connect();
  context.subscriptions.push({ dispose: () => client?.disconnect() });

  // Show Suggestions command (P3).
  const showCmd = vscode.commands.registerCommand(
    "sigil.showSuggestions",
    async () => {
      if (!client) {
        vscode.window.showErrorMessage("Sigil: Not connected to daemon");
        return;
      }

      let resp: SigilResponse;
      try {
        resp = await client.send("suggestions");
      } catch {
        vscode.window.showErrorMessage(
          "Sigil: Could not fetch suggestions from daemon",
        );
        return;
      }

      if (!resp.ok || !Array.isArray(resp.payload)) {
        vscode.window.showErrorMessage(
          `Sigil: ${resp.error || "No suggestions available"}`,
        );
        return;
      }

      const suggestions = resp.payload as StoredSuggestion[];
      if (suggestions.length === 0) {
        vscode.window.showInformationMessage("Sigil: No suggestions yet");
        return;
      }

      const items = suggestions.map((sg) => ({
        label: `$(${statusIcon(sg.status)}) ${sg.title}`,
        description: `[${sg.status}] ${sg.category}`,
        detail: sg.body,
        suggestion: sg,
      }));

      const picked = await vscode.window.showQuickPick(items, {
        placeHolder: "Select a suggestion to accept or dismiss",
        matchOnDetail: true,
      });

      if (!picked) {
        return;
      }

      const sg = picked.suggestion;
      if (sg.status === "accepted" || sg.status === "dismissed") {
        vscode.window.showInformationMessage(
          `Sigil: Already ${sg.status}: ${sg.title}`,
        );
        return;
      }

      const action = await vscode.window.showInformationMessage(
        `${sg.title}: ${sg.body}`,
        "Accept",
        "Dismiss",
      );

      if (action === "Accept" || action === "Dismiss") {
        try {
          await client.send("feedback", {
            suggestion_id: sg.id,
            outcome: action.toLowerCase(),
          });
          vscode.window.showInformationMessage(
            `Sigil: ${action}ed suggestion "${sg.title}"`,
          );
        } catch {
          vscode.window.showErrorMessage("Sigil: Failed to send feedback");
        }
      }
    },
  );
  context.subscriptions.push(showCmd);
}

function statusIcon(status: string): string {
  switch (status) {
    case "accepted":
      return "check";
    case "dismissed":
      return "x";
    case "shown":
      return "eye";
    default:
      return "circle-outline";
  }
}

export function deactivate(): void {
  client?.disconnect();
}
