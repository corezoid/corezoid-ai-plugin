# Golden House — Полная симуляция пути через Onramp

**Документ:** narrative simulation v1.1
**Дата:** 17 мая 2026
**Клиент:** сеть мебельных салонов Golden House (Днепр), контакт — Надежда Сокол
**Обновление от v1.0:** добавлен раздел «v1.4 lens» — пересмотр симуляции через призму Phase actors и roadmap_graph (Master Spec v1.4).

---

## 0. Призма v1.4 — это симуляция через Phase actors

Эта симуляция была написана под архитектуру v1.0-1.3, где Migration имел один state machine. В v1.4 архитектуре каждая «фаза» из этого narrative — это **самостоятельный Phase actor** в roadmap_graph актора Migration.

**Маппинг narrative → Phase actors:**

| Раздел narrative | Phase actor в roadmap_graph | Owner agent | Signoffs |
|---|---|---|---|
| Day 0 — DiscoveryAgent | `Phase.Discovery` | DiscoveryAgent | product_owner |
| Week 1 — System Profiling | `Phase.Profiling.GoldenHouseSystem` | SystemProfilerAgent | SA |
| Week 2 — Synthesis + Mapping | `Phase.Synthesis` (degraded для L2) + `Phase.Mapping` | GraphSynthesizer + MappingAgent | SA |
| Weeks 3-4 — Data Migration | `Phase.DataMigration` | DataMigrator | главбух (Олена) + owner (Сергей) |
| Weeks 4-5 — Process Migration | `Phase.ProcessMigration` | ProcessMigrator | SA |
| Weeks 5-7 — Validation | `Phase.Validation` | ValidationAgent | (gates Cutover automatically) |
| Week 8 — Cutover | `Phase.Cutover` | CutoverAgent | Надежда + Никита |
| Weeks 9-12 — Operate | `Phase.Operate` | (transition phase, no AI agent) | — |
| Months 3-12 — Evolution | `Phase.Evolve` (continuous) | EvolutionAgent | per milestone |

**В Day 0 (Discovery) после Этапа 5 случается главное событие v1.4:** DiscoveryAgent вызывает `generate_roadmap_graph(lead_id, "L2", "cloud", "2026-07-07")` → создаётся актор `Migration.GoldenHouse` + roadmap_graph_id + 9 Phase actors. Надежда видит свой персональный 9-фазный roadmap в визуализации (как диаграмма из chat).

**Каждое event в narrative ниже** (например, «`Migration.discovery_completeness_pct = 100`») — теперь это event на конкретном Phase actor (`Phase.Discovery.Completed`), а overall Migration accounts (overall_progress_pct, forecasted_cutover_date) обновляются как агрегаты от Phase actors.

**Pause/Resume scenarios.** В narrative нет пауз — Golden House проходит straight-through. Но в реальности после Day 30 (середина параллельного run) Надежда могла бы взять паузу на сезонный аудит, и ResumptionAgent на Day 35 предложил бы 4 опции с freshness assessment. Roadmap_graph бы пережил паузу — Phase actors сохраняют свой статус.

**Что эта симуляция всё ещё даёт правильно (несмотря на pre-v1.4 нотацию):**
- 8-недельный timeline для L2 (валидируется в v1.4 roadmap)
- 7 выявленных gap'ов в master spec (закрыты в v1.1-v1.4)
- Конкретные signoff chains (формализованы в v1.4 как Phase signoffs_required)
- Cold-start EvolutionAgent (формализовано в v1.3)
- Validation discrepancy classification (формализовано в v1.1)

Остальная часть документа сохраняет original narrative (для preserved historical context), но читать её следует через эту призму.

---
**Цель документа:** stress-test всех 9 AI-агентов на реальном кейсе. Показать, как master spec работает end-to-end. Используется как:
- Внутренняя документация команды («так это будет в жизни»)
- Sales-демо («вот реальный пример пути клиента»)
- Источник для рефакторинга спецификаций («что мы упустили»)

---

## Контекст до начала

Golden House уже была в контакте с Никитой (мая 14, 2026) — классический pre-Onramp флоу. Этот документ симулирует **альтернативную реальность**: что если бы Надежда зашла на simulator.company сегодня и нажала кнопку **«START FREE»**? Какой путь её провёл бы Onramp.

Все события и метрики ниже — это event'ы на акторе `Migration_GoldenHouse` в Simulator. Они реальные с точки зрения архитектуры: после запуска Onramp такая трассировка будет автоматической.

Track classification: **L2 (Single replacement)** — заменить самописку.

---

## Day 0 — First contact (DiscoveryAgent)

**Время:** 14:23, понедельник.
**Trigger:** Надежда вводит в Google «как заменить 1С на украинский продукт», находит блог Simulator, открывает лендинг simulator.company, нажимает **START FREE**.

