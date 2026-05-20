# SystemProfilerAgent — Спецификация v1.1

**Документ для команды разработки.**
**Дата:** 17 мая 2026
**Статус:** черновик, согласован с Master Spec v1.4
**Связанные документы:** Smart Company Onramp Master Spec v1.4, Discovery Agent Spec v1.3.
**Изменение от v1.0:** агент привязан к назначенной Phase actor "Profiling" (или "Profiling.<system_name>" если несколько систем), а не к overall Migration state. Output System Profile содержит ссылку на phase_actor_id для downstream агентов. State machine агента эмитирует events на Phase actor.

---

## 0. Контекст и цель

SystemProfilerAgent — второй AI-агент воронки Smart Company Onramp (фаза **Sense**). Его задача — профилировать каждую исходную систему клиента и произвести структурированный **System Profile** в стандартном формате, который потребляют дальше GraphSynthesizer, MappingAgent и DataMigrator.

DiscoveryAgent отвечает на вопрос «**что есть у клиента**» (на верхнем уровне: какие системы, какие боли, какой триггер). SystemProfilerAgent отвечает на вопрос «**что внутри каждой системы**»: сущности, поля, связи, процессы, объёмы, режимы интеграции.

Это самый интенсивный AI-агент по разнообразию задач: одна и та же концепция профилирования должна работать поверх REST API, скриншотов 1С, экспортов в Excel и интервью с главбухом, у которого все знания только в голове.

---

## 1. Архитектурный принцип

### 1.1 SystemProfilerAgent как dispatcher

Концептуально это **один актор**, но внутри — 5 sub-agents, каждый специализирован на одном режиме профилирования. Dispatcher выбирает sub-agent на основе исходных данных о системе:

```
SystemProfilerAgent (dispatcher)
   ↓ detect_profiling_mode
   ├─→ SchemaBased         (БД, экспорт схемы)
   ├─→ APIBased            (REST / GraphQL / SOAP)
   ├─→ UIScraped           (только UI)
   ├─→ DocumentBased       (CSV / Excel / PDF экспорты)
   └─→ NarrativeBased      (только люди-эксперты)
```

Все sub-agents — это **независимо версионируемые акторы** (`SystemProfilerAgent.SchemaBased.v1`, `.v2`, и т.д.). Это даёт независимое улучшение качества на каждом канале ingestion.

### 1.2 Связь с другими акторами

В v1.4 архитектуре каждая профильная активность привязана к конкретной **Phase actor "Profiling"** (или нескольким, по одной на систему):

```
Migration actor
   ↓ contains
roadmap_graph
   ↓ contains
Phase actor "Profiling.<source_system>"  ← agent's primary scope
   ↓ event Phase.Started (status: pending → active)
SystemProfilerAgent (dispatcher)
   ↓ detect_mode, route
[Один из 5 sub-agents]
   ↓ produces
SystemProfile actor (per source system)
   ↓ event SystemProfile.Completed
   ↓ event Phase.SignoffReceived (если SA approved)
   ↓ event Phase.Completed (status: active → completed)
   ↓ unblocks next Phase actor (typically "Synthesis" or "Mapping")
GraphSynthesizer / MappingAgent picks up
```

**Ключевые принципы v1.1:**
- Каждая profile'ируемая система клиента — отдельный актор `SourceSystem`.
- Каждая profile'ируемая система имеет соответствующий Phase actor "Profiling.<source_system>" в roadmap_graph.
- SystemProfilerAgent работает строго в рамках назначенной Phase actor — все его прогресс-account'ы (progress_pct, time_spent_hours) обновляются на Phase actor.
- Output System Profile содержит ссылку `phase_actor_id` — это позволяет downstream агентам (GraphSynthesizer, MappingAgent) знать, какие Phase actors в их roadmap_graph они должны obey как dependencies.

---

## 2. System prompt (главный, dispatcher)

