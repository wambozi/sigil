import { useEffect, useState } from "preact/hooks";
import { fetchCompliance, type ComplianceData } from "../api";

export function ComplianceView() {
  const [data, setData] = useState<ComplianceData | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    fetchCompliance()
      .then(setData)
      .catch((e: Error) => setError(e.message));
  }, []);

  function exportAuditSummary() {
    if (!data) return;

    const summary = [
      `Sigil Fleet Compliance Audit — ${data.date_range_from} to ${data.date_range_to}`,
      `Organization: ${data.org_name}`,
      `Total nodes reporting: ${data.total_nodes}`,
      `AI inference routing: ${data.local_pct.toFixed(1)}% on-device, ${data.cloud_pct.toFixed(1)}% cloud`,
      `All cloud queries routed through approved endpoints: ${data.all_approved ? "yes" : "no"}`,
      `Raw data residency: ${data.data_residency}`,
    ].join("\n");

    const blob = new Blob([summary], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `sigil-compliance-audit-${data.date_range_from}-${data.date_range_to}.txt`;
    a.click();
    URL.revokeObjectURL(url);
  }

  if (error) return <div class="error">Error: {error}</div>;
  if (!data) return <div>Loading compliance data...</div>;

  return (
    <div class="view">
      <h2>Compliance and Security Posture</h2>

      <div class="panel">
        <h3>Data Residency</h3>
        <p>{data.data_residency}</p>
      </div>

      <div class="panel">
        <h3>Routing Compliance</h3>
        <p>On-device: {data.local_pct.toFixed(1)}%</p>
        <p>Cloud: {data.cloud_pct.toFixed(1)}%</p>
        <p>
          All queries through approved endpoints:{" "}
          <strong>{data.all_approved ? "Yes" : "No"}</strong>
        </p>
      </div>

      <div class="panel">
        <h3>Fleet Summary</h3>
        <p>Organization: {data.org_name}</p>
        <p>Total nodes reporting: {data.total_nodes}</p>
        <p>Date range: {data.date_range_from} to {data.date_range_to}</p>
      </div>

      <button onClick={exportAuditSummary}>Export Audit Summary</button>
    </div>
  );
}