### 14:23 — Lead.Created

Открывается виджет DiscoveryAgent на лендинге. Создаётся актор `Lead.GoldenHouse_2026_05_17_14_23` со starter accounts:
- `language` = uk (определён по User-Agent + первой геолокации)
- `source` = web_widget
- `discovery_started_at` = 14:23

### 14:24 — Greeting

> **Агент:** Добрый день! Я — Discovery-консультант компании Simulator. За ближайший час я разберусь в вашем бизнесе настолько, чтобы понять, подойдёт ли вам наша платформа для замены текущей учётной системы. На выходе у вас будет персональный демо-стенд с вашими данными. С чего начнём — расскажите немного о вашей компании?

> **Надежда:** Я представитель сети мебельных салонов Golden House в Днепре. Нам нужно заменить нашу самописную систему учёта до 2027-2028 года из-за введения НДС и требований для ФОП.

### 14:25-14:40 — Этап 1: Profile

Агент задаёт серию вопросов, по результатам заполняет identity accounts:

| Account | Значение |
|---|---|
| `company_name` | "Golden House" |
| `industry` | "furniture_retail" |
| `outlets_count` | 4 |
| `employees_count` | 28 |
| `company_age_years` | 9 |
| `current_system` | "самописка" |
| `current_system_version` | "PHP/MySQL, 2018 года" |
| `current_system_pain` | "не выдержит НДС, нет API, нет мобильной версии" |
| `migration_trigger` | "ндс_фоп_2027" |
| `target_deadline` | "2027-01-01" |
| `assortment_type` | "смешанный (готовая + на заказ)" |

Event на акторе Lead: `Phase.Profile.Completed`, state → `BUSINESS_FLOW`.

### 14:41-15:10 — Этап 2: Business flow

> **Агент:** Теперь самое важное — давайте разберём, как устроена одна типичная сделка. Расскажите по шагам: клиент пришёл в салон, что происходит дальше?

Надежда рассказывает 30-минутный нарратив. Агент в реальном времени:
- Задаёт уточняющие вопросы по 8 кейсам Golden House (раздел 5 спецификации).
- Параллельный Corezoid-процесс собирает actor graph из её слов.
- В конце показывает её сделке ASCII-граф:

```
NEW → PREPAID → SUPPLIER_NOTIFIED → IN_PRODUCTION → READY → 
  → FULLY_PAID → AT_WAREHOUSE → SHIPPED → DELIVERED → CLOSED
```

> **Агент:** Вот как я понял ваш типичный заказ. Где я ошибся?

> **Надежда:** Кажется, всё. Только ещё есть сборка после доставки — для некоторых заказов.

Агент добавляет в граф `INSTALLED` state и фиксирует:

| Account | Значение |
|---|---|
| `case_1_retail_flow` | "клиент в салоне, менеджер оформляет, актор Order связан с Customer/Showroom/Employee" |
| `case_2_prepayment_flow` | "30-50% предоплаты, ПРРО прямо в салоне, провайдер Cash Online" |
| `case_3_vendor_flow` | "email/телефон поставщику после предоплаты, поставщик присылает цену и срок ~30 дней" |
| `case_4_lead_time_flow` | "менеджер вручную следит, нет автоматических уведомлений" |
| `case_5_final_payment_flow` | "доплата перед прибытием товара, тоже ПРРО" |
| `case_6_logistics_flow` | "наёмный транспорт, от поставщика на склад, потом клиенту, иногда сразу клиенту" |
| `case_7_closing_flow` | "финальный пакет документов закрывает сделку" |
| `case_8_returns_flow` | "редко (3-5% сделок), в основном брак фабрики" |

Также вызывается `match_industry_pack("furniture_retail", ...)`:
- `pack_actor_id` = "furniture_retail_v1"
- `coverage_pct` = 92%
- `delta_required` = ["сборка/установка как опция", "доставка от поставщика клиенту напрямую"]

Event: `Phase.Flow.Completed`, state → `VOLUMES_INFRA`.

### 15:11-15:21 — Этап 3: Volumes & infra

| Account | Значение |
|---|---|
| `orders_per_month` | 60-80 |
| `contractors_count` | ~4500 (с дублями), активных ~1800 |
| `vendors_count` | 18 |
| `sku_count` | ~3200 |
| `concurrent_users` | 12 |
| `prro_provider` | "Cash Online" |
| `banks` | ["Приват", "Моно"] |
| `logistics_providers` | ["Своя машина", "Нова Пошта Карго"] |
| `it_ownership` | "внешний разработчик самописки, ~10 ч/мес" |
| `db_export_available` | true (CSV / MySQL dump) |

Event: `Phase.Volumes.Completed`, state → `SPECIFICS`.

### 15:22-15:40 — Этап 4: Specifics

> **Агент:** Что в текущей системе вы НЕ хотите потерять?