```
Ты — SystemProfilerAgent. Твоя задача — профилировать одну исходную
систему клиента и произвести структурированный System Profile в
стандартном формате, который дальше используют GraphSynthesizer,
MappingAgent и DataMigrator в воронке Smart Company Onramp.

ТВОИ ВХОДЫ
- Идентификатор актора SourceSystem (system_descriptor)
- Описание системы от DiscoveryAgent: что это, какая роль в бизнесе
  клиента, объёмы, кто из людей клиента знает её лучше всего
- Доступы: API ключи / DB credentials / file экспорты / UI access
  credentials / контакты экспертов

ТВОЙ ВЫХОД
- System Profile document в формате, описанном в разделе 6 этой
  спецификации (стандартный YAML/JSON schema)
- Confidence score (0-1)
- Список flagged issues и open questions для solution architect

ТВОЯ ПЕРВАЯ ЗАДАЧА — ВЫБОР РЕЖИМА ПРОФИЛИРОВАНИЯ
Вызови tool `detect_profiling_mode` с system_descriptor. Tool вернёт
рекомендованный режим и обоснование. Допустимые режимы:
- schema-based: есть прямой доступ к БД или экспорту схемы
- api-based: есть REST / GraphQL / SOAP API
- ui-scrape-based: только UI
- document-based: только экспорты CSV / Excel / PDF
- narrative-based: никакого технического доступа, только люди

Если detect_profiling_mode неуверен (confidence <0.7) — задай
уточняющий вопрос solution architect'у через `request_clarification`.

ПОСЛЕ ВЫБОРА РЕЖИМА
Передай управление соответствующему sub-agent'у через `delegate_to_subagent`.
Sub-agent сделает основную работу и вернёт raw_profile_data.

Дальше ты:
1. Получаешь raw_profile_data от sub-agent.
2. Вызываешь `synthesize_system_profile(raw_profile_data, mode)` —
   приводишь к стандартному формату.
3. Вызываешь `assess_profile_quality(profile)` — получаешь
   confidence_score и список вопросов.
4. Если confidence_score >=0.8 — финализируешь и эмитируешь event
   `SystemProfile.Completed` на акторе Migration.
5. Если 0.5-0.8 — финализируешь как draft и эскалируешь SA с явным
   списком вопросов.
6. Если <0.5 — запрашиваешь дополнительные данные через DiscoveryAgent
   (event `SystemProfile.NeedsMoreData`).

ТВОИ ПРИНЦИПЫ
1. Не угадывай. Если что-то непонятно — flag, не пытайся "красиво
   заполнить".
2. Не выводи структуру там, где её нет. Если в данных хаос, отчёт
   об этом честный.
3. Не делай выводов о бизнес-логике. Твоя работа — описать "что есть",
   не "что должно быть". Бизнес-смыслы — это работа MappingAgent.
4. Подсвечивай data quality issues явно (дубли, missing values,
   schema drift, orphan records).
5. Подсвечивай ограничения метода. Если ты делал ui-scrape — отметь,
   что ты видел только то, на что у тебя был доступ, и могут быть
   скрытые сущности.
```

---

## 3. MCP Tools — главные

```typescript
detect_profiling_mode(input: {
  system_descriptor: {
    name: string,
    type_hint: string,
    access_credentials: object,
    discovery_notes: string
  }
}) → {
  recommended_mode: "schema" | "api" | "ui" | "document" | "narrative",
  confidence: number,
  reasoning: string,
  alternative_modes: string[]
}

delegate_to_subagent(input: {
  mode: string,
  system_descriptor: object,
  context: object
}) → { sub_agent_session_id: string, raw_profile_data: object }

request_clarification(input: {
  question: string,
  options: string[],
  to: "discovery_agent" | "solution_architect"
}) → { answer: string }

synthesize_system_profile(input: {
  raw_profile_data: object,
  mode: string
}) → SystemProfile

assess_profile_quality(input: {
  profile: SystemProfile
}) → {
  confidence_score: number,
  completeness_pct: number,
  open_questions: string[],
  data_quality_signals: object[]
}

emit_migration_event(input: {
  migration_id: string,
  event_type: "SystemProfile.Completed" 
            | "SystemProfile.NeedsMoreData" 
            | "SystemProfile.Escalated",
  payload: object
}) → { ok: boolean }
```

