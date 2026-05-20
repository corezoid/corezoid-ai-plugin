# Smart Company Onramp — Universal Migration Project

**Master Specification v1.4**
**Дата:** 17 мая 2026
**Аудитория:** команда разработки, solution architects, sales, marketing, партнёры.
**Слоган:** *From API to KPI.*

**Изменения от v1.3:** Migration Roadmap as Actor Graph — персонализированный граф-роадмап для каждого клиента. Каждая фаза — первоклассный актор Phase. Onramp Process pack как мета-Industry-Pack. См. Part 16 (Changelog).

---

## Part 0. Резюме

Универсальная AI-фабрика миграции любого софта на Simulator + Corezoid. Каждый клиент получает **персонализированный Migration Roadmap** как actor graph, генерируемый из шаблона трека и подстраиваемый под специфику клиента.

Ключевые принципы:

1. **Migration as Actor Graph.** Миграция — актор `Migration`, который содержит **roadmap_graph** — directed graph из акторов Phase.
2. **Каждая Phase — первоклассный актор** со своими accounts, статусом, dependencies, signoffs, target dates.
3. **10 AI-агентов, conditional не linear.** Девять основных по фазам + ResumptionAgent для pause/resume.
4. **5 фаз воронки** — это template. Реальный roadmap клиента может включать sub-phases, parallel branches, custom nodes.
5. **4 трека × 4 deployment режима** = 13 валидных конфигураций.
6. **Industry Packs + Onramp Process pack** — последний моделирует сам процесс миграции как actor graph.
7. **Самореференция:** Onramp работает на Simulator. Roadmap миграции — это actor graph, как любой другой в платформе.

### Что универсально, что специфично

| Слой | Universal | Context-specific |
|---|---|---|
| Архитектура | Migration as Actor Graph, **Roadmap as Phase actor graph**, 5 фаз template, 10 агентов, deployment topology, pause/resume mechanics | — |
| Deployment | Pattern | Stack: AWS/GCP/Azure vs self-hosted |
| Compliance Pack | Pattern | UA Retail / UA Banking / EU GDPR / US SOX |
| Industry Packs | Actor-template pattern | Список приоритетных vertical'ов |
| **Onramp Process pack** | **Meta-Industry-Pack для самой миграции** | Customizations per track, deployment, special needs |
| Tracks (L1-L4) | Sizing framework + roadmap template | Прайсовые точки |
| Pause/Resume | Universal pattern | Триггеры зависят от vertical |

---

## Part 1. Архитектурный фундамент

### 1.1 Vision: From API to KPI

(Без изменений от v1.2.)

### 1.2 Migration as Actor Graph

Migration — это **контейнер-актор**, содержащий nested actor graph. Структура:

```
Migration (overall actor)
├── identity: client_name, track, deployment_mode, industry, target_cutover_date
├── state: current_phase_id, overall_status
├── accumulator: overall_progress, freshness_score, time_in_pause
│
└── roadmap_graph:
    ├── Phase actor 1 (e.g. Discovery)
    │   ├── identity: phase_name, owner_agent, depends_on[], target_date, signoffs_required[]
    │   ├── state: status, assigned_to, blocked_reason
    │   └── accumulator: progress_pct, time_spent_h, deviation_days
    ├── Phase actor 2 (e.g. System Profiling)
    │   ├── ...
    └── ... (per template + customizations)
```

**Roadmap_graph генерируется DiscoveryAgent'ом** в Этапе 5 (Presentation), на основе:
1. Выбранного трека (template из Onramp Process pack)
2. Deployment mode (some phases добавляются/убираются)
3. Target deadline клиента (даты на nodes)
4. Specifics (custom nodes если нужно — например, "ждём согласования совета директоров")

**Граф состояний Migration (расширен в v1.4):**