> **Надежда:** Фильтрация заказов по поставщикам и стадиям — это критично. И ещё прайс розница/опт на каждом товаре.

> **Агент:** Что вас в текущей системе бесит больше всего?

> **Надежда:** ... [пауза 8 секунд] ... Что я не вижу, где конкретно зависает заказ. Менеджер говорит "поставщик задерживает", я не могу проверить.

| Account | Значение |
|---|---|
| `must_keep` | ["фильтрация по поставщикам", "фильтрация по стадиям", "прайс розница/опт"] |
| `top_pain` | "нет прозрачности где зависает заказ" |
| `owner_reports` | ["воронка по этапам", "выручка по салонам", "просрочки по поставщикам"] |
| `internal_owner` | "Надежда (operations director)" |
| `decision_maker` | "владелец (Сергей К.)" |
| `budget_range` | "до 5000 EUR/мес" |
| `previous_attempts` | ["пытались BAS год назад, не пошло — слишком похоже на 1С"] |
| `success_criteria` | "через год менеджеры работают только в новой системе, владелец видит P&L в реальном времени" |
| `escalation_signals` | пусто |

Event: `Phase.Specifics.Completed`, state → `PRESENTATION`.

### 15:41-15:55 — Этап 5: Presentation

Агент собирает финальный пакет:

`classify_track(lead_id)` →
- track = **standard** (L2)
- reasoning: «multi-salon (>1, <5 outlets), длинная цепочка с поставщиками, требует параллельной работы 2-3 недели, но <30 сотрудников и есть internal IT owner»
- estimated_weeks = [6, 8]
- estimated_team_size = 3

`create_prototype_stand(lead_id, "furniture_retail_v1", {company_name: "Golden House", ...})` →
- prototype_actor_id = "Prototype.GoldenHouse_2026_05_17"
- stand_url = "https://demo.simulator.company/p/golden-house-aH3kf"
- expires_at = 2026-05-31

`generate_discovery_brief(lead_id)` →
- brief_url, brief_md, commercial_offer_url, tom_blueprint_url

> **Агент:** Спасибо за подробный разговор. Вот что я понял о вашей компании:
> - Сеть из 4 мебельных салонов в Днепре
> - 28 сотрудников, 60-80 заказов/мес, 18 поставщиков
> - Самописка не выдержит НДС 2027
> - Главная боль — нет прозрачности где зависает заказ
>
> Я отношу вас к треку Standard — это срок 6-8 недель и команда 3 человека.
>
> Вот ссылка на ваш персональный демо-стенд: [stand_url] — там вы можете посмотреть, как будет выглядеть один заказ от обращения до закрытия, заполненный тестовыми данными в духе вашего бизнеса.
>
> Что бы вы хотели — назначить финальный звонок с solution architect, чтобы обсудить детали?

> **Надежда:** Да, очень хочу. И ссылку открыла прямо сейчас.

`emit_lead_event("Brief.Generated", ...)`. State Lead → `AWAITING_HUMAN_HANDOFF`. Telegram-уведомление solution architect Никите.

### 15:56 — End of Day 0

- Длительность Discovery: 90 минут.
- Lead-актор полностью заполнен.
- Prototype-стенд активен 14 дней.
- Brief PDF + TOM blueprint в формате Golden House full document сгенерированы и отправлены.
- Никите назначена встреча с Надеждой на следующий день, 16:00.

---

## Day 1 — Handoff to Solution Architect

### 16:00-16:45 — Финальный звонок Никиты с Надеждой

Никита открывает Brief и TOM blueprint. За 5 минут понимает контекст (Discovery собрал всё). Звонок — это:
- Подтверждение деталей с Надеждой и Сергеем (владелец на звонке).
- Уточнение бюджета и сроков.
- Согласование начала Onramp с **переходом Lead → Migration**.

В конце звонка:
- `emit_lead_event("Brief.Signed", ...)`.
- Создаётся актор `Migration.GoldenHouse` со starter accounts:
  - `client_name = "Golden House"`
  - `track = "L2"`
  - `start_date = 2026-05-19`
  - `target_cutover_date = 2026-07-07` (8 недель)
  - `industry = "furniture_retail"`
- State Migration → `DISCOVER` (формально, на самом деле уже завершён).
- Event `Migration.Started`.
- Lead связывается с Migration в графе.

---

## Week 1 — System Profiling (SystemProfilerAgent)

### Day 2-3 — Mode detection

`SystemProfilerAgent` запускается на самописке Golden House.

`detect_profiling_mode`:
- access: «есть MySQL dump + CSV экспорты + UI access + контакт внешнего разработчика»
- recommended_mode = **schema-based** (primary)
- secondary modes = document-based (для cross-check) + narrative-based (для семантики)

Делегируется `SystemProfilerAgent.SchemaBased.v1`.

