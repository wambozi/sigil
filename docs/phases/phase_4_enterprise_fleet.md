# Phase 4 — Enterprise Fleet Layer: Exit Criteria

## Status

- [ ] Fleet layer works end-to-end with at least 3 simulated engineer nodes sending data
      Verification: run 3 instances of sigild with fleet.enabled=true pointing at the fleet service.
      Confirm daily_metrics table has rows from all 3 nodes.

- [ ] Dashboard shows meaningful adoption tier distribution across simulated nodes
      Verification: open fleet dashboard, confirm AdoptionView shows tier distribution chart
      with data from the 3 simulated nodes.

- [ ] AI cost metrics accurate: Cactus routing ratio matches daemon logs
      Verification: compare CostView dashboard numbers with sigilctl status output from each node.

- [ ] Engineer opt-in flow clear to a non-technical observer
      Verification: walk through sigild init fleet opt-in. Confirm data preview is shown.

- [ ] Insights view Fleet Reporting tab accurately previews all outbound data
      Verification: open shell Insights view, confirm "Team Insights" tab shows the same
      FleetReport JSON that the fleet service receives.

- [ ] Compliance view generates an auditor-ready export
      Verification: click "Export Audit Summary" in ComplianceView, confirm the downloaded
      text file contains accurate data residency and routing compliance information.

## Implementation Notes

See linked issues: #116, #117, #118, #119, #120, #121, #122, #123, #124, #125.
