import { useState } from "preact/hooks";
import { AdoptionView } from "./views/AdoptionView";
import { VelocityView } from "./views/VelocityView";
import { CostView } from "./views/CostView";
import { ComplianceView } from "./views/ComplianceView";
import { AuthProvider, useAuth } from "./auth/AuthProvider";
import { Login } from "./auth/Login";

type View = "adoption" | "velocity" | "cost" | "compliance";

function Dashboard() {
  const { user, logout } = useAuth();
  const [view, setView] = useState<View>("adoption");

  if (!user) {
    return <Login />;
  }

  return (
    <div>
      <header>
        <h1>Sigil Fleet Dashboard</h1>
        <nav>
          <button onClick={() => setView("adoption")} class={view === "adoption" ? "active" : ""}>
            Adoption
          </button>
          <button onClick={() => setView("velocity")} class={view === "velocity" ? "active" : ""}>
            Velocity
          </button>
          <button onClick={() => setView("cost")} class={view === "cost" ? "active" : ""}>
            Cost
          </button>
          <button onClick={() => setView("compliance")} class={view === "compliance" ? "active" : ""}>
            Compliance
          </button>
          <button onClick={logout}>Sign Out</button>
        </nav>
      </header>
      <main>
        {view === "adoption" && <AdoptionView />}
        {view === "velocity" && <VelocityView />}
        {view === "cost" && <CostView />}
        {view === "compliance" && <ComplianceView />}
      </main>
    </div>
  );
}

export function App() {
  return (
    <AuthProvider>
      <Dashboard />
    </AuthProvider>
  );
}