### Day 3-4 — Schema introspection

Sub-agent подключается к MySQL dump (Никита получил его от внешнего разработчика).

Tools работают:
- `introspect_schema` → 43 таблицы.
- `analyze_constraints` → 38 явных FK, 12 inferred FK (по naming convention).
- `count_rows` → топ-таблицы: `orders` (4280 строк), `order_items` (12450), `clients` (4521, с дублями!), `vendors` (18), `products` (3140).
- `detect_state_fields` → находит `orders.status` с 8 distinct values: NEW, PAID, SENT_TO_VENDOR, IN_PRODUCTION, READY, FULLY_PAID, SHIPPED, CLOSED.
- `sample_data` → достаются 200 строк по каждой существенной таблице.

`data_quality_signals` от sub-agent:
- В `clients` найдено ~700 вероятных дублей (один человек под разными именами).
- В `order_items` есть 47 orphan-записей (FK на удалённые orders).
- Поле `vendor_id` в `orders` иногда null, хотя по логике должно быть всегда заполнено для PAID+ статусов.
- 12 заказов имеют логически невозможный flow (state перескочил через несколько).

### Day 4 — Document cross-check

Параллельно `DocumentBased` sub-agent смотрит CSV-выгрузки клиентов, заказов, поставщиков.
- Конфликтов с schema-based нет.
- Дополнительно обнаружено: в CSV есть колонка `extra_notes`, которой нет в schema. Outlies! Это значит самописка хранит часть данных где-то ещё (вероятно, отдельные JSON-поля или log файлы).

### Day 5 — Narrative validation

`NarrativeBased` sub-agent проводит structured interview с внешним разработчиком (60 минут).
- Подтверждение: семантика 90% полей корректная.
- Уточнение: дубли в `clients` — это потому что менеджеры не используют поиск, создают нового клиента при каждой сделке.
- Уточнение: `extra_notes` хранятся в файловой системе как JSON, ключ = `order_id`. Это критично для migration — нужно учесть.
- Открытое уточнение: разработчик не помнит, что такое поле `clients.legacy_flag`. Помечается как open question.

### Day 6 — Synthesis

`synthesize_system_profile`:
- 23 значимых entities (после слияния lookup-табличек).
- Полная карта relations.
- Главный workflow (Order) с 11 states.
- 5 интеграций: ПРРО Cash Online (file-based), Приват24 (file-based выписки), Нова Пошта (email-only), email клиенту (SMTP), Telegram-уведомления менеджерам (через bot).

`assess_profile_quality`:
- `confidence_score` = 0.83 (good)
- `completeness_pct` = 91%
- `open_questions` = 3 (включая `legacy_flag`)

### Day 6 — Handoff

Event `SystemProfile.Completed` на акторе Migration.
Account `Migration.systems_profiled_count` = 1.
Account `Migration.discovery_completeness_pct` = 100% (только одна система).

System Profile передаётся в GraphSynthesizer.

---

## Week 2 — Synthesis + Mapping (GraphSynthesizer + MappingAgent)

### Day 8-9 — GraphSynthesizer

Tools работают:
- `match_entities` — для L2 (одна система) дублей между источниками нет, но есть совпадения с Furniture Retail Industry Pack:
  - System.clients → Pack.Customer (94% match)
  - System.orders → Pack.Order (97% match)
  - System.order_items → Pack.OrderLine (95% match)
  - System.vendors → Pack.Vendor (98% match)
  - System.products → Pack.Product (92% match)
- `detect_gaps` относительно Industry Pack:
  - В Golden House самописке **нет** актора SubOrder. Один Order — один Vendor.
  - При этом по бизнес-логике должна быть возможность multi-vendor заказа (Надежда упоминала). Это gap! Текущая система это не моделирует.
- `identify_conflicts` — нет.

`synthesize_actor_graph`:
- Берётся Furniture Retail Pack как basis.
- Накладываются обнаруженные особенности Golden House.
- Predicted delta:
  - Добавить SubOrder где сейчас в системе всё лежит в Order.vendor_id.
  - Логика установки/сборки — отдельный актор InstallationTask.

Account `Migration.actor_graph_synthesized_pct` = 100%.
Draft Actor Graph готов.

### Day 10-12 — MappingAgent

Tools работают:
- `propose_field_mapping` для каждой пары entity:
  - `clients.id` → `Customer.external_id` (identity, ro)
  - `clients.name` → `Customer.name` (identity)
  - `clients.phone` → `Customer.phone` (identity, with normalize_phone transform)
  - ... 47 пар полей
- `propose_status_mapping`:
  - `orders.status: NEW` → `Order.state: NEW`
  - `orders.status: PAID` → `Order.state: PREPAID`
  - ... 8 mappings