```
INITIATED → ROADMAP_GENERATED → ACTIVE → 
  → CUTOVER_READY → CUTOVER_EXECUTED → OPERATING → EVOLVING
        ↳ ROLLBACK (из CUTOVER при критических ошибках)
        
PAUSED (transition в/из из ACTIVE)
   ↓ resume_requested
RESUMING
   ↓ choice
[continue / refresh_continue / restart_phase / restart_all]
```

Заметьте: высокоуровневый граф Migration **короче**, чем в v1.3. Детали — внутри roadmap_graph через Phase actors. Это лучшее разделение concerns.

**Identity accounts Migration:** `client_name`, `track` (L1-L4), `deployment_mode`, `start_date`, `target_cutover_date`, `industry`, `assigned_sa_id`, **`roadmap_graph_id`**.

**Accumulator accounts Migration (overall metrics, агрегированы из Phase actors):**
- `overall_progress_pct` — weighted mean of phase progresses
- `phases_completed_count`
- `phases_blocked_count`
- `phases_paused_count`
- `forecasted_cutover_date` — на основе текущей velocity
- `deviation_from_plan_days` — отклонение от initial target
- `freshness_score`, `staleness_flags[]`
- `time_in_state_PAUSED`, `pause_count`

**События на Migration:**
- `Migration.Started`, `Roadmap.Generated`, `Roadmap.Approved`, `Roadmap.Modified` (когда SA правит)
- `Phase.<id>.StatusChanged` (subscribed от nested Phase actors)
- `Migration.Paused`, `Migration.ResumeRequested`, `Migration.Resumed`, `Migration.Restarted`
- `Cutover.Executed`, `Rollback.Triggered`, `Evolution.Milestone`

**Инварианты на Migration:**
- Все upstream Phase actors must be `completed` before downstream can become `active`
- `validation_consecutive_match_days >= 14` (на Validation Phase actor) → CUTOVER Phase может стать active
- `cutover_readiness_score >= 0.85` → Cutover may execute
- `time_in_state_PAUSED > 180 days` → Continue/Refresh blocked

### 1.2.1 Phase actor — детальная структура

Каждый Phase actor имеет:

**Identity accounts:**
- `phase_id`, `phase_name`, `phase_type` (sense/model/migrate/validate/evolve/custom)
- `owner_agent_id` (какой AI-агент responsible)
- `depends_on[]` (list of phase_ids)
- `target_start_date`, `target_end_date`
- `signoffs_required[]` (list of {role, person_or_actor_id})
- `is_parallel_with[]` (другие phases, идущие параллельно)
- `is_critical_path` (boolean)

**State accounts:**
- `status` ∈ {pending, ready, active, blocked, paused, completed, skipped, reworked}
- `current_assignee_id`
- `blocked_reason` (если status == blocked)

**Accumulator accounts:**
- `progress_pct` (0-100)
- `time_spent_hours`
- `time_budget_hours`
- `deviation_from_plan_days`
- `artifacts_produced_count`
- `signoffs_collected_count`
- `rework_count` (сколько раз phase пересдавалась)

**События Phase actor:**
- `Phase.Started`, `Phase.Blocked`, `Phase.Unblocked`, `Phase.Paused`, `Phase.Resumed`
- `Phase.SignoffReceived`, `Phase.SignoffMissed`
- `Phase.Completed`, `Phase.Skipped`, `Phase.Reworked`

**Валентности на Phase events:**
- `executor` = owner_agent (AI agent или SA)
- `signer` = по списку signoffs_required
- `viewer` = client product_owner + assigned team

### 1.3 Валентности — 8 ролей

(Без изменений от v1.1: viewer, reviewer, approver, signer, executor, delegate, blocker, observer.)

### 1.4 Универсальная воронка: 5 фаз × 10 AI-агентов

```
Phase 1 — Sense:        DiscoveryAgent, SystemProfilerAgent
Phase 2 — Model:        GraphSynthesizer, MappingAgent
Phase 3 — Migrate:      DataMigrator, ProcessMigrator
Phase 4 — Validate:     ValidationAgent, CutoverAgent
Phase 5 — Evolve:       EvolutionAgent

Cross-phase:            ResumptionAgent (activated by PAUSED state)
```