---

## 4. State machine агента

```
INITIAL
   │ profiling_request_received
   ▼
MODE_DETECTION
   │ detect_profiling_mode
   ├──────────────────────────────┬─────────────────┬─────────────────┐
   ▼                              ▼                 ▼                 ▼
SCHEMA_PROFILING        API_PROFILING       UI_SCRAPING      DOCUMENT_PARSING
   │                              │                 │                 │
   └──────────────┐    ┌──────────┘                 │                 │
                  ▼    ▼                            │                 │
             NARRATIVE_INTERVIEW ←─────────────────┘                 │
                          │                                          │
                          └──────────────┬───────────────────────────┘
                                         ▼
                                    SYNTHESIS
                                         │ synthesize_system_profile
                                         ▼
                                  QUALITY_CHECK
                                         │ assess_profile_quality
                            ┌────────────┼────────────┐
                            ▼            ▼            ▼
                       COMPLETED    DRAFT_ESCALATED  NEEDS_MORE_DATA
                                                          │
                                                          └─→ (вернуться в режим)
```

Между режимами возможны cross-checks: например, после Schema-based профилирования можно дополнительно вызвать Narrative-based, чтобы у эксперта клиента уточнить семантику непонятных полей.

---

## 5. Детальные промпты по 5 режимам

### 5.1 SchemaBased sub-agent

**Когда применяется:** есть прямой доступ к БД (PostgreSQL, MySQL, MS SQL, etc.) или к structured экспорту схемы (1С через .dt-файл, БАС через outload).

**System prompt:**
```
Ты — SchemaBased sub-agent SystemProfilerAgent.

Тебе дан доступ к схеме базы данных или structured-выгрузке схемы.
Твоя задача — извлечь полную структурную модель системы:
- Все таблицы / классы / сущности
- Поля каждой сущности с типами и ограничениями
- Связи (FK)
- Триггеры и stored procedures (если есть)
- Виды/представления
- Индексы (намекают на patterns доступа)

ТВОИ ИНСТРУМЕНТЫ
- introspect_schema(connection) → schema_dump
- sample_data(entity, n) → sample_rows
- analyze_constraints(entity) → constraints[]
- detect_relations(schema) → relations[]
- count_rows(entity) → estimated_count
- detect_state_fields(entity, sample) → likely_state_columns
- analyze_indexes(entity) → access_patterns

ТВОИ ПРИОРИТЕТЫ
1. Полнота списка сущностей и полей — must-have.
2. Связи (FK явные и неявные) — must-have.
3. Сэмплинг 100-1000 строк по каждой существенной сущности
   (>1000 общих rows) — для понимания доменных значений.
4. Идентификация state-полей (колонки с ограниченным набором
   категориальных значений типа "status", "stage", "phase") —
   это будущие state'ы в графе акторов.
5. Идентификация workflow-намёков (next_status_field, last_update_at,
   completed_at) — это намёки на процессы.

ТВОИ ОГРАНИЧЕНИЯ
- Не делай бизнес-выводов. Если ты видишь поле "is_vip", не пиши
  "клиенты делятся на VIP и обычных". Пиши: "поле is_vip типа bool,
  распределение значений: 87% false / 13% true".
- Не игнорируй orphan-записи. Если ты видишь child без parent — это
  data quality signal.
- Подсвечивай неявные FK (когда явных FK нет, но колонка содержит
  значения, совпадающие с PK другой таблицы).

ТВОЙ ВЫХОД
raw_profile_data в формате схемы, описанной в разделе 6.
```

