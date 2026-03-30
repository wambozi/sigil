const DAYS = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"];

interface DndState {
  enabled: boolean;
  start: string;
  end: string;
  days: string[];
}

export function DndSchedule({
  value,
  onChange,
}: {
  value: DndState;
  onChange: (v: DndState) => void;
}) {
  const toggle = (day: string) => {
    const days = value.days.includes(day)
      ? value.days.filter((d) => d !== day)
      : [...value.days, day];
    onChange({ ...value, days });
  };

  return (
    <div class="dnd-schedule">
      <div class="settings-row">
        <span class="settings-label">Do Not Disturb</span>
        <label class="toggle-row" style={{ justifyContent: "flex-end", width: "auto" }}>
          <input
            type="checkbox"
            checked={value.enabled}
            onChange={(e) =>
              onChange({ ...value, enabled: (e.target as HTMLInputElement).checked })
            }
            style={{ display: "none" }}
          />
          <div class={`toggle-switch`}>
            <div class={`toggle-track ${value.enabled ? "active" : ""}`}>
              <div class="toggle-thumb" />
            </div>
          </div>
        </label>
      </div>
      {value.enabled && (
        <>
          <div class="settings-row">
            <span class="settings-label">Start</span>
            <input
              class="settings-input"
              type="time"
              value={value.start}
              onInput={(e) =>
                onChange({ ...value, start: (e.target as HTMLInputElement).value })
              }
            />
          </div>
          <div class="settings-row">
            <span class="settings-label">End</span>
            <input
              class="settings-input"
              type="time"
              value={value.end}
              onInput={(e) =>
                onChange({ ...value, end: (e.target as HTMLInputElement).value })
              }
            />
          </div>
          <div class="settings-row">
            <span class="settings-label">Days</span>
            <div class="dnd-days">
              {DAYS.map((day) => (
                <button
                  key={day}
                  class={`dnd-day ${value.days.includes(day) ? "active" : ""}`}
                  onClick={() => toggle(day)}
                >
                  {day}
                </button>
              ))}
            </div>
          </div>
        </>
      )}
    </div>
  );
}