**Это template.** Реальный roadmap клиента может включать custom phases между этими (например, "Board approval awaited" — manual Phase без AI owner).

### 1.5 Deployment topology

(Без изменений от v1.2.)

**Дополнение для v1.4:** в roadmap_graph deployment mode влияет на список phases. Air-gapped добавляет phase "Air-gap setup" перед SystemProfiling. On-prem добавляет phase "On-prem installation" после Mapping.

---

## Part 2. Детальные спецификации AI-агентов

(Из v1.2-v1.3, без изменений в основном содержании.)

**Дополнение в v1.4:** каждый AI-агент работает с **назначенной Phase actor**, не с overall Migration state. То есть DiscoveryAgent выполняет работу для Phase actor "Discovery", SystemProfilerAgent — для "System Profiling", и т.д. Это даёт чистое разделение responsibilities.

### Особое дополнение для DiscoveryAgent

В Этапе 5 (Presentation) DiscoveryAgent дополнительно:
1. Вызывает tool `generate_roadmap_graph(lead_actor_id, track, deployment_mode, target_deadline)` — генерирует initial roadmap_graph из template + customizations.
2. Показывает roadmap клиенту визуально (как диаграмма выше для Golden House).
3. Получает initial sign-off на roadmap (event `Roadmap.Approved` со signer = product_owner_клиента).
4. Передаёт roadmap_graph_id в актор Migration.

### Особое дополнение для ResumptionAgent

ResumptionAgent при resume:
1. Читает текущий roadmap_graph.
2. Видит, какие Phase actors completed, active, paused.
3. Оценивает freshness каждого Phase actor отдельно (не весь Migration целиком).
4. При Refresh & Continue — для каждого stale Phase actor запрашивает refresh action у соответствующего AI-агента.
5. Может предложить **roadmap_revision** — изменения в graph (новые даты, новые phases, перестановка). Клиент signs revision как event `Roadmap.Revised`.

---

## Part 3. Cross-cutting concerns

Из v1.2-v1.3 без изменений: 3.1 fall-back, 3.2 conflict resolution, 3.3 cross-pack patterns, 3.4 hybrid coordination, 3.5 activation conditions, 3.6 on-prem, 3.7 pause/resume.

### 3.8 Roadmap mechanics (новый в v1.4)

#### 3.8.1 Roadmap templates per track

В Onramp Process pack (Industry Pack для самой миграции) хранятся template roadmap_graphs:

| Track | Default phases | Опциональные phases |
|---|---|---|
| L1 Greenfield | Discovery, Pack Selection, Data Init, Setup, Training, Go-Live, Operate, Evolve | — |
| L2 Single replacement | Discovery, Profiling, Mapping, Data Migration, Process Migration, Validation, Cutover, Operate, Evolve | Synthesis (degraded) |
| L3 Multi-system | Discovery, Profiling (N), Synthesis, Mapping, Data Migration, Process Migration, Validation (cross-system), Staged Cutover (N), Operate, Evolve | — |
| L4 Enterprise | All из L3 + Pre-engagement security review, On-prem setup, Compliance audit, Extended validation, Board approval phase | По специфике клиента |

Каждый template — это actor graph в Onramp Process pack каталога. Клонируется при `generate_roadmap_graph`.

#### 3.8.2 Roadmap customization при генерации

DiscoveryAgent кастомизирует template:

1. **Подстановка дат.** Target cutover делится на phases пропорционально template weight'ам.
2. **Phase removal.** Если track не требует phase (например, Synthesis для L2) — phase помечается as `skipped`.
3. **Phase addition.** Если клиент upomянул специфику (например, "нужно одобрение совета директоров перед cutover") — добавляется custom Phase.
4. **Parallel branches.** Где template позволяет parallelism (Synthesis + Mapping) — устанавливаются edges соответственно.
5. **Signoff binding.** На основе Lead data — кто из людей клиента будет signer на каждой phase.