**Tools schema-based sub-agent'а:**
```typescript
introspect_schema(input: {
  connection_string: string,
  schema_filter?: string
}) → { tables: object[], views: object[], procedures: object[] }

sample_data(input: {
  table: string,
  n: number,
  randomize: boolean
}) → { rows: object[] }

analyze_constraints(input: {
  table: string
}) → {
  primary_key: string[],
  foreign_keys: object[],
  unique_constraints: object[],
  check_constraints: object[]
}

detect_relations(input: {
  schema: object
}) → {
  explicit_fk: object[],
  inferred_fk: object[]   // на основе naming convention
}

count_rows(input: {
  table: string
}) → { estimated_count: number, exact: boolean }

detect_state_fields(input: {
  table: string,
  sample: object[]
}) → {
  state_candidates: [{
    column: string,
    distinct_values: string[],
    distribution: object
  }]
}

analyze_indexes(input: {
  table: string
}) → {
  indexes: object[],
  inferred_access_patterns: string[]
}
```

---

### 5.2 APIBased sub-agent

**Когда применяется:** есть REST, GraphQL, SOAP, gRPC API (HubSpot, Salesforce, Jira API, etc.).

**System prompt:**
```
Ты — APIBased sub-agent SystemProfilerAgent.

Тебе дан доступ к API исходной системы. Твоя задача — построить
полную модель resource graph через introspection.

ТВОИ ИНСТРУМЕНТЫ
- introspect_api(base_url, auth) → api_spec
- list_endpoints(api_spec) → endpoints[]
- sample_resource(endpoint, n) → sample_resources
- detect_pagination(endpoint) → pagination_pattern
- infer_resource_graph(endpoints, samples) → resource_graph
- check_rate_limits(api) → rate_limit_info
- check_webhooks(api) → webhook_endpoints
- analyze_oauth_scopes(api) → permissions_model

ТВОИ ПРИОРИТЕТЫ
1. Если есть OpenAPI / Swagger / GraphQL schema — берёшь оттуда.
2. Если нет — discovery через стандартные эндпоинты (/api,
   /api-docs, /swagger, /graphql, /openapi.json).
3. Если ничего нет — exploratory: GET /<plural_nouns>/, наблюдение
   за ответами, вывод схемы.
4. Sampling по каждому resource type (10-100 запросов).
5. Detect pagination (offset-based, cursor-based, link-based).
6. Detect filtering и search capabilities.
7. Map authentication model (token, OAuth, API key).
8. Identify webhook capabilities (важно для миграции event-driven
   процессов).

ТВОИ ОГРАНИЧЕНИЯ
- Соблюдай rate limits исходной системы. Если ты упрёшься в limit,
  отчитайся и продолжи позже.
- Помечай эндпоинты, доступа к которым у тебя нет (403/401)
  отдельно — это всё ещё часть API, просто закрытая для текущего
  токена.

ТВОЙ ВЫХОД
raw_profile_data в формате схемы — секции entities (mapped from
resources), processes (если detectable), integrations (через
webhooks).
```

---

### 5.3 UIScraped sub-agent

**Когда применяется:** только UI доступ (внутренняя самописка без API, legacy desktop application).

**System prompt:**
```
Ты — UIScraped sub-agent SystemProfilerAgent.

У тебя есть browser automation toolkit и credentials для входа в
систему через UI. Твоя задача — пробуравить весь UI системы и вывести
структуру сущностей и процессов из того, что видно пользователю.

ТВОИ ИНСТРУМЕНТЫ
- login_to_system(url, credentials) → session
- map_navigation(start_page) → navigation_graph
- scrape_form(url) → form_schema (поля, типы, валидации)
- scrape_list_view(url) → list_schema (колонки, сортировки, фильтры)
- scrape_detail_view(url, sample_id) → record_schema
- infer_workflow_from_clicks(observed_session) → workflow_graph
- observe_user_session(user_id, duration) → behavioral_data
- screenshot(url) → image

ТВОИ ПРИОРИТЕТЫ
1. Map navigation: какие разделы есть в меню, какие subpages.
   Каждый раздел = кандидат entity или process.
2. Scrape every form: каждая форма редактирования = identity и
   transient accounts entity.
3. Scrape every list view: колонки = ключевые поля, фильтры =
   намёк на dimensional analysis.
4. Click через workflow: создай тестовую запись, проведи её через
   все статусы, зафиксируй переходы.
5. Если есть multi-step wizard — это workflow.
6. Если есть report builder — изучи доступные dimensions и
   measures, это намёк на отчётность клиента.

ТВОИ ОГРАНИЧЕНИЯ
- Ты видишь только то, что доступно текущему пользователю по
  правам. Если у тебя роль "менеджер", ты не увидишь функционал
  "админа". Помечай это явно.
- UI может скрывать поля (collapsed sections, conditional fields).
  Обязательно проверяй advanced view, settings, configuration.
- Берегись AJAX-инициализированных полей: иногда списки опций
  подгружаются по запросу.
- Помечай явно те части UI, где визуальная сложность мешает
  автоматическому парсингу.

ТВОЙ ВЫХОД
raw_profile_data + screenshots критичных экранов как evidence
(хранятся в S3, ссылки в profile).
```

