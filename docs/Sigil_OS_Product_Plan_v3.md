# AETHER OS — Product Requirements & Execution Plan

## AI-Native, Self-Tuning Operating System for Software Engineers

**Document Classification:** Internal — Confidential  
**Author:** Product Management  
**Date:** February 28, 2026  
**Version:** 3.0  
**Status:** Draft for Executive Review  
**Changelog:**  
v3.0 — Cactus Compute partnership integrated into inference architecture; Enterprise Strategy section added; fleet aggregation layer and AI adoption tracking added to execution phases; timeline extended to 36 weeks.  
v2.0 — Unified Shell UI direction integrated; architecture, phases, and stack updated.

---

## 1. Executive Summary

Aether OS is a purpose-built, AI-native Linux operating system designed exclusively for professional software engineers and the engineering organizations they work within. Unlike consumer AI-OS attempts (Humane's Cosmos, Rabbit OS) that tried to abstract away complexity, Aether leans into it — delivering a unified single-pane shell where every developer tool lives inside one application frame, governed by a self-tuning AI daemon that adapts the entire environment to each developer's unique workflow patterns.

The user interface is the product's most distinctive design decision: **Aether presents the entire OS as a single full-screen shell** — a left-hand navigation rail for switching between embedded tools (editor, terminal, browser, git, containers, daemon insights), a main content pane, and a persistent unified input line at the bottom that seamlessly toggles between terminal commands and natural language AI prompts.

The core thesis: **developers don't need AI that replaces their work — they need a single cohesive environment that learns how they work and removes friction from everything around the code.** The self-tuning feedback loop — an OS-level daemon that observes workflow patterns, builds a local user model, and reshapes the environment — is the IP. The unified shell is the experience that makes that IP tangible.

**The enterprise thesis builds on top:** Aether gives engineering leadership fleet-wide visibility into AI adoption rates, developer velocity correlations, AI cost efficiency, and compliance posture — all derived from the same daemon that makes individual engineers more productive. The product that engineers love for personal productivity becomes the product that justifies the enterprise AI investment to the CTO.

A strategic partnership with **Cactus Compute** (YC-backed, open-source hybrid inference engine) replaces our previous Ollama + LiteLLM stack with a single runtime that intelligently routes AI queries between on-device inference and cloud models — solving both the constrained-hardware problem on our 2017 MacBook Pro test machine and the enterprise cost optimization story simultaneously.

We are building this with a single staff-level engineer (15 years FinTech, 6+ years Go), tested on a single 2017 MacBook Pro, with the explicit constraint that this must win over a telemetry-averse audience while delivering enterprise-grade observability to leadership.

---

## 2. Competitive Landscape Analysis

### 2.1 What Exists Today (and Why None of It Is This)

**Developer-Focused Linux Distributions.** The closest spiritual ancestors are custom Linux spins aimed at developers — Fedora Sway Spin, EndeavourOS with tiling WMs, Omarchy (a Hyprland-based distro). These give you a minimal, keyboard-driven environment with a terminal at the center. NixOS with Hyprland has emerged as a particularly popular combination in 2025–2026, with Hyprland now ranked the third most popular Linux environment in the Arch Linux survey. But none of these have an AI-native intelligence layer, and crucially, none unify the developer toolchain into a single application frame or offer any enterprise observability story.

**AI-Augmented Development Tools.** Cursor, Windsurf, Claude Code, GitHub Copilot — these live at the IDE/terminal layer. They're powerful for in-context code generation but operate as point solutions. They don't observe your broader workflow, don't tune your environment, and don't coordinate with your OS. Warp pushes AI into the terminal with a chat-like interface, and its "blocks" concept is a direct ancestor of our unified input bar — but Warp is a terminal application, not an operating environment. Enterprise adoption metrics for these tools are limited to license seat counts and vague "usage reports" — they can't tell leadership how AI is actually being integrated into engineering workflows.

**The Claude Desktop App.** Anthropic's native Claude app established an important UX pattern: a left-hand rail for conversation navigation, a main content pane, and a bottom input bar. Aether adopts this frame but replaces "conversations" with "tools" in the rail and replaces "chat" with a dual-mode shell/AI input line.

**Consumer AI-OS Attempts.** Humane's AI Pin (CosmOS) and Rabbit R1 went the opposite direction entirely — abstracting away from the developer toward a general consumer intent model. Both flopped spectacularly. Humane was acquired by HP for $116M (a fraction of its $230M raised) in February 2025, and the AI Pin was permanently bricked. The Rabbit R1 saw a 95% user abandonment rate within five months of launch. The critical lesson: they tried to replace existing tools instead of making existing tools better.

**Enterprise AI Operating Systems.** Red Hat's RHEL AI, Ubuntu AI, and Microsoft's Copilot+ PCs approach AI at the infrastructure or enterprise deployment layer. These are about running AI workloads, not about making the developer's daily experience smarter. OpenAI's emerging Apps SDK (announced at Developer Day 2025) and their partnership with Jony Ive signal ambition toward an "AI operating layer," but this is consumer-oriented and years from developer-focused realization.

**Autonomous Coding Agents.** OpenHands (formerly OpenDevin), which raised $18.8M, and similar platforms target autonomous task completion. These are complementary to what we're building, not competitive. They solve "do this task for me" while we solve "make my entire environment adapt to how I work."

### 2.2 The Gap That Defines Our Opportunity

No product today unifies these seven capabilities into a single integrated experience:

1. A minimal, declarative, reproducible OS base (NixOS territory)
2. A Wayland compositor as the rendering substrate (Hyprland territory)
3. A unified single-pane shell that embeds all developer tools in one frame (nobody's territory)
4. A dual-mode input line that blurs the boundary between terminal and AI (nobody's territory)
5. A hybrid inference engine that intelligently routes between on-device and cloud AI (Cactus Compute territory — but not yet applied to developer OS workflows)
6. A self-tuning daemon that observes workflows and reshapes the environment (nobody's territory)
7. Fleet-level AI adoption analytics and compliance reporting for engineering leadership (nobody's territory for developer workstation OSes)

Capabilities 3, 4, 6, and 7 are where no existing product operates. The combination is the moat.

### 2.3 Why This Hasn't Been Built

Four structural reasons explain the gap.

**Integration complexity.** The value proposition requires the full stack. Any individual component is a weekend project. The compound value only emerges when the shell, the embedded tools, the daemon, and the input bar all share context and work in concert.

**The privacy paradox.** The self-tuning layer requires deep OS-level instrumentation of developer behavior — exactly the kind of telemetry that developers are most hostile toward. A 2019 developer survey showed 20% of developers reject any telemetry flat-out, and among those who accept it, a majority require the data to be fully open and auditable. Building a product that watches your workflow while targeting people allergic to surveillance is a genuine product design challenge. The enterprise layer intensifies this tension — engineers need to trust that "fleet reporting" doesn't mean "my manager sees my screen."

**The cold-start problem.** You need enough user data for the AI layer to be useful, but you need the AI layer to be useful to attract users. Our mitigation is twofold: the unified shell must be valuable with zero AI — just as the best single-pane developer environment you've ever used — and the daemon's value should be immediately apparent even with a few hours of data.

**UX risk in abandoning the window metaphor.** Replacing tiling/floating windows with a single-pane model is a bet. Our mitigation is a split-pane mode within the main content area and the ability to "pop out" any tool into a Hyprland-managed window when needed.

---

## 3. Strategic Partnership: Cactus Compute

### 3.1 What Cactus Is

Cactus Compute is a YC-backed startup (4.2k+ GitHub stars) building an open-source, cross-platform AI inference engine optimized for low-power devices. Their core product is a C/C++ runtime that runs quantized LLMs (down to 2-bit precision) directly on-device with hardware-specific acceleration, zero-copy memory mapping for minimal RAM usage, and near-instant model loading.

The key capability is their **Hybrid Router** — a routing layer that automatically distributes AI queries between on-device inference and cloud models based on query complexity, device capability, and user-configurable policy. Their SDK supports four routing modes: `local` (strictly on-device), `localfirst` (try local, fall back to cloud), `remotefirst` (prefer cloud, use local if API fails), and `remote` (strictly cloud). The router exposes an OpenAI-compatible API, making it a drop-in replacement for existing LLM integrations.

Cactus claims 5x cost savings by handling over 80% of production inference on-device, sub-120ms on-device latency, and Linux/x86 support via their C++ backend.

### 3.2 Why This Partnership Matters for Aether

Cactus solves three of our hardest problems simultaneously.

**Problem 1: The 2017 MacBook Pro can't run local LLMs well.** Our previous plan used Ollama for local inference, which loads full model weights into RAM — impractical on 8GB hardware. Cactus's zero-copy memory mapping and INT4 quantization are purpose-built for constrained hardware. We can run a small model (Phi-3, Gemma 2B, or LiquidAI's LFM2-2.6B which Cactus directly supports) for the daemon's routine analysis tier without consuming the memory budget that the shell and developer tools need.

**Problem 2: We were maintaining two separate inference stacks.** The v2 plan used LiteLLM as the cloud router and Ollama as the local engine — two tools with different APIs, different configuration, and different failure modes. Cactus replaces both with a single hybrid runtime. One API. One configuration. One routing decision engine. The daemon's Analyzer calls Cactus, and Cactus decides whether to run locally or route to the cloud. This simplifies the architecture substantially.

**Problem 3: The enterprise cost story needed teeth.** Telling a CTO "we route AI queries intelligently" is vague. Telling them "80% of your fleet's AI inference runs on-device at zero marginal cost, and we only send the complex 20% to paid cloud APIs — here's the exact cost per query" is a procurement-ready number. Cactus's routing metrics (local vs. cloud, latency, cost-per-query) feed directly into the enterprise fleet dashboard.

### 3.3 How Cactus Integrates Into the Architecture

The Cactus engine replaces both LiteLLM and Ollama in the stack. It runs as a local service, managed by the NixOS flake, and exposes an OpenAI-compatible endpoint on localhost. The daemon's Analyzer sends all inference requests to this endpoint. Cactus's Hybrid Router decides the routing:

Simple queries (pattern lookups, command suggestions, file relevance scoring) are routed to the on-device model. This is the 80% — high frequency, low complexity, and latency-sensitive. These never touch the network.

Complex queries (weekly workflow analysis, codebase-level reasoning, multi-step problem diagnosis) are routed to a cloud frontier model (Claude, GPT-4, etc.) via Cactus's cloud fallback. This is the 20% — low frequency, high complexity, and the user can preview the prompt before it sends.

The routing mode maps to our privacy tiers: `local` mode for privacy maximalists (air-gap), `localfirst` for the default experience, `remotefirst` for users who prioritize quality, and `remote` for enterprise deployments where all inference goes through a centrally managed cloud endpoint.

### 3.4 What We Offer Cactus

Cactus is currently focused on mobile (iOS, Android) and wearables. Aether would be their first desktop OS integration and their first developer-tools use case — a new market vertical for their technology. We offer them a high-visibility deployment in a community (Linux developers) that deeply values open-source infrastructure, a real-world benchmark for their Linux/x86 runtime on constrained hardware, and potential enterprise distribution through our fleet model. In return, we get an optimized inference runtime without building one, and a partner with deep expertise in on-device AI efficiency.

### 3.5 Partnership Risks

Cactus is a startup. They could pivot, get acquired, or deprioritize Linux support. Mitigation: their engine is open-source (we can fork if needed), and their API is OpenAI-compatible (we can swap in any compatible backend). The architectural dependency is on the API shape, not on the company.

---

## 4. Hardware Constraints & Test Platform

### 4.1 The 2017 MacBook Pro

Our sole test machine is an Intel-based 2017 MacBook Pro. Expected specifications: Dual or Quad-Core Intel Core i5/i7, 8–16GB RAM, Intel Iris Plus 640/650 graphics, 256GB–1TB SSD, and a Broadcom Wi-Fi chipset that requires proprietary firmware under Linux.

**Linux support status.** NixOS runs on 2017 MacBook Pros with known caveats. The nixos-hardware repository includes modules for MacBook Pro models of this era. Key concerns include Broadcom Wi-Fi requiring a custom ISO with proprietary drivers baked in, Intel graphics working well under Wayland, power management being functional but not matching macOS efficiency, and the Touch Bar (if present on the 15" model) having limited Linux support. The T2 security chip is not present on 2017 models, which simplifies installation.

**Resource implications with Cactus.** The Cactus engine's zero-copy memory mapping means the on-device model doesn't load fully into RAM — it memory-maps the GGUF file from disk and loads layers on demand. A 2.6B parameter model quantized to INT4 is approximately 1.5GB on disk. With zero-copy mapping, peak RAM usage for inference is substantially lower than the file size. This changes the budget: the shell (Tauri + WebView, ~150–200MB), the daemon (~50MB), Cactus runtime (~100–200MB during inference), and the OS/compositor (~200MB) should fit within 8GB with headroom for the user's actual development tools.

### 4.2 Why This Constraint Is Actually Useful

If Aether runs well on a 2017 MacBook Pro with Cactus routing 80% of inference on-device, it validates the cost story for enterprise: even the cheapest laptop in your fleet can handle the bulk of AI workload without cloud API costs. That's a powerful demo.

---

## 5. The Unified Shell — UI Architecture

### 5.1 Design Philosophy

The Aether Shell is a single full-screen application that *is* the user's entire desktop. It runs as a Wayland client on Hyprland, occupying the full screen. Hyprland manages the low-level compositor responsibilities (GPU rendering, input handling, display management), while the Aether Shell manages everything the user sees and interacts with.

The design philosophy is: **a terminal that learned to be a GUI.** The entire interface should feel like a high-end terminal application — keyboard-first, dark, monospace typography, minimal chrome — but with the spatial affordances of a GUI: a navigation rail, tabbed content areas, inline rendering of rich content, and mouse interactivity when wanted.

### 5.2 Shell Anatomy

**The Left Rail.** A narrow (56px) vertical strip containing icon buttons for each embedded tool: Terminal, Editor, Browser, Git, Containers, and Insights (the daemon dashboard). Each icon has a keyboard shortcut (⌘1 through ⌘6). At the bottom, status indicators show daemon health, Cactus inference mode (local/hybrid/cloud), and daemon memory usage.

**The Main Content Pane.** Occupies all remaining screen space. The Terminal view is a full PTY-connected terminal emulator. The Editor view embeds Neovim via a terminal PTY. The Browser view is a WebView-based minimal browser. The Git view shows commit log, working tree, and diffs. The Containers view shows Docker container status. The Insights view is the daemon's privacy and analytics dashboard. The content pane supports split mode — horizontal or vertical — toggled via ⌘\\.

**The Suggestion Bar.** A single-line strip between the content pane and the input bar. This is the daemon's passive voice — a rotating feed of contextual suggestions based on recent activity. Tab accepts the current suggestion; Esc dismisses it. This is the primary mechanism for encouraging AI adoption organically — the suggestions demonstrate value without requiring the engineer to seek it out.

**The Unified Input Bar.** A persistent input field at the bottom with two modes, toggled via ⌥Tab. Shell mode ($) is a terminal prompt. AI mode (✦) is a natural language interface to the entire system with full context from the daemon. The dual-mode input is what makes the shell concept work — the developer never context-switches between "doing things" and "asking things."

### 5.3 Technical Implementation

The shell is built with Tauri (Rust backend + WebKitGTK WebView + TypeScript/Preact frontend). Terminal emulation via xterm.js connected to a PTY. The shell communicates with the daemon over a Unix socket, and the daemon communicates with Cactus via its OpenAI-compatible HTTP API on localhost.

---

## 6. Product Architecture

### 6.1 The Seven Layers

**Layer 1: Base OS (NixOS).** Declarative, reproducible, rollback-safe foundation.

**Layer 2: Compositor (Hyprland on Wayland).** GPU rendering substrate, multi-monitor support, fallback window management.

**Layer 3: The Aether Shell (Tauri).** The unified single-pane application — the user's entire desktop experience.

**Layer 4: Hybrid Inference Engine (Cactus Compute).** Runs as a local service, routes AI queries between on-device models and cloud APIs based on complexity, device capability, and user-configured privacy policy. Replaces the previous LiteLLM + Ollama stack with a single unified runtime.

**Layer 5: The Daemon (aetherd).** Go binary as a systemd service with three subsystems — Collector, Analyzer, Actuator — plus the enterprise Fleet Reporter. Communicates with the shell over a Unix socket and with Cactus via its local HTTP API.

**Layer 6: Fleet Aggregation Layer (enterprise only).** Receives anonymized, aggregated metrics from opted-in daemons across an engineering organization. Provides the leadership dashboard, compliance reporting, and cost analytics.

**Layer 7: Configuration (Nix Flake).** Single declarative specification for the entire stack.

### 6.2 The Daemon Architecture

**The Collector (Observation Layer).** Hooks into OS-level event sources: inotify/fsnotify for file events, /proc for process info, Hyprland IPC for display events, the shell's internal event bus (active tool, split config, input bar mode), Git activity, terminal commands, and build/test output. All raw events go to local SQLite. Nothing leaves the machine.

**Local store retention policy.** Raw events are inputs to the user model; derived patterns are the model itself. The store runs a background retention job that deletes raw events older than 90 days while keeping the `patterns` table indefinitely. This bounds disk growth without losing the accumulated intelligence. The 90-day window is user-configurable. The kill switch in the Insights view wipes both tables.

Additionally, the Collector now tracks **AI interaction metrics**: every time the engineer uses AI mode in the input bar, accepts a suggestion from the suggestion bar, or dismisses one, the event is logged with metadata (query category, routing decision from Cactus — local vs. cloud, response latency, acceptance/rejection). These metrics are the foundation of both the individual productivity story and the enterprise adoption story.

**The Analyzer (Intelligence Layer).** Operates at two tiers. The local tier (statistical heuristics, running on-device via Cactus's local model) handles pattern detection: file access frequency, command patterns, temporal patterns, build success rates, and AI interaction patterns. The cloud tier (frontier models via Cactus's cloud fallback) handles complex reasoning: weekly workflow summaries, codebase-level insights, and stuck-detection.

**The Actuator (Action Layer).** Passive actions feed the suggestion bar. Active actions (opt-in) adjust the shell layout, pre-warm containers, and adjust keybinding profiles. The suggestion bar is now also a vector for **progressive AI adoption** — it surfaces capabilities the engineer hasn't discovered yet, contextually matched to what they're currently doing.

**The Fleet Reporter (enterprise only).** A new subsystem that, when enabled and with explicit engineer opt-in, computes aggregate metrics and sends them to the Fleet Aggregation Layer. The Fleet Reporter never sends raw events, individual queries, file paths, code content, or anything that identifies a specific engineer's work. It sends only anonymized aggregate signals: AI query counts by category, suggestion acceptance rates, local-vs-cloud routing ratios, build velocity trends, and adoption tier classification (see Section 7).

### 6.3 The Privacy Model

The privacy architecture now has two distinct scopes.

**Individual scope (always on, non-negotiable).** All raw telemetry stays on the machine forever. Only summarized, user-reviewed context goes to external LLMs via Cactus's cloud fallback. The Insights view in the shell shows all data flows. All daemon code is open source. The Cactus engine is open source. The engineer has a kill switch to wipe the local store at any time.

**Fleet scope (enterprise only, opt-in per engineer).** If the engineer opts in to fleet reporting, the Fleet Reporter computes anonymous aggregates and sends them to the organization's Fleet Aggregation Layer. The engineer can see exactly what the Fleet Reporter sends — it's visible in the Insights view under a "Fleet Reporting" tab. The engineer can opt out at any time, and opting out has zero impact on their individual Aether experience. No raw data, no file paths, no code content, no query text ever flows to the fleet layer. Only counts, rates, ratios, and anonymized category labels.

The framing matters. We never call the fleet layer "telemetry." We call it "team insights." The engineer is sharing aggregate patterns with their organization, not being monitored by it.

---

## 7. Enterprise Strategy

### 7.1 The Two-Audience Problem

Enterprise Aether must satisfy two audiences with conflicting instincts. Engineers want privacy, autonomy, and tools that respect their intelligence. Leadership wants visibility, measurability, and ROI justification for their AI investment. The product must give both audiences what they want without either feeling like the other's needs are being served at their expense.

The resolution: **measure the environment, not the engineer.** Leadership gets fleet-level trends. Engineers keep individual-level control. The data that feeds both is the same — the daemon's observations — but the aggregation boundary (individual vs. fleet) determines who sees what.

### 7.2 What Leadership Gets: The Fleet Dashboard

The Fleet Aggregation Layer provides a web-based dashboard (deployed on the organization's infrastructure, not hosted by Aether) that gives engineering leadership four categories of insight:

**AI Adoption Analytics.** The most requested metric in enterprise AI deployment: "are our engineers actually using the AI tools we're paying for?" The fleet dashboard shows AI mode query volume over time (across the org, by team, by week), suggestion acceptance rates (what percentage of daemon suggestions are accepted vs. dismissed), adoption tier distribution (see below), AI query categories (code generation, debugging, documentation, architecture, workflow optimization), and trend lines showing adoption acceleration or plateau.

The **adoption tier model** classifies engineers (anonymously) into four tiers based on their AI interaction patterns: Tier 0 (Observer) — uses Aether but hasn't engaged with AI mode or suggestions. Tier 1 (Explorer) — occasional AI mode queries, low suggestion acceptance. Tier 2 (Integrator) — regular AI mode usage, moderate suggestion acceptance, AI is part of their workflow. Tier 3 (Native) — high-frequency AI usage across multiple categories, high suggestion acceptance, AI mode and shell mode are used interchangeably. The fleet dashboard shows the distribution across tiers over time. Leadership can see the org moving from "most engineers are Observers" to "most engineers are Integrators" — which is the story that justifies the next round of AI investment.

**Developer Velocity Correlation.** The daemon tracks commit cadence, build success rates, PR cycle time, and time-to-first-commit-on-new-repos. The fleet layer correlates these with AI adoption tiers (not individual engineers) to show patterns like "teams with a higher proportion of Tier 2–3 engineers ship 20% more PRs per sprint" or "engineers who moved from Tier 1 to Tier 2 saw a 15% improvement in build success rate." The framing is critical: this is about proving the tool works, not about ranking people. The dashboard explicitly labels these as correlations, not performance evaluations.

**AI Cost Efficiency.** Because all inference flows through Cactus, the fleet layer has exact cost data. The dashboard shows total AI API spend across the org (cloud-only, since on-device is free), cost per team, cost per model, local-vs-cloud routing ratio (the higher the local ratio, the lower the cost), cost per accepted suggestion (the ultimate ROI metric), and trend lines showing cost efficiency improving as the on-device model handles more queries. For a CTO managing a $500K/year AI API budget across 500 engineers, seeing that Aether routes 80% of queries on-device (free) and that the cost per accepted suggestion is $0.03 is a conversation-ending justification.

**Compliance & Security Posture.** For regulated industries (fintech, healthcare, defense), the dashboard shows which AI models are in use across the fleet, what percentage of queries are routed to approved endpoints, whether any engineers are using non-approved model providers, data residency compliance (all raw data local, only aggregates leave the machine), and a summary suitable for auditors: "100% of AI inference in Q1 2026 was routed through approved model endpoints. Zero raw code was sent to external APIs. All engineers are running Aether configuration version X."

### 7.3 Encouraging AI Adoption Without Mandating It

The enterprise buyer wants adoption. The engineer rejects mandates. Aether encourages adoption through four mechanisms that feel like product quality, not organizational pressure.

**Progressive disclosure via the suggestion bar.** The daemon starts with simple, obviously useful suggestions that don't even feel like "AI" — "you usually run tests after this edit" or "this container has been idle for 4 hours." Over time, as the engineer sees value in passive suggestions, the system surfaces more sophisticated capabilities: "I noticed your last three PRs had similar review comments about error handling — want me to pre-check your diff for the same patterns?" The AI earns trust through demonstrated utility.

**The AI mode input bar as a natural on-ramp.** The engineer lives in shell mode by default. But ⌥Tab is always one keystroke away. The suggestion bar occasionally surfaces things that can only be answered in AI mode — "want to understand why this test is flaky? ⌥Tab and ask." This creates natural discovery moments tied to real problems, not training sessions.

**Team-level shared insights (opt-in).** If multiple engineers on the same team opt in, the daemon can share anonymized patterns across the team: "3 engineers on your team hit the same build failure this week — here's the common fix" or "the most-queried documentation across your team this sprint was the payments API." This makes AI adoption social — engineers see peers getting value, which is more persuasive than any top-down mandate.

**Onboarding acceleration.** For new engineers joining a team, the daemon offers a "team context" mode that surfaces the most common workflows, frequently accessed files, and typical development patterns for that team's codebase — derived from aggregate opted-in data. "Engineers on this team typically start by running make setup, then open services/payments/handler.go." New hires get productive fast, which is a story both engineers and leadership love.

### 7.4 Enterprise Packaging

**Aether Open Source (individual developer).** The full shell, daemon (Collector + Analyzer + Actuator), and Cactus integration. Local-only. No fleet layer. Free forever. This is the adoption driver.

**Aether Enterprise.** Everything in open source, plus: the Fleet Reporter daemon subsystem, the Fleet Aggregation Layer (deployable on the organization's infrastructure via Helm chart or NixOS module), the leadership dashboard, centralized Cactus routing policy (the org can enforce `localfirst` or `remote` modes across the fleet), centralized model allowlisting (the org controls which cloud models engineers can route to), SSO integration for the dashboard, and priority support. Priced per seat, annually.

The engineer's local experience is identical in both tiers. The enterprise layer is purely additive — it aggregates what engineers choose to share and gives leadership visibility into fleet-level trends. The individual product earns love; the enterprise layer earns revenue.

### 7.5 Enterprise Go-to-Market

The adoption path mirrors what made VS Code → GitHub Copilot → Copilot for Business work. Individual engineers discover Aether, adopt it as their personal environment, and become internal advocates. When enough engineers on a team are using it, the team lead notices and asks about fleet visibility. The enterprise sale is pull, not push — it starts with bottom-up adoption and converts to top-down procurement when leadership sees organic usage and wants the dashboard.

The initial target vertical is **fintech** — our engineer's home territory, where engineering teams are large, AI investment is high, regulatory compliance is non-negotiable, and the privacy-first architecture is a genuine differentiator over hosted AI tooling.

---

## 8. Execution Plan

### Phase 0: Bare Metal Foundation (Weeks 1–2)

**Goal:** Boot a functional NixOS + Hyprland developer workstation on the 2017 MacBook Pro.

**Deliverables:** A custom NixOS ISO with Broadcom Wi-Fi drivers. A Nix flake that declaratively configures Hyprland, Kitty terminal, Neovim with LSP for Go and TypeScript, Git/lazygit, Docker, and ungoogled Chromium. The Cactus engine compiled and running locally — verify that `cactus run` works on the 2017 MBP with a small model (LFM2-2.6B at INT4 quantization).

**Engineer focus:** Nix flake authoring, hardware compatibility, Cactus Linux/x86 build.

**Exit criteria:** The engineer uses this as their daily driver. Cactus runs a local model and responds to queries within 200ms on the 2017 MBP.

### Phase 1: The Daemon v0 (Weeks 3–6)

**Goal:** A running Go daemon that observes, reports, and talks to Cactus.

**Deliverables:** `aetherd` as a systemd service. Collector watches file events, /proc, and Hyprland IPC. Events stored in SQLite. Hourly summary sent to Cactus's local API endpoint (which routes to local model or cloud based on complexity). Responses displayed as D-Bus notifications. `aetherctl` CLI for querying the local database. Unix socket server for future shell communication. AI interaction metrics tracked from the start (even though the shell doesn't exist yet, the daemon logs its own AI queries and Cactus routing decisions). Background retention job that prunes raw events older than 90 days while preserving derived patterns indefinitely.

**Engineer focus:** Go daemon architecture, Cactus API integration (OpenAI-compatible HTTP), SQLite schema with AI interaction metrics.

**Exit criteria:** Daemon runs 48+ hours stable, under 50MB RAM. Cactus routing works — simple queries go local, complex queries go cloud. Socket API is stable.

### Phase 2: The Aether Shell v0 (Weeks 7–14)

**Goal:** The unified single-pane shell replaces the tiling WM as the primary interface.

**Milestone 2a (Weeks 7–10): Shell skeleton.** Tauri app runs full-screen on Hyprland. Left rail, content pane, and unified input bar rendered. Terminal view works (real PTY via xterm.js). Input bar works in shell mode. Navigation via keyboard shortcuts.

**Milestone 2b (Weeks 11–14): Tool embedding and daemon connection.** Editor view (Neovim via PTY), browser view (WebView), git view, container view. Insights view connected to daemon socket — shows collected data, derived patterns, Cactus routing history, and LLM prompt previews. Suggestion bar live — daemon pushes suggestions through socket. AI mode input bar works — queries route through daemon to Cactus and responses render in the content pane. AI interaction metrics now rich: every AI mode query, every suggestion acceptance/dismissal, every Cactus routing decision logged.

**Engineer focus:** Tauri, xterm.js, Neovim PTY embedding, Unix socket client in Rust.

**Exit criteria:** The engineer uses the shell as their sole interface for a full day of development.

### Phase 3: Intelligence, Actuator & Polish (Weeks 15–22)

**Goal:** The daemon gets smart, the shell gets refined, and the feedback loop closes.

**Intelligence.** Local heuristic model in the Analyzer — frequency tables, pattern detection, temporal analysis, AI interaction analysis (which query categories are most used, what time of day AI mode is most active, suggestion acceptance trends). LLM tier enhanced with local model patterns. Ollama remains supported as an alternative to Cactus for users who prefer it.

**Actuator.** Passive suggestions in the suggestion bar. Active actuations (opt-in): auto-split pane on build, pre-warm containers, dynamic keybindings. Undo system (⌘Z at shell level). Progressive AI disclosure — the suggestion bar surfaces more sophisticated AI capabilities as the engineer's AI interaction tier increases.

**Shell polish.** Split-pane support. Pop-out to Hyprland windows. Command palette (⌘K). Theme customization via Nix flake. Visual refinement.

**Engineer focus:** Go statistical analysis, Cactus local model optimization, Tauri split-pane, feedback loop design.

**Exit criteria:** Three active actuations work. Suggestion acceptance rate above 60% (engineer self-testing). Split-pane and pop-out work. One external developer has tested.

### Phase 4: Enterprise Fleet Layer (Weeks 23–28)

**Goal:** The fleet aggregation layer and leadership dashboard.

**Deliverables — Fleet Reporter.** A new subsystem in `aetherd` that computes anonymous aggregate metrics and sends them to a central endpoint. Metrics include: AI query counts by category, suggestion acceptance rates, adoption tier classification, Cactus local-vs-cloud routing ratio, build velocity indicators, and cost-per-query data from Cactus. The Fleet Reporter is disabled by default and requires explicit opt-in. When enabled, the Insights view shows a "Fleet Reporting" tab with a live preview of exactly what is being sent.

**Deliverables — Fleet Aggregation Layer.** A small Go service (or a set of cloud functions) that receives Fleet Reporter data, aggregates across the org, and serves the leadership dashboard. Deployable on the org's infrastructure via a Helm chart or NixOS module. Stores aggregated data in PostgreSQL.

**Deliverables — Leadership Dashboard.** A web application (Preact + Tailwind, or similar) showing the four dashboard categories: AI Adoption Analytics (with adoption tier distribution), Developer Velocity Correlation, AI Cost Efficiency (with Cactus routing data), and Compliance & Security Posture. The dashboard is read-only — leadership can view trends but cannot drill into individual engineer data.

**Deliverables — Enterprise configuration.** Centralized Cactus routing policy (enforce `localfirst` across the fleet). Centralized model allowlisting. SSO integration for the dashboard.

**Engineer focus:** Go Fleet Reporter, aggregation service, PostgreSQL schema, dashboard frontend, Helm chart packaging.

**Exit criteria:** The fleet layer works end-to-end with at least 3 simulated engineer nodes. The dashboard shows meaningful adoption metrics. The engineer opt-in flow is clear and the Insights view accurately previews all outbound data.

### Phase 5: Distribution & Community (Weeks 29–36)

**Goal:** Other people can install and use Aether, both as individuals and as enterprise teams.

**Deliverables — Open source release.** The shell, daemon, and Cactus integration under Apache 2.0. Downloadable ISO (or Nix flake). Guided first-boot experience. Documentation site: installation, configuration, privacy model, shell shortcuts, daemon extension API, Cactus configuration, and enterprise setup guide. Community feedback channel.

**Deliverables — Enterprise pilot.** Identify 1–2 engineering teams (starting with the engineer's own network in fintech) for a closed enterprise pilot. Deploy the fleet layer. Gather feedback on the dashboard, adoption metrics accuracy, and the opt-in experience.

**Deliverables — Content.** Demo video showing the shell in action. Blog post on the architecture. A "privacy whitepaper" that details exactly how data flows in individual and fleet modes — this becomes the document that security teams review during procurement.

**Engineer focus:** Installation UX, documentation, packaging, cross-hardware testing, enterprise pilot support.

**Exit criteria:** 10+ individual installs with feedback. 1 enterprise pilot team running the fleet layer. 3+ hardware profiles validated. Demo video published.

---

## 9. Technical Stack Summary

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| Base OS | NixOS (stable channel) | Declarative, reproducible, rollback-safe |
| Compositor | Hyprland (Wayland) | GPU substrate, multi-monitor, IPC, pop-out windows |
| Shell Framework | Tauri (Rust + WebKitGTK) | ~5–10MB binary, native Rust backend |
| Shell Frontend | Preact + TypeScript | Lightweight, fast, minimal bundle |
| Shell Styling | IBM Plex Mono, hand-written CSS | Terminal-native aesthetic |
| Terminal Emulator | xterm.js (in Tauri WebView) | Mature, PTY-compatible |
| Editor | Neovim (via PTY in xterm.js) | Full Neovim, not a subset |
| Browser | Tauri WebView (WebKitGTK) | Zero extra cost, already in runtime |
| Inference Engine | Cactus Compute (C/C++ runtime) | Hybrid on-device/cloud routing, zero-copy memory, quantized models |
| On-Device Model | LFM2-2.6B (INT4) or similar | Small, fast, handles 80% of queries |
| Cloud Models | Claude, GPT-4, etc. (via Cactus cloud fallback) | Complex reasoning, 20% of queries |
| Daemon | Go — `aetherd` | Low memory, fast startup |
| Daemon CLI | Go — `aetherctl` | Query local store, manage config |
| Shell ↔ Daemon IPC | Unix domain socket + JSON | Fast, standard |
| Daemon ↔ Cactus | OpenAI-compatible HTTP (localhost) | Standard API, drop-in replaceable |
| Local Store | SQLite (WAL mode) | Zero-config, concurrent reads |
| Fleet Aggregation | Go service + PostgreSQL | Simple, deployable on org infra |
| Fleet Dashboard | Preact + TypeScript | Consistent with shell frontend |
| Enterprise Deploy | Helm chart or NixOS module | Matches org infrastructure patterns |
| Config | Nix flakes + Home Manager | Single-source for entire stack |

---

## 10. Success Metrics

**Phase 0 (Foundation).** NixOS + Hyprland boots on 2017 MBP. Cactus runs local inference in under 200ms.

**Phase 1 (Daemon v0).** Daemon runs 48+ hours. Under 50MB RAM. Cactus routing works (local and cloud paths verified).

**Phase 2 (Shell v0).** Engineer switches to shell as sole interface and doesn't switch back. All six views functional. Shell total memory under 200MB.

**Phase 3 (Intelligence & Polish).** Three active actuations work. Suggestion acceptance above 60%. Split-pane and pop-out work. One external tester gives positive feedback.

**Phase 4 (Enterprise).** Fleet layer works end-to-end. Dashboard shows adoption tiers. Cost metrics are accurate. Opt-in flow is clear to non-technical observers.

**Phase 5 (Distribution).** 10+ individual installs. 1 enterprise pilot. 3+ hardware profiles. Demo video published.

**Long-term (6–12 months post-launch).** 100+ individual users. 3+ enterprise pilots. Measurable movement up adoption tiers in pilot organizations. Fleet dashboard cited as procurement justification by at least one CTO. Third-party daemon plugins or shell themes emerge.

---

## 11. Risks & Open Questions

**Will the single-pane model frustrate power users?** Mitigations: split-pane, pop-out, and Hyprland fallback.

**Is Tauri mature enough?** Phase 2a validates the architecture before committing to full tool embedding. Fallback to Electron or custom GTK4.

**Can one engineer build Go + Rust + TypeScript?** The Rust surface area in Tauri is small. The engineer's TypeScript experience applies. If Rust is too slow to learn, the Tauri backend stays minimal with logic in the Go daemon.

**Will developers accept fleet reporting?** The opt-in model, the Insights preview, and the "team insights" framing are our mitigations. The enterprise pilot is the acid test.

**Will engineers feel surveilled by the adoption tier model?** The tiers are anonymous — leadership sees "42% of engineers are at Tier 2" not "Nick is at Tier 1." But perception matters. If pilot feedback shows anxiety about tiers, we rename or restructure them to be purely aggregate (e.g., "org-wide AI maturity score" instead of per-engineer tiers).

**Does the Cactus partnership create a single point of failure?** Cactus is open-source and the API is OpenAI-compatible. Worst case, we fork the engine or swap in Ollama + LiteLLM (the v2 architecture). The integration is at the API layer, not the binary layer.

**Is fintech the right initial vertical?** Our engineer has deep fintech networks, which de-risks the enterprise pilot. But fintech's compliance requirements may slow procurement. We should simultaneously pursue a second vertical with lighter compliance overhead — perhaps developer tools companies or SaaS startups — for faster enterprise learning.

**What happens when the LLM is wrong?** Bad passive suggestions are dismissible. Bad active suggestions are reversible (⌘Z). We build trust gradually.

---

## 12. Closing Perspective

Aether started as a thought experiment: what if your OS learned how you work? The v1 plan was a configured NixOS workstation with a daemon. The v2 plan added a unified shell that reimagines the developer desktop. This v3 plan adds an enterprise layer that turns individual developer productivity into organizational AI adoption intelligence, and a strategic partnership with Cactus Compute that makes the entire inference architecture more efficient, more private, and more cost-effective.

The product now has three moats: the unified shell (a new interaction model), the self-tuning daemon (a new intelligence layer), and the fleet analytics (a new enterprise observability product). Each layer makes the others more valuable. The shell makes the daemon's insights visible. The daemon makes the shell adaptive. The fleet layer makes both worth paying for.

The shell is what people will screenshot. The daemon is what will make them stay. The fleet dashboard is what will make their CTO pay.

One engineer. One old MacBook. One open-source inference partner. Three novel layers. Let's build it.