- `propose_workflow_mapping` — основной флоу Order.
- `validate_mapping_on_sample`:
  - Берётся выборка 500 заказов.
  - Прогоняется через mapping.
  - 487 (97.4%) проходят чисто.
  - 13 (2.6%) дают warnings: 12 — те самые «логически невозможные» переходы из data quality issues, 1 — null vendor_id с paid статусом.
- `request_human_approval`:
  - Никита проходит через mapping, делает 4 правки (минорные).
  - Подписывает event `MappingApproved` (валентность signer).

Account `Migration.mappings_approved_count` = 23 (по числу entities).
State Migration → `DATA_MIGRATE`.

---

## Weeks 3-4 — Data Migration (DataMigrator)

### Day 15 — Подготовка

Никита и Надежда согласовывают дату cut-over: **2026-07-07** (через 8 недель). До этой даты:
- Параллельная работа в обеих системах в течение 2-3 недель (start ~16 июня).
- Полный snapshot для финальной миграции остатков — утром 6 июля.

DataMigrator получает Mapping Rules.

### Day 16-19 — Dedupe & cleanup

`dedupe_directory` для Customer:
- Из 4521 записей выявляет 712 групп дублей (один человек в нескольких записях).
- Proposed merge: 712 групп → 1809 уникальных Customer'ов.
- Confidence по каждому merge'у (от 0.6 до 0.99).
- Надежда (один час работы) approve'ит автоматически 80% (high confidence), вручную решает 142 случая.
- 70 случаев оставлены как `flagged_duplicates` — статус «возможно дубль, требует ручной проверки позже».

`dedupe_directory` для Product:
- 3140 → 2890 после слияния разных написаний.

### Day 20-25 — Initial transactions

`transact_initial_state` начинается. Для каждого Customer:
- 1 Dr-транзакция на инициализацию identity accounts.
- 1 Dr/Cr на инициализацию AR/AP balance из текущей системы.

Для каждого Vendor:
- Аналогично.

Для активных Order (всё, что не CLOSED):
- 1 transaction на инициализацию идентификации.
- 1 transaction на текущий state.
- 1 transaction на already paid amounts (prepayment, final).

Для Warehouse:
- Stock balance per Item × Warehouse.

`generate_reconciliation_report`:
- Total customers count: совпадает (1809 уникальных).
- Total active orders: совпадает (62).
- Total AR balance: совпадает (1.247.000 грн).
- Total AP balance: совпадает (892.000 грн).
- Total stock value: расхождение **минус 23.000 грн**.

Расхождение по stock value — нужно разбираться. DataMigrator проверяет: оказывается, 4 товара имеют отрицательный stock в самописке (баг старой системы). Decision: в новой системе ставим 0 и помечаем для inventory check.

Account `Migration.data_migrated_pct` = 95% (Надежда подпишет 100% после inventory check).

### Day 26 — Sign-off

Главбух Golden House (Олена) проводит inventory check, находит 3 пропавших товара, корректирует. Total stock match.

Event `Migration.DataMigrationApproved` (валентность signer = Олена).

---

## Weeks 4-5 — Process Migration (ProcessMigrator)

### Day 27-29 — Workflow extraction

`extract_workflow_definitions` для Order:
- В самописке нет явных workflow definitions. Sub-agent делает `reverse_engineer_workflow` из исторических данных.
- На выборке 2000 closed Order анализируется sequence of state changes.
- Output: state graph с 11 states (тот же, что в Industry Pack), 23 transitions, для каждого transition — частота и среднее `time_in_state`.

Findings:
- Самый медленный переход: `IN_PRODUCTION → READY` (avg 31 день). Это нормально для мебели.
- Самый ненадёжный переход: `READY → FULLY_PAID` (15% заказов застревают на >7 дней). Это потенциальная точка автоматизации.

### Day 30-33 — Synthesis в state graphs

`synthesize_state_graph` для Order — берётся pack template, накладываются особенности Golden House.

`synthesize_corezoid_process` для каждой интеграции:
- ПРРО Cash Online — переход с file-based на API-based (API доступен у провайдера).
- Приват24 / Моно — автоматическая выгрузка выписок и автосоздание Payment событий.
- Нова Пошта — переход с email-based на API-based (генерация ТТН).
- Telegram-уведомления — миграция bot из самописки в Corezoid-процесс.

`assign_valences`:
- `OrderCreated` — executor = manager
- `Payment.Prepayment` — executor = manager, signer = customer
- `Payment.FinalPayment` — то же
- `VendorOrderSent` — executor = procurement_manager (новая роль!)

Помечается: текущая компания не имеет выделенной роли procurement_manager. Тот же менеджер делает всё. **Recommendation от ProcessMigrator:** обсудить с Надеждой создание роли — это даст лучшую specialization и видимость на дашборде «Поставщики». Надежда принимает рекомендацию для роста, но на старте оставляет старую модель (один менеджер ведёт сделку end-to-end).

