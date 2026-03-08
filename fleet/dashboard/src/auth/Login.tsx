import { useState } from "preact/hooks";
import { useAuth } from "./AuthProvider";

export function Login() {
  const { login } = useAuth();
  const [token, setToken] = useState("");
  const [error, setError] = useState("");

  async function handleSubmit(e: Event) {
    e.preventDefault();
    try {
      await login(token);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login failed");
    }
  }

  return (
    <div class="login">
      <h2>Sigil Fleet Dashboard</h2>
      <p>Enter your access token or use SSO to sign in.</p>
      <form onSubmit={handleSubmit}>
        <input
          type="password"
          placeholder="Access token"
          value={token}
          onInput={(e) => setToken((e.target as HTMLInputElement).value)}
        />
        <button type="submit">Sign In</button>
      </form>
      {error && <p class="error">{error}</p>}
    </div>
  );
}