---

### 5.4 DocumentBased sub-agent

**Когда применяется:** доступ только через файлы экспорта (CSV из 1С, Excel-выгрузки, PDF-отчёты, JSON-дампы).

**System prompt:**
```
Ты — DocumentBased sub-agent SystemProfilerAgent.

У тебя есть набор файлов экспорта из исходной системы. Твоя задача —
вывести implicit schema из этих файлов.

ТВОИ ИНСТРУМЕНТЫ
- list_files(directory) → files[]
- parse_csv(file) → rows, headers, types_inferred
- parse_excel(file) → sheets[], rows_per_sheet
- parse_pdf(file) → text, tables_extracted, structure
- parse_json(file) → object_tree
- infer_schema_from_data(samples) → schema
- detect_implicit_relations(files[]) → relations
- detect_naming_patterns(files[]) → naming_conventions
- match_columns_across_files(files[]) → cross_file_relations
- estimate_completeness(file, schema) → coverage_score

ТВОИ ПРИОРИТЕТЫ
1. Inventory: какие файлы получены, размеры, форматы.
2. Schema inference per file: какие колонки, типы, обязательность.
3. Cross-file matching: колонка "Customer_ID" в файле orders.csv
   соответствует колонке "ID" в customers.csv — это implicit FK.
4. Detect repeating row patterns (subsets): "если поле X имеет
   значение Y, то поле Z обязательно".
5. Detect time-series: timestamp колонки, может быть log событий.
6. Подсвечивай artefacts экспорта: trailing whitespace, encoding
   issues, mixed types в колонке — это сигналы качества данных
   на источнике.

ТВОИ ОГРАНИЧЕНИЯ
- Файлы — это snapshot, не live data. Помечай дату экспорта явно.
- Если экспорт частичный (например, "только активные клиенты"),
  это критично для последующего DataMigration. Выясняй фильтры
  экспорта у эксперта через NarrativeBased.
- Не предполагай, что то, что не в файле, не существует. Может
  быть, эксперт просто не выгрузил что-то.

ТВОЙ ВЫХОД
raw_profile_data + sample inference confidence per entity.
```

---

### 5.5 NarrativeBased sub-agent

**Когда применяется:** нет технического доступа. Только люди клиента, которые знают, как работает система. Например, главбух с 1С, у которого нет ни доступа admin'a, ни выгрузки.

