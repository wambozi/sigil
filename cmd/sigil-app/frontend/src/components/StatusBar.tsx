interface StatusBarProps {
  connected: boolean;
  currentTask: any;
}

export function StatusBar({ connected, currentTask }: StatusBarProps) {
  const taskLabel = currentTask
    ? `${currentTask.description || currentTask.branch || "Working..."}`
    : null;

  return (
    <div class="status-bar">
      <span
        class={`status-dot ${connected ? "connected" : "disconnected"}`}
      />
      <span class="status-text">
        {connected ? "Connected" : "Disconnected"}
      </span>
      {taskLabel && <span class="status-task">{taskLabel}</span>}
    </div>
  );
}