Output: customized roadmap_graph + event `Roadmap.Generated`.

#### 3.8.3 Roadmap modification в течение Migration

Roadmap не immutable. SA может править в течение Migration:

| Действие | Event | Кто signs |
|---|---|---|
| Add custom phase | `Phase.Added` | SA + product_owner_клиента |
| Remove phase | `Phase.Removed` (только если не started) | SA |
| Block phase | `Phase.Blocked` (с reason) | SA |
| Change phase dates | `Phase.Rescheduled` | SA |
| Reorder phases | `Roadmap.Reordered` | SA + product_owner_клиента (если breaking) |
| Mark phase for rework | `Phase.RequiresRework` | SA |

Все модификации — events на Migration, audit trail сохраняется.

#### 3.8.4 Roadmap forecasting

`forecasted_cutover_date` обновляется в real-time на основе:
- Текущей velocity (сколько progress_pct по dependency-chain phases за день)
- Average rework rate
- Blocked time history
- Pause history

Это даёт клиенту always-current ETA, а не только initial target_cutover_date.

#### 3.8.5 Roadmap visualisation

Клиент видит свой roadmap в UI:
- Граф-view: nodes = phases, edges = dependencies, colors = status, label = dates
- Gantt-view: timeline horizontal с критическим путём подсвеченным
- Calendar-view: даты привязаны к календарю
- List-view: simple checklist

Все view — это разные projections на roadmap_graph (как любые дашборды в Simulator — это проекции на акторы).

---

## Part 4. 4 трека миграции + deployment modifier

(Без изменений от v1.2.)

**Дополнение в v1.4:** каждый track имеет свой default roadmap template в Onramp Process pack.

---

## Part 5. Industry Packs

### 5.1 Концепция

Industry Pack — это актор-шаблон в каталоге Simulator. При вызове `create_prototype_stand` он клонируется как актор `Prototype` под клиента.

### 5.2-5.4
(Без изменений от v1.2: Furniture Retail готов, roadmap расширения, methodology.)

### 5.5 Pack + Deployment matrix
(Без изменений от v1.2.)

### 5.6 Onramp Process pack (новый в v1.4)

**Самая важная meta-конструкция:** Onramp Process pack — это Industry Pack про сам процесс миграции.

**Состав:**
- 4 default roadmap_graph templates (L1, L2, L3, L4)
- Каждый template — это actor graph из Phase actors со связями и dependencies
- Templates ссылаются на AI-агентов как owner_agent (так что при изменении набора агентов, templates автоматически наследуют)
- Templates подвергаются версионированию

**Owner:** Solution Architects Lead.

**Эволюция:** после каждой реальной миграции — feedback в templates. Какие phases чаще всего добавляются как custom? Какие сроки реальны vs планируемые? Это превращается в template improvements.

**Самореференция:** Onramp Process pack хранится **в той же Simulator**, на которую клиент мигрирует. Это значит:
- Клиент видит свой roadmap как actor graph
- Команда Simulator видит roadmaps всех клиентов как dashboard
- Roadmaps команды для своих внутренних проектов — те же templates
- AI-агенты внутри Simulator работают по тем же graph patterns

---

## Part 6. Regional + Industry Compliance Packs

(Без изменений от v1.2.)

---

## Part 7. Метрики и дашборды воронки

(Без изменений от v1.2-v1.3.)

**Дополнения для v1.4:**

### 7.6 Roadmap metrics

В дашборде команды Simulator:

- `avg_phase_count_per_migration` (по трекам)
- `template_coverage_pct` — % migrations, прошедших без custom phases
- `phase_completion_rate` — % phases, completed без rework
- `phase_blocked_rate` — % phases, бывших blocked
- `roadmap_revisions_per_migration_avg` — как часто SA правит roadmap
- `forecast_accuracy` — насколько initial target_cutover_date соответствует actual
- `critical_path_deviation_avg`

