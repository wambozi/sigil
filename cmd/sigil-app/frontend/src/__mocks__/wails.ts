import { vi } from 'vitest';

export const GetStatus = vi.fn().mockResolvedValue({});
export const GetSuggestions = vi.fn().mockResolvedValue([]);
export const AcceptSuggestion = vi.fn().mockResolvedValue(undefined);
export const DismissSuggestion = vi.fn().mockResolvedValue(undefined);
export const SetLevel = vi.fn().mockResolvedValue(undefined);
export const GetDaySummary = vi.fn().mockResolvedValue({});
export const Ask = vi.fn().mockResolvedValue({});
export const GetCurrentTask = vi.fn().mockResolvedValue(null);
export const IsConnected = vi.fn().mockResolvedValue(true);
export const GetConfig = vi.fn().mockResolvedValue({});
export const SetConfig = vi.fn().mockResolvedValue({});
export const GetPluginStatus = vi.fn().mockResolvedValue([]);
export const GetPluginRegistry = vi.fn().mockResolvedValue([]);
export const InstallPlugin = vi.fn().mockResolvedValue(undefined);
export const EnablePlugin = vi.fn().mockResolvedValue(undefined);
export const DisablePlugin = vi.fn().mockResolvedValue(undefined);
export const StopDaemon = vi.fn().mockResolvedValue(undefined);
export const StartDaemon = vi.fn().mockResolvedValue(undefined);
export const RestartDaemon = vi.fn().mockResolvedValue(undefined);

// Wails runtime mocks
export const EventsOn = vi.fn().mockReturnValue(() => {});
export const EventsEmit = vi.fn();
export const WindowShow = vi.fn();
export const WindowHide = vi.fn();
export const Quit = vi.fn();