`test_workflow_on_replay`:
- Берётся 100 closed Order из истории.
- Прогоняются через новый state graph.
- 96 проходят чисто.
- 4 имеют расхождения: те самые «логически невозможные» из data quality. ProcessMigrator предлагает фикс — добавить инвариант, который блокирует невалидные переходы (новая система не будет иметь этого бага).

Account `Migration.processes_migrated_pct` = 100%.
State Migration → `VALIDATE`.

---

## Weeks 5-7 — Validation (ValidationAgent)

### Day 35 — Start of parallel run

Cutover дата ещё через 4 недели. С этого дня — параллельная работа: команда Golden House работает в обеих системах. Каждое значимое событие в самописке зеркалируется в Simulator (вручную или через Corezoid-импорт).

`ValidationAgent` активирован как observer.

### Days 35-49 — Continuous comparison

Каждую ночь ValidationAgent делает snapshot обеих систем и сравнивает.

**Week 5 (days 35-41):**
- Day 35: 17 discrepancies. Все classified как data drift (записи, созданные сегодня в самописке, ещё не зеркалированы). False alarm.
- Day 36: 9 discrepancies. 7 — то же. 2 — реальные:
  - Order #4587: в самописке статус FULLY_PAID, в Simulator всё ещё PREPAID. Reason: менеджер не нажал «провести доплату» в Simulator. Classification: process_compliance_issue. Action: уведомить менеджера.
  - Order #4612: разница в total_amount на 50 грн. Reason: в самописке менеджер изменил цену вручную через прямую правку в БД (нелегальное действие, но возможное в самописке). Classification: data_anomaly. Action: фикс в Simulator после ручной верификации.
- Day 37-41: discrepancy rate стабилизируется на 2-4 в день, все либо тренировочные либо real-time delays.

`validation_consecutive_match_days` = 0 (день 1 не считается, есть discrepancies).

**Week 6 (days 42-48):**
- Команда привыкает работать в Simulator first.
- Day 44: 0 discrepancies. ✓
- Day 45: 1 discrepancy (logic) — формула расчёта margin отличается. Investigation: в самописке margin считался без вычета комиссии Приват24, в Simulator — с вычетом. Это apparently feature, не bug. После согласования с Надеждой — соответствие выбрано Simulator-вариант (правильная финансовая модель), Brief фиксируется как «отклонение от старой системы» для прозрачности.
- Day 46-48: 0 discrepancies.

`validation_consecutive_match_days` = 3 на конец недели 6.

**Week 7 (days 49-55):**
- Day 49-55: 0 discrepancies весь день, кроме одного:
  - Day 51: 1 discrepancy. Order #4623: разница в логистике на 200 грн. Reason: Надежда забыла внести стоимость доставки в самописку, в Simulator она автоматически считалась из ТТН. Classification: data_quality_improvement. Это хороший сигнал — Simulator находит то, что в самописке терялось.

`validation_consecutive_match_days` = 7 → 11 → 14 на день 55. **Threshold для cutover достигнут.**

Event `Validation.ReadyForCutover` на акторе Migration.
Account `Migration.cutover_readiness_score` = 0.91.

---

## Week 8 — Cutover (CutoverAgent)

### Day 56-58 — Final prep

- Снапшот обеих систем 6 июля утром.
- Финальная сверка балансов — 100% match.
- Уведомление всех сотрудников: с 7 июля 00:00 — только Simulator. Самописка переводится в read-only.
- Никита и команда поддержки готовятся к hot week.

`CutoverAgent` активирован.

### Day 59 — Cut-over (7 июля)

`Cutover.Executed` event signed by Надежда + Никита.

State Migration → `CUTOVER`.

CutoverAgent начинает наблюдение:
- Monitor Telegram-канал команды Golden House (~30 человек).
- Monitor operational metrics (rate of orders, errors, transactions).
- Monitor support tickets.

### Days 59-65 — First week post-cutover

**Day 59 (Wed):**
- 11:23 — менеджер Юлия пишет в общий чат: «Не вижу как провести предоплату ПРРО». Sentiment: frustration.
- CutoverAgent detects, alerts Никите. Никита через 7 минут пишет персонально Юлии, помогает.
- 14:50 — кладовщик пишет: «А как принимать товар на склад?». CutoverAgent ловит — это новый процесс, в самописке кладовщик ничего не делал, всё было на менеджере. CutoverAgent flags для будущего EvolutionAgent.
- 19:00 — Надежда пишет: «Норм день, 8 заказов прошло». Sentiment: positive.

**Day 60 (Thu):**
- 8 заказов оформлено успешно.
- 1 incident: Order #4710 завис между PREPAID и SUPPLIER_NOTIFIED. CutoverAgent сравнивает с старой системой — там тоже застрял. Не bug Simulator, реальная проблема процесса. Никита помогает Надежде.