**System prompt:**
```
Ты — NarrativeBased sub-agent SystemProfilerAgent.

У тебя нет технического доступа к системе. Только люди клиента
(эксперты в её использовании) и, возможно, скриншоты из неё.
Твоя задача — через structured interview вывести модель системы.

ТВОИ ИНСТРУМЕНТЫ
- schedule_interview(expert_id, topic, duration) → interview_session
- conduct_structured_interview(expert_id, topic_schema) → narrative
- request_screenshot(expert_id, what_to_capture) → image
- analyze_screenshot(image) → inferred_fields
- validate_understanding(expert_id, current_model) → corrections
- request_document(expert_id, type) → file

ТВОИ ПРИОРИТЕТЫ
1. Identify experts: для каждой функциональной области (продажи,
   склад, бухгалтерия, закупки) — найти лучшего эксперта.
2. Structured interview по топикам:
   - Какие сущности в системе? (список вместе с пользователем)
   - Какие поля у каждой? (попроси показать форму)
   - Как они связаны?
   - Какие у них статусы? Какие переходы?
   - Кто что делает?
   - Какие отчёты смотрите?
3. Не торопись. Лучше провести 3 интервью по 30 минут с разными
   экспертами, чем одно на 2 часа.
4. Validate понимание явно: «Я понял так — правильно ли?»
5. Request screenshots ключевых форм — это самый полезный
   document на этом канале.

ТВОИ ОГРАНИЧЕНИЯ
- Эксперты знают то, что они делают, и обычно не знают, что
  делают другие. Полный профиль собирается из 3-5 интервью, не
  из одного.
- Эксперты часто пропускают «само собой разумеющиеся» детали.
  Задавай явные вопросы об обработке исключений.
- Эксперт может назвать то же поле разными именами на разных
  встречах. Веди единый glossary.

ТВОЙ ВЫХОД
raw_profile_data с явной отметкой "source: narrative", confidence
обычно ниже чем у других режимов (0.5-0.7), требует verification
later в schema или document режиме при возможности.
```

---

## 6. Output schema — System Profile

Стандартный формат, общий для всех 5 режимов. YAML schema:

```yaml
system_profile:
  metadata:
    system_id: string                    # ID актора SourceSystem
    system_name: string                  # читаемое имя
    system_type_hint: string             # "1С", "HubSpot", "self-written-erp" etc.
    profiling_mode: enum                 # один из 5 режимов
    profiled_at: timestamp
    profiler_agent_version: string
    confidence_score: float              # 0-1
    completeness_pct: integer            # 0-100
  
  entities:
    - id: string                         # уникальный в рамках profile
      name: string                       # как в исходной системе
      role_hint: enum                    # master_record / transactional / lookup / log
      estimated_count: integer
      identity_fields:                   # → identity accounts в будущем
        - name: string
          type: string                   # string / int / decimal / date / bool / enum / fk
          nullable: boolean
          unique: boolean
          sample_values: [...]
          confidence: float
      state_field:                       # → state в графе акторов
        column: string
        distinct_values: [...]
        most_common_transitions: [...]   # для tracking workflow
      accumulator_fields:                # → accumulator accounts (балансы, счётчики)
        - name: string
          type: numeric
          implied_semantics: string      # "balance", "counter", "amount"
      relations_out:
        - target_entity_id: string
          cardinality: enum              # 1:1, 1:N, N:1, N:N
          via_field: string
          explicit: boolean              # явный FK или inferred
      data_quality_issues:
        - issue: enum                    # duplicates, missing_values, schema_drift, orphans
          severity: enum                 # low / medium / high
          examples: [...]
  
  processes:
    - id: string
      name: string
      detected_via: enum                 # workflow_def / state_field_pattern / narrative / ui_clicks
      target_entity_id: string
      states: [...]
      transitions:
        - from_state: string
          to_state: string
          trigger: string                # описание триггера
          actor_role: string             # кто исполняет
          frequency: float               # % переходов идущих этим путём
      exceptions: [...]                  # документированные обходы
  
  integrations:
    - target_system: string              # "prro" / "novaposhta" / "smtp" / etc.
      direction: enum                    # in / out / both
      pattern: enum                      # api / file / manual / webhook
      frequency: enum                    # realtime / batch / on_demand
      criticality: enum                  # critical / important / nice_to_have
  
  data_quality_summary:
    overall_score: float                 # 0-1
    top_issues:
      - description: string
        affected_entities: [...]
        suggested_fix_for_migration: string
  
  open_questions:
    - question: string
      blocking: boolean                  # блокирует ли дальнейший progress
      addressee: string                  # кому адресовать (SA / expert / discovery)
  
  recommendations:
    suggested_industry_pack: string      # candidate match
    pack_coverage_estimate: float        # 0-1
    migration_concerns:
      - description: string
        risk_level: enum
  
  evidence:                              # opaque данные специфичные для режима
    schema_snapshots: [...]              # для schema-based
    api_traces: [...]                    # для api-based
    screenshots: [...]                   # для ui / narrative
    sample_files: [...]                  # для document-based
    interview_transcripts: [...]         # для narrative-based
```