Эти метрики обратной связью идут в Onramp Process pack templates — улучшая их версии.

---

## Part 8. Commercial structure

(Без изменений от v1.2-v1.3.)

---

## Part 9. Roadmap сборки Onramp

### Q3 2026 — Cloud foundation
- DiscoveryAgent v1.2 в Cloud mode.
- Furniture Retail Pack.
- **Onramp Process pack v0.5** — templates для L1, L2.
- **Phase actor schema implementation.**
- **Базовый roadmap_graph generation** в DiscoveryAgent.
- Cross-pack patterns library foundation.
- Fall-back behavior для DiscoveryAgent.
- Golden House — первый клиент.
- Базовый PAUSED state на акторе Migration.

### Q4 2026 — Profiling + first hybrid case
- SystemProfilerAgent (Cloud).
- DataMigrator (Cloud).
- Activation conditions.
- Hybrid deployment proof-of-concept.
- 2-й Industry Pack: Auto Parts.
- 5-10 клиентов.
- ResumptionAgent v0.5.
- **Roadmap modification mechanics** (SA может править live).
- **Roadmap forecasting v0.5.**

### Q1 2027 — Validation + Banking foundation
- ValidationAgent.
- CutoverAgent.
- Conflict resolution mechanism.
- On-prem deployment framework.
- UA Banking Compliance Pack v0.5.
- 3-й Industry Pack.
- 15-25 клиентов.
- ResumptionAgent v1.0.
- **Onramp Process pack v1.0** — templates для L3, L4.

### Q2 2027 — Full Onramp + first banking client
- GraphSynthesizer + MappingAgent для multi-system.
- ProcessMigrator.
- EvolutionAgent с cold-start.
- First banking client (strategic, L4 + On-prem).
- 4-й и 5-й Industry Packs.
- Партнёрская сеть alpha.
- 50+ клиентов.
- **Roadmap visualization full UI** (graph/Gantt/calendar views).

### Q3-Q4 2027 — Scale + Air-gapped capability
(Без изменений от v1.3.)

### 2028 — Geographic expansion
(Без изменений от v1.3.)

---

## Part 10. Концептуальная рекурсия

Onramp работает на самой Simulator. Это означает:
- Demo = product. Sales-процесс работает в Simulator.
- AI-агенты Onramp = AI-агенты, которые получает клиент.
- Partner ecosystem = actor graph.
- **NEW в v1.4:** Migration Roadmap — это actor graph, такой же как любой business process в Simulator. Самореференция второго уровня: Onramp моделирует свой собственный процесс как Industry Pack.

---

## Part 11. Связанные документы

(Без изменений от v1.2.)

**Будущие документы:**
- **Onramp Process pack detailed spec** (Q3 2026) — все templates для L1-L4.
- **Phase actor types catalog** (Q3 2026).
- **Roadmap UI design** (Q2 2027).
- ResumptionAgent detailed spec.
- On-prem deployment guide.
- Banking Compliance Pack spec.

---

## Part 12. Architectural improvements roadmap

8 improvements из v1.1 + 1 из v1.2 + 1 из v1.3 + 1 новое в v1.4.

### Existing
- #1-#8 (из v1.1)
- #9 — Deployment topology (из v1.2)
- #10 — Pause/Resume mechanics с ResumptionAgent (из v1.3)

### New in v1.4
**#11 — Migration Roadmap as Actor Graph (per client) + Onramp Process pack.**
Trigger: уже сейчас — это закрывает фундаментальный gap между generic state machine и реальностью персонализированной миграции.
Owner: Tech Lead + Solution Architects Lead.
Scope:
- Phase 1 (Q3 2026): Phase actor schema + базовая roadmap_graph generation в DiscoveryAgent + Onramp Process pack v0.5 (L1, L2 templates).
- Phase 2 (Q4 2026): Roadmap modification mechanics (SA edits live), forecasting v0.5.
- Phase 3 (Q1 2027): Onramp Process pack v1.0 — full templates для L3, L4 + integration с ResumptionAgent для roadmap revision при resume.
- Phase 4 (Q2 2027): Full roadmap visualization UI (graph/Gantt/calendar views) для клиентов и команды.

