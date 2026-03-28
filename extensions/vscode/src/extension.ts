import * as vscode from "vscode";
import * as os from "os";
import * as path from "path";
import { SigilClient } from "./client";
import { SigilPanelProvider } from "./panel";

let client: SigilClient | undefined;
let statusBarItem: vscode.StatusBarItem;

function getSocketPath(): string {
  const configured = vscode.workspace
    .getConfiguration("sigil")
    .get<string>("socketPath");
  if (configured) {
    return configured;
  }
  const runtime =
    process.env.XDG_RUNTIME_DIR || `/run/user/${os.userInfo().uid}`;
  return path.join(runtime, "sigild.sock");
}

export function activate(context: vscode.ExtensionContext): void {
  // Status bar item.
  statusBarItem = vscode.window.createStatusBarItem(
    vscode.StatusBarAlignment.Right,
    100,
  );
  statusBarItem.text = "$(circle-slash) Sigil: Disconnected";
  statusBarItem.show();
  context.subscriptions.push(statusBarItem);

  const socketPath = getSocketPath();

  client = new SigilClient(socketPath);

  client.onConnectionChange = (connected: boolean) => {
    if (connected) {
      statusBarItem.text = "$(check) Sigil: Connected";
    } else {
      statusBarItem.text = "$(circle-slash) Sigil: Disconnected";
    }
  };

  // Register the Webview panel provider for the sidebar.
  const panelProvider = new SigilPanelProvider(context.extensionUri, client);
  context.subscriptions.push(
    vscode.window.registerWebviewViewProvider(
      SigilPanelProvider.viewType,
      panelProvider,
      { webviewOptions: { retainContextWhenHidden: true } },
    ),
  );

  // Forward suggestion pushes to the panel (not toasts).
  client.onSuggestion = (sg) => {
    panelProvider.pushSuggestion(sg);
  };

  client.connect();
  context.subscriptions.push({ dispose: () => client?.disconnect() });
}

export function deactivate(): void {
  client?.disconnect();
}