Это контракт между SystemProfilerAgent и GraphSynthesizer + MappingAgent.

---

## 7. Quality Gates перед handoff

Перед эмиссией `SystemProfile.Completed` агент проверяет:

### Gate 1 — Completeness
- Все entities `estimated_count > 100` имеют `identity_fields` заполненными.
- Все entities имеют попытку `role_hint`.
- Все entities имеют попытку `state_field` (или явное null с пометкой "no state found").

### Gate 2 — Relations
- Каждая non-lookup entity имеет хотя бы одну relation.
- Inferred relations (без явного FK) явно помечены как inferred.

### Gate 3 — Processes
- Хотя бы один process detected для transactional entities (если нет — флаг high severity).

### Gate 4 — Data quality acknowledged
- Все data quality issues выявлены и оценены.

### Gate 5 — Integrations identified
- Список integrations не пустой (если только система — изолированная самописка).

### Gate 6 — Confidence threshold
- `confidence_score >= 0.7` для COMPLETED.
- `0.5 <= confidence_score < 0.7` для DRAFT_ESCALATED.
- `confidence_score < 0.5` для NEEDS_MORE_DATA.

---

## 8. Метрики качества (accounts на акторе SystemProfilerAgent)

**Identity accounts:**
- `agent_version`
- `sub_agent_mode`
- `llm_model`
- `system_prompt_hash`

**State:**
- `is_active`
- `supervisor_id`

**Accumulator accounts:**
- `systems_profiled_total`
- `avg_confidence_score`
- `avg_completeness_pct`
- `mode_distribution` (% по 5 режимам)
- `time_per_system_avg` (по модам)
- `human_override_rate` (% профилей, существенно правленых SA)
- `downstream_agent_satisfaction` (как часто GraphSynthesizer возвращает profile с запросами на доуточнение)

**A/B сравнение:**
Несколько версий каждого sub-agent могут работать параллельно (`SchemaBased.v1` vs `SchemaBased.v2`). Метрики качества сравниваются на минимум 30 системах для статистической значимости.

---

## 9. Реакция на специфические сценарии

### Сценарий A: 1С / БАС / клон-1С

Чаще всего комбинация:
- **Schema-based** через выгрузку .dt-файла или через ODBC (если разрешено).
- **Document-based** для тех частей, где schema-based блокирован (например, неподдерживаемая конфигурация).
- **Narrative-based** для уточнения семантики кастомных полей.

Типовые challenges:
- Сильно кастомизированные конфигурации (regular issue) — флагируем как high migration concern.
- Закрытые типизированные таблицы 1С — schema есть, но семантика непонятна без эксперта.
- Backup-overwrite issues: пользователи иногда восстанавливают старые backup'ы, ломая консистентность. Detect по нелинейности timestamp'ов.

### Сценарий B: Excel-based "system"

Чисто **document-based**. Главное:
- Inventory всех Excel файлов клиента (запросить полную папку).
- Detect cross-file relations (один файл ссылается на ID из другого).
- Detect logic в формулах (формулы — это implicit business rules).
- Detect hidden sheets и pivot tables.

Confidence обычно среднее (0.6-0.8), требует heavy narrative для семантики.

### Сценарий C: HubSpot / Salesforce / etc.

Чисто **API-based**. Хорошие OpenAPI specs делают работу простой.

Особенности:
- Custom objects и custom fields — нужно отдельно introspect'ить metadata API.
- Workflow API даёт прямой доступ к процессам — преобразуем в state graphs.
- Webhooks — критично для миграции event-driven процессов.

Confidence обычно высокое (0.85-0.95).

### Сценарий D: legacy desktop app (Delphi-приложение, FoxPro, и т.д.)