Это самое значимое архитектурное усиление с момента deployment topology — превращает Migration из "state machine + flags" в **полноценный navigable executable graph**, естественный для AG-парадигмы.

---

## Part 13. Что важно концептуально

1. **Migration as Actor Graph — теперь with nested Phase actors.** Это даёт настоящую AG-нативность: миграция не "state machine", а живой граф первоклассных акторов.

2. **Personalized roadmap per client.** Не один template для всех, а персонализированный actor graph, сгенерированный под клиента. Visible, editable, forecastable.

3. **10 агентов — conditional + connected to specific Phase actors.** Каждый AI-агент работает с своей назначенной Phase, не с overall Migration.

4. **Onramp Process pack — meta-Industry-Pack.** Сам процесс миграции моделируется как Industry Pack. Самореференция второго уровня.

5. **Pause is normal, не exception.** 20-30% Migrations паузируются. ResumptionAgent работает с roadmap_graph при возврате.

6. **Restart ≠ Forget.** Любой restart сохраняет prior знание.

7. **Self-hosted LLM = degraded quality.** ~85% качества vs managed Claude.

8. **Cross-pack patterns library работает кросс-deployment.**

9. **Regional + Industry Compliance Packs — family pattern.**

---

## Part 14. Что отвергнуто

(Из v1.2: data sovereignty as gap, migration reversibility, audit trail as separate concern, multi-tenancy cross-client learning, maturity index multi-dimensional, self-reference as risk, Pack Owner career path.)

---

## Part 15. Changelog v1.3 → v1.4

### Добавлено
- **Part 0:** Onramp Process pack в Core vs Context table.
- **Part 1.2:** Migration становится двухуровневой структурой с nested roadmap_graph. Высокоуровневый граф состояний Migration упрощён (детали в roadmap). Новые accumulator accounts (overall_progress_pct, forecasted_cutover_date, deviation_from_plan_days).
- **Part 1.2.1:** Phase actor — детальная структура (identity/state/accumulator accounts, events, валентности).
- **Part 1.4:** упоминание roadmap_graph как место реальной структуры (template-based).
- **Part 2:** все AI-агенты теперь привязаны к назначенным Phase actors. DiscoveryAgent дополнительно генерирует initial roadmap_graph. ResumptionAgent работает с roadmap_graph при resume.
- **Part 3.8:** Roadmap mechanics — templates per track, customization, modification, forecasting, visualization.
- **Part 5.6:** Onramp Process pack как meta-Industry-Pack.
- **Part 7.6:** Roadmap metrics.
- **Part 9:** Roadmap сборки пересчитан с Phase actor schema, Onramp Process pack, roadmap modification, forecasting, visualization milestones.
- **Part 10:** упоминание самореференции второго уровня.
- **Part 12 #11:** Migration Roadmap as architectural improvement.

### Не изменилось
- Vision (Part 1.1).
- Валентности (Part 1.3).
- Deployment topology (Part 1.5).
- AI agent specs 2.1-2.10 в основном содержании.
- Cross-cutting concerns 3.1-3.7.
- 4 трека L1-L4 (Part 4).
- Industry Packs base concept (Part 5.1-5.5).
- Compliance Packs (Part 6).
- Commercial structure 8.
- Architectural improvements #1-#10.

---

**Версия:** 1.4
**Статус:** master spec для всей команды
**Утверждение:** требуется re-sign-off от founder + tech lead + sales lead (см. Onramp Sign-off Chart v1.0 + новые items: S15 Pause/Resume, S16 Migration Roadmap as Actor Graph + Onramp Process pack).
