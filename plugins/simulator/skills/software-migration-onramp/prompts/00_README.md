# Smart Company Onramp — Document Package

**From API to KPI.**
**Version:** 1.4 (master spec) / mixed (component specs)
**Date:** 17 May 2026
**Status:** ready for internal sign-off; pending Founder approval

---

## 30-second pitch

Universal AI migration factory. Преобразует «у клиента сейчас зоопарк систем — мы хотим Smart Company» из 6-12-месячного консалтингового проекта в продукт.

**Архитектурно:** 5 фаз воронки × 10 AI-агентов как акторы первого класса. Сама миграция — actor `Migration`, который содержит **personalized roadmap_graph** — directed graph из Phase actors с конкретными датами, dependencies, signoffs, статусами. Каждый клиент получает свой граф-роадмап, сгенерированный из templates и кастомизированный под специфику.

**По продукту:** 4 трека (L1-L4) × 4 deployment режима (Cloud/Hybrid/On-prem/Air-gapped) = 13 валидных конфигураций. От MSB на Cloud до банков на Air-gapped.

**Реальность долгих циклов:** 20-30% Migrations прерываются на паузу. ResumptionAgent (10-й агент) обрабатывает возвращения с freshness assessment и опциями Continue / Refresh & Continue / Restart from phase / Restart all. Roadmap может быть пересмотрен при resume.

**Рекурсия второго уровня:** Onramp работает на Simulator. Migration roadmap — actor graph как любой business process в Simulator. **Onramp Process pack** — meta-Industry-Pack про сам процесс миграции, который клонируется и кастомизируется под клиента.

---

## Document map

| # | Документ | Назначение | Размер | Аудитория |
|---|---|---|---|---|
| 01 | `01_master_spec.md` | Smart Company Onramp Master Spec v1.4 | ~45K | All leads |
| 02 | `02_discovery_agent_spec.md` | Detailed spec первого агента (Discovery) v1.3 | ~43K | Dev + Prompt eng |
| 03 | `03_discovery_agent_kb_bundle.md` | Knowledge base bundle для Discovery Agent v1.1 | ~26K | Prompt eng |
| 04 | `04_system_profiler_agent_spec.md` | Detailed spec второго агента (5 режимов) v1.1 | ~39K | Dev |
| 05 | `05_golden_house_simulation.md` | Narrative: путь Golden House через 9 агентов v1.1 (с v1.4 reframing) | ~47K | All |
| 06 | `06_signoff_chart.md` | Approval roadmap, 14 sign-off items v1.1 | ~24K | Founder + leads |

**Total:** ~225K markdown, 6 документов + README. Все согласованы с Master Spec v1.4.

---

## Reading paths

### Если вы Founder (Alexander)
1. **README**
2. **01 Master Spec** — особое внимание Part 1.2 (двухуровневая Migration), Part 1.2.1 (Phase actor schema), Part 3.8 (Roadmap mechanics), Part 5.6 (Onramp Process pack).
3. **06 Sign-off Chart**.
4. **05 Golden House Simulation** — proof.
5. **02-04** — детали по агентам.

### Если вы Tech Lead
1. **01 Master Spec** — особое внимание двухуровневой архитектуре Migration, Phase actor schema, roadmap generation/modification/forecasting.
2. **02 Discovery Agent Spec** — для понимания структуры детального spec'а.
3. **04 System Profiler Spec**.
4. **05 Golden House Simulation** — gap'ы.

### Если вы Solution Architects Lead (Никита)
1. **01 Master Spec** parts 5 (Industry Packs incl. **Onramp Process pack**), 6 (Compliance), 3.8 (Roadmap mechanics).
2. **02 Discovery Agent Spec** part 5.
3. **05 Golden House Simulation** — поход через фазы.

### Если вы Sales Lead
1. **01 Master Spec** parts 4 (tracks), 8 (commercial), 9 (roadmap).
2. **06 Sign-off Chart** S4, S5.
3. **05 Golden House Simulation**.

### Если вы Marketing
1. **01 Master Spec** part 1 (Vision), part 13 (концепции), part 10 (рекурсия).
2. **05 Golden House Simulation**.

### Если вы Developer на агенте
- DiscoveryAgent → **02** + **03**.
- SystemProfilerAgent → **04**.
- Остальные 8 агентов outlined в **01 Master Spec** part 2.

### Если вы Partner
1. **README**
2. **01 Master Spec** parts 0-1, 4-5, 8.4.
3. **05 Golden House Simulation**.

---

## Core concepts cheat sheet

### Migration как двухуровневая структура

```
Migration (overall actor)
   identity, state, accumulator (overall metrics)
   ↓ contains
roadmap_graph (acyclic directed graph)
   ↓ nodes are
Phase actors (first-class)
   identity, state, accumulator (per-phase metrics)
   events, signoffs, dependencies
```

Каждая фаза — самостоятельный актор с собственной жизнью. Roadmap — это actor graph внутри Migration.

### 10 AI-агентов

