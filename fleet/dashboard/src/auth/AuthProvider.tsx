import { createContext } from "preact";
import { useContext, useState, useCallback } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { setAuthToken } from "../api";

interface AuthUser {
  name: string;
  role: "admin" | "viewer";
  token: string;
}

interface AuthContextValue {
  user: AuthUser | null;
  login: (token: string) => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthContextValue>({
  user: null,
  login: async () => {},
  logout: () => {},
});

export function useAuth() {
  return useContext(AuthContext);
}

interface Props {
  children: ComponentChildren;
}

export function AuthProvider({ children }: Props) {
  const [user, setUser] = useState<AuthUser | null>(() => {
    const stored = localStorage.getItem("sigil_fleet_token");
    if (stored) {
      try {
        const parsed = JSON.parse(stored);
        setAuthToken(parsed.token);
        return parsed;
      } catch {
        return null;
      }
    }
    return null;
  });

  const login = useCallback(async (token: string) => {
    setAuthToken(token);
    // In a real OIDC flow, we'd decode the JWT to get user info.
    // For now, store the token and assume viewer role.
    const authUser: AuthUser = {
      name: "User",
      role: "viewer",
      token,
    };
    setUser(authUser);
    localStorage.setItem("sigil_fleet_token", JSON.stringify(authUser));
  }, []);

  const logout = useCallback(() => {
    setUser(null);
    setAuthToken("");
    localStorage.removeItem("sigil_fleet_token");
  }, []);

  return (
    <AuthContext.Provider value={{ user, login, logout }}>
      {children}
    </AuthContext.Provider>
  );
}