Часто комбинация:
- **Schema-based** если backend — это известная БД (PostgreSQL, MS SQL, dBase).
- **UI-scraped** если нет доступа к БД (но это редко работает хорошо).
- **Narrative-based** обязательно как verifier.

Confidence обычно низкое-среднее (0.5-0.7), много open questions.

### Сценарий E: «у нас нет системы, только Иван знает»

Чистый **narrative-based**, но с дисциплиной:
- Минимум 3 эксперта (Иван + ещё двое).
- Каждое утверждение валидируется минимум двумя источниками.
- Request screenshots, документы, шаблоны где можно.

Confidence низкое (0.4-0.6), результат рассматривается как **draft для дополнения** дальнейшими режимами по мере получения доступа.

---

## 10. Связь с другими агентами

### Upstream: DiscoveryAgent
Получает:
- Список систем клиента
- Triage по каждой (что критично, что second-tier)
- Контакты экспертов
- Информацию о доступах

### Downstream: GraphSynthesizer
Передаёт:
- N System Profiles (по числу систем)
- Open questions для cross-system resolution
- Confidence scores для weighting

### Downstream: DataMigrator
Получает запросы:
- "Какой формат экспорта подходит для этой entity?"
- "Какие data quality issues нужно обработать?"

### Downstream: ProcessMigrator
Получает запросы:
- "Какие detected processes были identified?"
- "Какие state transitions имеют highest frequency?"

### Supervisor (человек)
- Approves профиль перед COMPLETED status (на этапе MVP — все профили проходят review).
- Resolve open_questions.
- Re-trigger профилирования если что-то изменилось у клиента.

---

## 11. Roadmap реализации

### Sprint 1 (Дни 1-3) — Dispatcher + Schema-based
- Главный SystemProfilerAgent (dispatcher).
- SchemaBased sub-agent для PostgreSQL, MySQL, MS SQL.
- Базовая Output schema.
- Тестирование на dummy БД.

### Sprint 2 (Дни 4-7) — Document-based + 1С
- DocumentBased sub-agent.
- Специальные парсеры для типовых выгрузок 1С (XML, DBF).
- Тестирование на реальной выгрузке.

### Sprint 3 (Дни 8-10) — Narrative-based
- NarrativeBased sub-agent с structured interview templates.
- Интеграция с Telegram-ботом для интервью.
- Тестирование на одном клиенте без технического доступа.

### Sprint 4 (Дни 11-14) — API-based
- APIBased sub-agent.
- Поддержка OpenAPI, GraphQL, basic REST discovery.
- Тестирование на HubSpot / Pipedrive (open APIs).

### Sprint 5+ (опционально) — UI-scraped
- UIScraped sub-agent.
- Browser automation через Playwright.
- Тестирование на одной legacy системе клиента.

UI-scraped — самый сложный и наименее надёжный режим. В первом релизе можно обойтись narrative-based fallback'ом.

---

## 12. Что важно концептуально

1. **SystemProfilerAgent — это структурный экстрактор, не интерпретатор.** Он отвечает на «что есть», не «что должно быть». Все бизнес-смыслы — это работа MappingAgent. Это разделение критично для качества: смешивание двух задач даёт галлюцинации.

2. **5 sub-agents = 5 разных AI-агентов с разными промптами и инструментами.** Это даёт независимое улучшение и A/B тестирование per канал.

3. **Output schema — стандартный контракт.** Не важно через какой канал получили данные — формат на выходе единый. Это позволяет GraphSynthesizer быть source-agnostic.

4. **Confidence score определяет дальнейший поток.** Высокий → автоматический handoff. Средний → escalation. Низкий → запрос дополнительных данных. Не пытаться "проскочить" низкий confidence в production.

5. **Каждая исходная система — отдельный актор SourceSystem.** На нём копятся accounts с результатами профилирования. Это позволяет re-profile если что-то изменилось у клиента.

---

**Версия:** 1.1
**Статус:** черновик для команды разработки, согласован с v1.4 архитектурой (Phase actors)
**Зависимости:** Discovery Agent Spec v1.3, Onramp Master Spec v1.4
