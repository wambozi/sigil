declare module "../wailsjs/go/main/App" {
  export function GetStatus(): Promise<any>;
  export function GetSuggestions(): Promise<any[]>;
  export function AcceptSuggestion(id: number): Promise<void>;
  export function DismissSuggestion(id: number): Promise<void>;
  export function SetLevel(n: number): Promise<void>;
  export function GetDaySummary(): Promise<any>;
  export function Ask(query: string): Promise<any>;
  export function GetCurrentTask(): Promise<any>;
  export function IsConnected(): Promise<boolean>;
  export function GetConfig(): Promise<any>;
  export function SetConfig(config: any): Promise<any>;
  export function GetPluginStatus(): Promise<any[]>;
  export function GetPluginRegistry(): Promise<any[]>;
  export function InstallPlugin(name: string): Promise<void>;
  export function EnablePlugin(name: string): Promise<void>;
  export function DisablePlugin(name: string): Promise<void>;
  export function StopDaemon(): Promise<void>;
  export function StartDaemon(): Promise<void>;
  export function RestartDaemon(): Promise<void>;
}

declare module "../wailsjs/runtime/runtime" {
  export function EventsOn(
    eventName: string,
    callback: (...args: any[]) => void
  ): () => void;
  export function EventsEmit(eventName: string, ...args: any[]): void;
  export function WindowShow(): void;
  export function WindowHide(): void;
  export function Quit(): void;
}
