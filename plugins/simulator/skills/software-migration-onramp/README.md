# software-migration-onramp

Discovery facilitator skill for **Smart Company Onramp** — implementation of
the leadership-provided Discovery Agent specification.

The skill bundles the 7 authoritative prompts from
`Migration of any software to the simulator/Prompts/` and instructs Claude to
follow them verbatim during a Discovery session. Output: local JSON file
representing the actor data that would otherwise be POSTed to Simulator.

---

## What's inside

```
software-migration-onramp/
├── SKILL.md           ← entry point with frontmatter (read by Claude on activation)
├── README.md          ← this file (for human maintainers)
└── prompts/           ← leadership-provided canonical specifications
    ├── 00_README.md
    ├── 01_master_spec.md                  (Master Spec v1.4)
    ├── 02_discovery_agent_spec.md         (Discovery Agent Spec v1.3 — CANONICAL)
    ├── 03_discovery_agent_kb_bundle.md    (KB Bundle v1.1)
    ├── 04_system_profiler_agent_spec.md   (SystemProfilerAgent Spec v1.1)
    ├── 05_golden_house_simulation.md      (worked example)
    └── 06_signoff_chart.md                (approval roadmap)
```

---

## What this skill does

1. Activates when the operator says trigger phrases (see `SKILL.md` frontmatter)
2. Reads `prompts/02_discovery_agent_spec.md` and follows it as canonical instructions for how to act as DiscoveryAgent
3. Conducts the 5-phase Discovery dialog defined in §4 of the spec
4. Runs the 8 Quality Gates from `prompts/03_*.md` §4
5. Watches for the 8 escalation triggers from `prompts/02_*.md` §9
6. Writes session results as JSON to `./discovery-output/<client-slug>/actor-graph.json`
7. Hands off to solution architect after Phase 5

The skill is a **faithful implementation** of the spec. No architectural
extensions, no synthesized data structures, no operational deviations beyond
the single one explicitly documented (local JSON output instead of MCP calls
to Simulator — covered in SKILL.md «Operational adaptation»).

---

## Installation into Simulator plugin

When ready, copy the skill into your plugin project:

```bash
cp -r "/Users/user/Documents/Middleware/Migration of any software to the simulator/software-migration-onramp" \
      "/Users/user/Documents/Middleware/AI SIMULATOR AGENT/corezoid-ai-plugin/plugins/simulator/skills/"
```

Restart Claude Code. The skill activates on trigger phrases listed in
`SKILL.md` frontmatter description.

---

## Activation triggers

Specifically tied to starting a Discovery session. See `SKILL.md` frontmatter
for the full list. Examples:

- «начать discovery»
- «проведи discovery»
- «сделай discovery»
- «запусти онбординг клиента»
- «новый клиент — запускай»
- «discovery с клиентом»
- «migration onramp discovery»

The skill does NOT activate for general questions about Onramp architecture
or Smart Company concepts — those are design discussions.

---

## Output

After Discovery completes, you'll find:

```
./discovery-output/<client-slug>/
└── actor-graph.json   ← contains Lead actor data + Migration + Phase actors
                          conforming to prompts/02_*.md §6 and
                          prompts/01_*.md Part 1.2.1
```

The JSON is structured so it can later be POSTed via simulator MCP tools
(when wired up) without conversion. For now it's a deliverable: the operator
or SA can review the file, and a separate workflow can push it to Simulator.

---

## Updating the prompts

If leadership updates the source prompts in
`Migration of any software to the simulator/Prompts/`, sync the bundled
copies:

```bash
cp "Migration of any software to the simulator/Prompts/"*.md \
   "Migration of any software to the simulator/software-migration-onramp/prompts/"
```

No other change is needed — SKILL.md references the prompts by path and
follows whatever the latest spec says.

---

## Versioning

- **v1.0.0** — initial release. Pure pass-through to `prompts/` with one
  operational adaptation (local JSON output instead of MCP calls).

Version lives in `SKILL.md` frontmatter only.

---

## License

Same as parent `corezoid-ai-plugin` repository.
