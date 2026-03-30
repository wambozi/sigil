import * as net from "net";

// --- Types matching the sigild socket protocol ---

export interface SigilRequest {
  method: string;
  payload?: Record<string, unknown>;
}

export interface SigilResponse {
  ok: boolean;
  payload?: unknown;
  error?: string;
}

export interface Suggestion {
  id: number;
  title: string;
  text: string;
  confidence: number;
  action_cmd?: string;
  category?: string;
  created_at?: number;
  status?: string;
}

export interface StoredSuggestion {
  id: number;
  category: string;
  confidence: number;
  title: string;
  body: string;
  action_cmd?: string;
  status: string;
  created_at: number;
}

export interface DaemonStatus {
  status: string;
  version: string;
  notifier_level: number;
  rss_mb: number;
  uptime_seconds: number;
  events_today: number;
  analysis_interval: string;
  routing_mode: string;
  active_sources: string[];
  current_keybinding_profile: string;
}

export interface LlamaServerMetrics {
  active: boolean;
  managed: boolean;
  pid?: number;
  rss_bytes?: number;
  cpu_pct?: number;
  model_name?: string;
  context_tokens_used?: number;
  context_tokens_max?: number;
}

export interface ResourceMetrics {
  sigild_pid: number;
  sigild_rss_bytes: number;
  llama_server: LlamaServerMetrics;
}

export interface EventSummary {
  id: number;
  kind: string;
  source: string;
  summary: string;
  timestamp: number;
  has_details: boolean;
}

export interface EventPage {
  events: EventSummary[];
  total: number;
  limit: number;
  offset: number;
}

export interface EventDetail {
  id: number;
  kind: string;
  source: string;
  payload: Record<string, unknown>;
  timestamp: number;
}

export interface EventFilter {
  source?: string;
  after?: number;
  before?: number;
  limit?: number;
  offset?: number;
}

// --- Socket client ---

export class SigilClient {
  private socket: net.Socket | null = null;
  private buffer = "";
  private reconnectDelay = 1000;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private disposed = false;

  public onSuggestion: ((sg: Suggestion) => void) | null = null;
  public onConnectionChange: ((connected: boolean) => void) | null = null;

  constructor(private socketPath: string) {}

  connect(): void {
    if (this.disposed) {
      return;
    }

    this.socket = net.createConnection(this.socketPath);

    this.socket.on("connect", () => {
      this.reconnectDelay = 1000;
      this.onConnectionChange?.(true);
      this.writeRaw({
        method: "subscribe",
        payload: { topic: "suggestions" },
      });
    });

    this.socket.on("data", (data: Buffer) => {
      this.buffer += data.toString();
      const lines = this.buffer.split("\n");
      this.buffer = lines.pop() || "";
      for (const line of lines) {
        if (line.trim()) {
          this.handleLine(line);
        }
      }
    });

    this.socket.on("error", () => {});

    this.socket.on("close", () => {
      this.onConnectionChange?.(false);
      this.scheduleReconnect();
    });
  }

  private handleLine(line: string): void {
    try {
      const msg = JSON.parse(line);
      if (typeof msg.event === "string" && msg.event === "suggestions") {
        this.onSuggestion?.(msg.payload as Suggestion);
      }
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
  send(
    method: string,
    payload?: Record<string, unknown>,
  ): Promise<SigilResponse> {
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

  // --- Typed RPC methods ---

  async status(): Promise<DaemonStatus | null> {
    try {
      const resp = await this.send("status");
      if (resp.ok) {
        return resp.payload as DaemonStatus;
      }
    } catch {
      // Daemon unavailable.
    }
    return null;
  }

  async metrics(): Promise<ResourceMetrics | null> {
    try {
      const resp = await this.send("metrics");
      if (resp.ok) {
        return resp.payload as ResourceMetrics;
      }
    } catch {
      // Daemon unavailable.
    }
    return null;
  }

  async events(filter?: EventFilter): Promise<EventPage | null> {
    try {
      const resp = await this.send(
        "events",
        filter as Record<string, unknown>,
      );
      if (resp.ok) {
        return resp.payload as EventPage;
      }
    } catch {
      // Daemon unavailable.
    }
    return null;
  }

  async eventDetail(id: number): Promise<EventDetail | null> {
    try {
      const resp = await this.send("event-detail", { id });
      if (resp.ok) {
        return resp.payload as EventDetail;
      }
    } catch {
      // Daemon unavailable.
    }
    return null;
  }

  async purgeEvents(
    params: { ids: number[] } | EventFilter,
  ): Promise<number> {
    try {
      const resp = await this.send(
        "purge-events",
        params as Record<string, unknown>,
      );
      if (resp.ok) {
        const p = resp.payload as { deleted: number };
        return p.deleted;
      }
    } catch {
      // Daemon unavailable.
    }
    return 0;
  }

  async suggestions(): Promise<StoredSuggestion[]> {
    try {
      const resp = await this.send("suggestions");
      if (resp.ok && Array.isArray(resp.payload)) {
        return resp.payload as StoredSuggestion[];
      }
    } catch {
      // Daemon unavailable.
    }
    return [];
  }

  async feedback(suggestionId: number, outcome: string): Promise<void> {
    try {
      await this.send("feedback", {
        suggestion_id: suggestionId,
        outcome,
      });
    } catch {
      // Non-fatal.
    }
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