| # | Agent | Назначена Phase actor типа | Что делает |
|---|---|---|---|
| 1 | DiscoveryAgent | Discovery | Веде разговор, генерирует initial roadmap |
| 2 | SystemProfilerAgent | Profiling | Профилирует в 5 режимах |
| 3 | GraphSynthesizer | Synthesis | Объединяет profiles |
| 4 | MappingAgent | Mapping | Source → actor rules |
| 5 | DataMigrator | Data Migration | Переносит данные |
| 6 | ProcessMigrator | Process Migration | Workflows → state graphs |
| 7 | ValidationAgent | Validation | Observer parallel run |
| 8 | CutoverAgent | Cutover | Go-live supervisor |
| 9 | EvolutionAgent | Operate/Evolve | Долгосрочный рост maturity |
| 10 | ResumptionAgent | Cross-phase | Pause/resume cycles, roadmap revision |

### 4 трека × 4 deployment режима
**Tracks:** L1 Greenfield / L2 Single replacement / L3 Multi-system / L4 Enterprise.
**Deployment:** Cloud / Hybrid / On-premise / Air-gapped.

Critical: DataMigrator и ValidationAgent НИКОГДА не в managed cloud для Hybrid+.

### Pause/Resume mechanics
4 опции при resume: Continue / Refresh & Continue (recommended) / Restart from phase / Restart all. ResumptionAgent оценивает freshness и предлагает опции. Restart сохраняет prior знания.

### Onramp Process pack — meta-Industry-Pack
4 default roadmap templates (L1-L4). DiscoveryAgent клонирует template + customизирует под клиента. Pack эволюционирует на основе реальных миграций.

### Industry Packs
✅ Furniture Retail. Roadmap: Auto Parts, Wholesale FMCG, Services, HoReCa, UA Banking Compliance, Light Manufacturing, Apparel, Pharmacy, Healthcare, Government.

### Compliance Packs (family)
✅ UA Retail Compliance. Q1 2027: UA Banking. 2027-2028: PL, RO, EU GDPR, US.

### Валентности (8 ролей)
viewer / reviewer / approver / signer / executor / delegate / blocker / observer.

### Account types
Identity / State / Accumulator. Dr/Cr-транзакции через events.

---

## Status & immediate next steps

### Status
- **Architecture:** v1.4 stable. Migration двухуровневая (Migration + Phase actors), 10 агентов, 4 deployment modes, pause/resume mechanics, Onramp Process pack.
- **Specs:** 2 агента из 10 — full detail. Остальные 8 — outlined.
- **Pack:** 1 готов (Furniture Retail). Onramp Process pack templates — Q3 2026.
- **Pilot client:** Golden House.

### Что нужно от Founder в ближайшие 2-3 недели

- [ ] **S1 — Brand & Vision.** «Smart Company Onramp» + «From API to KPI».
- [ ] **S2 — Master Spec v1.4.** Прочитать `01_master_spec.md`, утвердить или прислать правки.
- [ ] **S3 — Tech Roadmap.** Quarterly breakdown + capacity.
- [ ] **S6 — Marketing approach.** Whitepaper / каналы.
- [ ] **NEW S15 — Pause/Resume strategy + ResumptionAgent.** Утвердить подход.
- [ ] **NEW S16 — Migration Roadmap as Actor Graph + Onramp Process pack.** Утвердить двухуровневую архитектуру и Phase actors как первоклассные элементы.

### Команда tасков для леадов
- **Tech Lead:** S3, S8, #1 fall-back до Golden House. **Phase actor schema + базовая roadmap generation в Q3 2026.**
- **Sales Lead:** S4, S10, 8.5 pause-aware contracts.
- **Solution Architects Lead:** S7 (Pack roadmap incl. **Onramp Process pack**), готовность пилота.
- **Marketing Lead:** S6.
- **Operations Lead:** S5, S11.

---

## Versions

| Документ | Текущая | Status |
|---|---|---|
| Master Spec | **v1.4** | stable |
| Discovery Agent Spec | **v1.3** | stable, согласован с v1.4 (добавлен generate_roadmap_graph) |
| Discovery Agent KB Bundle | **v1.1** | stable, добавлен Onramp Process pack как RAG-источник, Gate 8 |
| System Profiler Agent Spec | **v1.1** | stable, привязан к Phase actor "Profiling" |
| Golden House Simulation | **v1.1** | stable, с v1.4 reframing section в начале |
| Sign-off Chart | **v1.1** | stable, добавлены S15 (Pause/Resume) + S16 (Migration Roadmap) |

**Master Spec прошёл 5 итераций:**
- v1.0: первая универсальная архитектура.
- v1.1: critical assessment.
- v1.2: deployment topology.
- v1.3: PAUSED state + ResumptionAgent.
- **v1.4: Migration Roadmap as Actor Graph (per client), Phase actors, Onramp Process pack — meta-Industry-Pack.**

---

## Roadmap собирания остальных артефактов

1. **Onramp Process pack detailed spec** — Q3 2026 (один из первых после Phase actor schema).
2. **Phase actor types catalog** — Q3 2026.
3. **ResumptionAgent detailed spec** — Q4 2026.
4. **Roadmap UI design** — Q2 2027.
5. **Cross-pack patterns library spec** — Q1 2027.
6. **Specs остальных 7 агентов** — Q4 2026 - Q2 2027.
7. **UA Banking Compliance Pack** — Q4 2026 - Q1 2027.
8. **On-prem deployment guide** — Q1 2027.

---

*Snapshot 14-17 May 2026. Alexander Vityaz × Claude. AG-native, русский с английскими/украинскими техническими терминами.*
