const BASE_URL = import.meta.env.VITE_FLEET_API_URL || "http://localhost:8090";

let authToken = "";

export function setAuthToken(token: string) {
  authToken = token;
}

async function fetchAPI<T>(path: string, params?: Record<string, string>): Promise<T> {
  const url = new URL(path, BASE_URL);
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v) url.searchParams.set(k, v);
    }
  }

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (authToken) {
    headers["Authorization"] = `Bearer ${authToken}`;
  }

  const resp = await fetch(url.toString(), { headers });
  if (!resp.ok) {
    throw new Error(`API error: ${resp.status} ${resp.statusText}`);
  }
  return resp.json();
}

export interface AdoptionData {
  view: string;
  data: Array<{ date: string; tier: number; count: number }>;
}

export interface VelocityData {
  view: string;
  data: Array<{ tier: number; avg_build_rate: number; avg_events: number }>;
  disclaimer: string;
}

export interface CostData {
  view: string;
  data: Array<{
    date: string;
    local_ratio: number;
    total_events: number;
    cloud_queries: number;
    estimated_cost: number;
  }>;
}

export interface ComplianceData {
  view: string;
  total_nodes: number;
  local_pct: number;
  cloud_pct: number;
  all_approved: boolean;
  data_residency: string;
  org_name: string;
  date_range_from: string;
  date_range_to: string;
}

export interface TasksData {
  view: string;
  data: Array<{
    date: string;
    avg_completed: number;
    avg_started: number;
    avg_duration: number;
    stuck_rate: number;
    avg_speed: number;
  }>;
}

export interface QualityData {
  view: string;
  data: Array<{
    date: string;
    avg_quality: number;
    total_degradations: number;
  }>;
}

export interface MLData {
  view: string;
  data: Array<{
    date: string;
    ml_nodes: number;
    total_nodes: number;
    ml_speed: number | null;
    non_ml_speed: number | null;
    total_predictions: number;
    total_retrains: number;
  }>;
}

export interface OverviewData {
  view: string;
  data: Array<{
    date: string;
    node_count: number;
    avg_accept_rate: number;
    avg_tier: number;
    avg_routing_ratio: number;
    avg_build_rate: number;
    total_events: number;
  }>;
}

export function fetchAdoption(orgId?: string, from?: string, to?: string) {
  return fetchAPI<AdoptionData>("/api/v1/metrics", { view: "adoption", org_id: orgId || "", from: from || "", to: to || "" });
}

export function fetchVelocity(orgId?: string, from?: string, to?: string) {
  return fetchAPI<VelocityData>("/api/v1/metrics", { view: "velocity", org_id: orgId || "", from: from || "", to: to || "" });
}

export function fetchCost(orgId?: string, from?: string, to?: string) {
  return fetchAPI<CostData>("/api/v1/metrics", { view: "cost", org_id: orgId || "", from: from || "", to: to || "" });
}

export function fetchCompliance(orgId?: string, from?: string, to?: string) {
  return fetchAPI<ComplianceData>("/api/v1/metrics", { view: "compliance", org_id: orgId || "", from: from || "", to: to || "" });
}

export function fetchTasks(orgId?: string, from?: string, to?: string) {
  return fetchAPI<TasksData>("/api/v1/metrics", { view: "tasks", org_id: orgId || "", from: from || "", to: to || "" });
}

export function fetchQuality(orgId?: string, from?: string, to?: string) {
  return fetchAPI<QualityData>("/api/v1/metrics", { view: "quality", org_id: orgId || "", from: from || "", to: to || "" });
}

export function fetchML(orgId?: string, from?: string, to?: string) {
  return fetchAPI<MLData>("/api/v1/metrics", { view: "ml", org_id: orgId || "", from: from || "", to: to || "" });
}

export function fetchOverview(orgId?: string, from?: string, to?: string) {
  return fetchAPI<OverviewData>("/api/v1/metrics", { view: "", org_id: orgId || "", from: from || "", to: to || "" });
}