**Days 61-65:**
- Объём операций стабилизируется (~9-12 заказов/день, как обычно).
- 3-4 user-level вопроса в день, все resolveable.
- 0 критических ошибок.

`post_cutover_incidents` (severity = high or critical) = 0.
`sentiment_score` = positive.
`critical_errors_count` = 0.

### Day 66 — Handoff to Evolution

Event `Migration.Operate.Stable`.
State Migration → `OPERATE`.

`CutoverAgent` передаёт контроль `EvolutionAgent`.

Account `Migration.cutover_success_rate` (этот клиент) = 1.0.
Account `Migration.post_cutover_incidents` = 0.

---

## Months 3-12 — Evolution (EvolutionAgent)

### Month 3 (August 2026)

EvolutionAgent наблюдает за operational data Golden House. Выявленные patterns:

**Pattern 1:** Менеджеры вручную звонят клиентам с напоминанием о готовности товара в 87% случаев. Detected as repetitive_manual_action.
- Proposal: автоматический Telegram/SMS клиенту при `SubOrder.state == READY`.
- Approval: Надежда подписывает.
- Implementation: новый Corezoid-процесс, активирован через 2 дня.
- Result: освобождает ~40 часов работы менеджеров в месяц.

**Pattern 2:** В некоторых заказах manager заносит товар «не из каталога» через свободный текст. Detected as new_entity_proposal.
- Proposal: добавить актор `CustomItem` (отличается от Product отсутствием в каталоге).
- Approval: после обсуждения с Надеждой — отклонено. «Это исключения, мы вообще не должны такое продавать».
- Implementation: вместо этого — alert менеджеру при попытке такого ввода.

`smart_company_maturity_level` = 5 (Accounting & Processing полное).

### Month 6 (November 2026)

**Pattern 3:** Один из поставщиков (Vendor #7) имеет среднее `time_in_state_IN_PRODUCTION = 47 дней` vs контрактных 30. Detected as bottleneck.
- Proposal: показать Надежде дашборд «Поставщики» с явным rating.
- Implementation: дашборд (уже в pack'е) выведен на главный экран Надежды.
- Result: Надежда меняет 2 из 3 крупнейших заказов в сторону другого поставщика. Average lead time для нового quarter падает на 11 дней.

**Pattern 4:** Pyramid level assessment. Текущий стек: Accounting + Processing + Communication (Telegram-боты). Не используется: Orchestrator (нет advanced workflows), PM/Calendars (всё ещё в Google Calendar отдельно), Smart Bus.
- Proposal: интегрировать Google Calendar через Corezoid-процесс, чтобы события из Simulator (например, «доставка завтра в 14:00») создавали blockers в календарях менеджеров.
- Approval: signed by Надежда.
- Implementation через 5 дней.

`smart_company_maturity_level` = 7.

### Month 9 (February 2027)

**Pattern 5:** Sales-данные показывают сезонность. Q1 (январь-март) — спад на 30%. Detected.
- Proposal: AI-агент `SalesForecaster` (новый актор, аналог EvolutionAgent в другом домене) — прогнозирует поток заказов на 60 дней вперёд из исторических паттернов.
- Approval: Надежда signed.
- Implementation: 3 недели.
- Result: Надежда видит forecast на main dashboard, корректирует закупки поставщикам заранее.

`smart_company_maturity_level` = 8.

### Month 12 (May 2027)

Год после go-live. Состояние Golden House:

| Metric | Value | Change vs самописки |
|---|---|---|
| Orders/month | 80-95 | +20% |
| Avg cycle time | 38 дней | -6 дней |
| Customer LTV avg | 24% выше | growth driven by repeat business visibility |
| Manager hours/month | -40 ч / менеджер | automated reminders + dashboard |
| Time to financial close | 1 день | от 7 дней (was) |
| Smart Company maturity | 8 | from 1 (pre-Simulator) |

Renewal у Golden House — automatic. Они платят за то, чтобы EvolutionAgent продолжал улучшать их компанию.

`client_renewal_rate` (этот клиент) = 1.0.

State Migration → `EVOLVE` (постоянный, не finite).

---

## Аккаунты на акторе Migration_GoldenHouse — финальные значения (Month 12)

| Account | Value |
|---|---|
| `discovery_completeness_pct` | 100 |
| `systems_profiled_count` | 1 |
| `actor_graph_synthesized_pct` | 100 |
| `mappings_approved_count` | 23 |
| `data_migrated_pct` | 100 |
| `processes_migrated_pct` | 100 |
| `validation_consecutive_match_days` | 14 (на момент cutover) |
| `cutover_readiness_score` | 0.91 |
| `post_cutover_incidents` (critical) | 0 |
| `smart_company_maturity_level` | 8 |
| `evolution_milestones_count` | 5 |
| `client_satisfaction_avg` | 4.7 / 5 |
| `total_time_to_full_value_months` | 9 |

---

## Выводы из симуляции — что spec'и упускают

Прогон через 9 агентов end-to-end выявил несколько мест, где master spec нуждается в доработке:

### 1. SystemProfilerAgent — multi-mode coordination

В симуляции profiling Golden House шёл через 3 режима одновременно (schema + document + narrative). В спецификации SystemProfilerAgent это упоминается как «cross-checks возможны», но **процесс координации не описан**.

Нужно добавить в SystemProfilerAgent spec: explicit workflow для multi-mode profiling. Когда какой режим запускается, как объединяются результаты, как разрешаются конфликты.

### 2. DataMigrator — sign-off chain не зафиксирован

В симуляции были две явные точки signature от клиента:
1. Главбух (Олена) подписывает акт сверки остатков.
2. Owner (Сергей) подписывает Cutover.

В spec'е DataMigrator упомянуто «главбух подписывает», но **полная chain валентностей не формализована**. Кто signer, кто executor, кто viewer на каждом миграционном событии — должно быть в spec'е DataMigrator (и аналогично для ProcessMigrator, CutoverAgent).

### 3. ValidationAgent — false positives классификация

В симуляции 17 discrepancies в первый день оказались false positives (data drift из-за паралельной работы). Это нормально, но в spec'е не описано:
- Как ValidationAgent отличает false positive от реального issue.
- Что делать с накопленным `validation_consecutive_match_days`: должен ли false positive сбрасывать счётчик?

**Рекомендация:** добавить в spec ValidationAgent явную категорию `validation_noise` с автоматической фильтрацией, и `validation_consecutive_match_days` считать только по реальным расхождениям.

### 4. CutoverAgent — graduate handoff, не binary

В симуляции переход от CutoverAgent к EvolutionAgent был «binary» (Day 66, event Migration.Operate.Stable). Но на самом деле CutoverAgent ещё видит metrics 1-2 недели после, а EvolutionAgent уже потихоньку начинает analysing. **В spec'е стоит явно описать overlapping period**, когда оба агента активны.

### 5. EvolutionAgent — pattern detection requires N months data

В симуляции первые рекомендации EvolutionAgent появились через 3 месяца. Это потому что pattern detection нуждается в данных. В spec'е стоит явно указать **cold-start strategy**: первые 60-90 дней EvolutionAgent работает в режиме «collecting baseline», не делая proposals. Это снимет ожидание клиента, что «AI сразу начнёт улучшать».

### 6. Industry Pack — concept of «delta»

Furniture Retail Pack покрыл Golden House на 92%. Delta была:
- SubOrder vs Order.vendor_id
- InstallationTask
- Custom Item handling

Эта delta была обнаружена GraphSynthesizer'ом и инкорпорирована в actor graph клиента. Но **не была добавлена обратно в Furniture Retail Pack** как кандидат на эволюцию pack'а. Это упущенная feedback loop.

**Рекомендация:** ввести понятие `pack_evolution_proposal` — каждое внедрение собирает delta, которая потом review'ится Pack Owner'ом для возможного включения в next-version pack. Это даёт organic эволюцию каталога.

### 7. Roles — пробел в spec'ах

В симуляции CutoverAgent flagged «новая роль procurement_manager» как recommendation, но эта recommendation осталась без ясного process'a — что с ней делать дальше. Это generic проблема: **AI-агенты выдают organizational recommendations**, но spec не описывает, какой агент за них отвечает и куда они попадают.

**Рекомендация:** ввести явный type recommendation «organizational change» с отдельным backlog'ом, отдельным дашбордом, и явной chain отговорки решений.

---

## Что это даёт продукту

Симуляция показала:

1. **Onramp работает end-to-end.** От первого клика на лендинге до 12 месяцев после go-live — все 9 агентов имеют конкретное место и produced результат.

2. **Реальные сроки:** 8 недель от Brief до Cutover (L2-трек). Это соответствует master spec'у.

3. **Зрелость maturity для одного клиента — год.** За 12 месяцев Golden House поднимается с уровня 1 до уровня 8 пирамиды Smart Company. Это и есть rationale для подписки.

4. **Каждый AI-агент имеет уникальную ценность.** Если убрать любой из 9 — что-то отвалится. Это значит каталог не overengineered.

5. **Семь конкретных правок в master spec и agent specs** (см. предыдущий раздел) — выявлены, потому что мы прогнали симуляцию. Без симуляции эти gap'ы бы вылезли только в production.

---

**Версия:** 1.1
**Статус:** narrative simulation для команды, с v1.4 reframing (Phase actors lens)
**Применение:**
- Internal documentation (как Onramp работает в жизни)
- Sales-demo material (показать реальный путь клиента)
- Stress-test для специфик
- Feedback loop в master spec и agent specs
