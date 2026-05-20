# Discovery Agent — Спецификация v1.3

**Документ для команды разработки.**
**Дата:** 17 мая 2026
**Изменение от v1.2:** DiscoveryAgent в Этапе 5 (Presentation) дополнительно генерирует `roadmap_graph` — персонализированный actor graph миграции клиента, состоящий из Phase actors. Добавлен tool `generate_roadmap_graph`. Уточнено что Lead-актор привязывается к роди Migration actor с roadmap_graph_id. Согласовано с Master Spec v1.4.

**Предыдущие изменения:**
- **v1.1 → v1.2:** синхронизирована с нотацией Golden House full document. Accounts с Dr/Cr транзакциями. Валентности на событиях. `time_in_state` как нативный Account. Инварианты на акторах. Furniture Retail Industry Pack переписан по 8 кейсам Golden House.

---

## 0. Контекст и архитектурный принцип

### Бизнес-контекст
В Украине идёт массовая миграция с российских и устаревших учётных систем. Триггер — введение НДС и требований для ФОП в 2027 году. Узкое горло — фаза Discovery: 1-2 недели человеческого времени на каждого лида. Решение — AI-агент, проводящий Discovery самостоятельно.

### Архитектурный принцип

Discovery-агент продаёт Simulator с помощью самой Simulator. Это техническое требование, не маркетинг.

В Simulator любая сущность — **актор**. У актора есть:
- **State** — где он сейчас в жизненном цикле
- **Accounts** — счета, на которых копится время, деньги, события через **Dr/Cr-транзакции**
- **Связи** с другими акторами — образуют **граф**
- **Форма** — UI-представление
- **События (ивенты)** — первичные документы, меняющие state и порождающие транзакции
- **Инварианты** — условия целостности, проверяемые триггерами

На событиях работают **валентности**:
- `viewer` — может видеть событие
- `signer` — должен подписать
- `executor` — исполняет действие

В Corezoid живёт **обработка** — процессы, вызываемые событиями Симулятора: интеграции (ПРРО, банки, перевозчики), нотификации, AI-агенты, маршрутизация. **Сами события создаются в Симуляторе** — это его базовый примитив.

Применительно к Discovery:
- **Lead — актор** в Simulator. Не запись в стороннем CRM.
- **DiscoveryAgent — AI-актор первого класса** с правом подписи на events Lead.
- **Industry Pack — актор-шаблон** в каталоге, клонируемый при `create_prototype_stand`.
- **Prototype — актор**, созданный клонированием Industry Pack под данные Lead.

Никакого внешнего CRM, BI или ETL. Единственная внешняя зависимость — LLM API.

### Условные обозначения (унифицированы с Golden House full)

- 🟦 **Состояние** актора (state в Симуляторе)
- 🟩 **Событие / действие** (ивент в Симуляторе)
- 🟧 **Транзакция** на счёт (Dr/Cr на Account)
- 🟪 **Процесс Corezoid** (интеграция / триггер / нотификация)
- 🟨 **Решение / условие** (инвариант)

---

## 1. System prompt агента

```
Ты — Discovery-консультант компании Simulator (платформа на базе Corezoid).
Твоя роль — провести с потенциальным клиентом структурированную беседу
длительностью 60-90 минут (в один или несколько заходов), по результатам
которой собрать Discovery Brief, классифицировать клиента в один из трёх
треков внедрения (Express / Standard / Custom) и подготовить
персонализированное коммерческое предложение.

ТЕХНИЧЕСКИЙ КОНТЕКСТ
Ты работаешь как AI-актор в Simulator. Каждый потенциальный клиент —
это актор Lead в Simulator с Accounts, на которых копятся данные
по фазам твоего диалога (через Dr/Cr-транзакции). Изменения в Lead
происходят через события, которые ты эмитируешь через MCP tools.
После завершения диалога актор Lead передаётся solution architect'у.

ЦЕЛЕВОЙ АРТЕФАКТ
Твоя работа считается успешной, если собранный Lead-актор содержит
достаточно данных, чтобы solution architect за 1-2 встречи мог
развернуть его в Target Operating Model уровня документа
«Golden House — Покрытие кейсов клиента» (8 кейсов с акторами,
Accounts, событиями, валентностями, состояниями, инвариантами и
Corezoid-процессами). Если по результатам диалога ты не можешь
описать хотя бы один из 8 кейсов клиента — Discovery не завершён,
нужно дозадать вопросы.

КОНТЕКСТ КЛИЕНТА И РЫНКА
Украинский рынок. Многие клиенты сейчас мигрируют с 1С или БАС или
с самописных систем — частично из-за санкционных причин, частично
из-за введения НДС и новых требований для ФОП в 2027 году. Сегмент —
B2C/B2B: розница, опт, услуги, лёгкое производство. Говори на языке
клиента: украинском или русском, по первому сообщению.

ТВОЙ ПРОДУКТ
Simulator — control plane: моделирование, граф состояний, дизайн
бизнес-процессов. В нём живут акторы со state, Accounts, формами,
событиями, инвариантами. Corezoid — compute plane: исполнение,
интеграции, API. В продакшене: КЕП через Дію.Підпис, ТТН Нова Пошта
и Укрпошта, банки Приват/Моно/УкрСиб, отправка отчётности через
M.E.Doc и Вчасно. Кейсы: A-Bank, Метро, Синево.

Подход — Actor Graph: всё моделируется как акторы с Accounts и
события, порождающие Dr/Cr-транзакции. Нативно покрывает функционал
1С/БАС и даёт то, чего у них нет: живые графы процессов, событийную
архитектуру, нативный API, готовность к AI-агентам.

ТВОИ ПРИНЦИПЫ
1. Веди диалог как опытный консультант, не как анкета. Слушай больше,
   чем говоришь.
2. Никогда не обещай функционал, не подтверждённый каталогом Industry
   Packs или существующими интеграциями. При сомнении — фиксируй как
   «delta-кастомизация» и эскалируй на solution architect.
3. Цены и сроки называй только из таблицы треков. Не торгуйся.
4. Не говори клиенту, что ты AI, если он не спросит. Если спросит —
   ответь честно.
5. Используй tool `match_industry_pack` после Этапа 1 — он скажет,
   какой шаблон и с каким % покрытия подходит.
6. На каждом ключевом ответе строй краткую интерпретацию своими
   словами и проси подтвердить.
7. Если активирован триггер эскалации — спокойно предложи финальный
   звонок с solution architect и вызови `escalate_to_human`.

СТРУКТУРА БЕСЕДЫ — 5 ЭТАПОВ
Этап 1: Профиль компании (10-15 мин)
Этап 2: Бизнес-флоу (20-30 мин)
Этап 3: Объёмы и инфраструктура (10 мин)
Этап 4: Специфика и ограничения (15-20 мин)
Этап 5: Презентация результата (5-10 мин)

После каждого этапа вызывай tool `save_session` — он эмитирует event
`Phase.Completed` на акторе Lead и обновляет его Accounts через
Dr/Cr-транзакции.

ВЫХОДНЫЕ АРТЕФАКТЫ
После Этапа 5:
- `create_prototype_stand` — клонирует Industry Pack как актор
  Prototype с TTL 14 дней, привязывает к Lead.
- `generate_discovery_brief` — генерирует PDF и markdown проекцией
  на Accounts Lead.
- Corezoid-процесс уведомления solution architect.
```

---

## 2. MCP Tools — сигнатуры

```typescript
match_industry_pack(input: {
  industry: string,
  description: string
}) → {
  pack_actor_id: string,
  coverage_pct: number,
  matched_cases: string[],      // например ["case_1_retail", "case_3_supplier"]
  delta_required: string[]
}

// save_session эмитирует event Phase.Completed на акторе Lead
// и применяет Dr/Cr-транзакции к его Accounts
save_session(input: {
  lead_actor_id: string,
  phase: "profile" | "flow" | "volumes" | "specifics" | "presentation",
  collected_data: object
}) → { ok: boolean, lead_state: string }

emit_lead_event(input: {
  lead_actor_id: string,
  event_type: string,
  payload: object
}) → { ok: boolean, event_id: string }

create_prototype_stand(input: {
  lead_actor_id: string,
  industry_pack_actor_id: string,
  client_data: {
    company_name: string,
    sample_customers?: object[],
    sample_vendors?: object[],
    sample_products?: object[],
    showrooms?: object[],
    custom_fields?: object
  }
}) → {
  prototype_actor_id: string,
  stand_url: string,
  expires_at: string
}

classify_track(input: {
  lead_actor_id: string
}) → {
  track: "express" | "standard" | "custom",
  reasoning: string,
  estimated_weeks: [number, number],
  estimated_team_size: number
}

generate_discovery_brief(input: {
  lead_actor_id: string
}) → {
  brief_url: string,
  brief_md: string,
  commercial_offer_url: string,
  tom_blueprint_url: string     // черновик Target Operating Model
                                // в стиле Golden House full document
}

// Генерация персонализированного Migration Roadmap actor graph
// для клиента — клонирует template из Onramp Process pack по треку
// и кастомизирует под deployment_mode, target_deadline, specifics
generate_roadmap_graph(input: {
  lead_actor_id: string,
  track: "L1" | "L2" | "L3" | "L4",
  deployment_mode: "cloud" | "hybrid" | "on_prem" | "air_gapped",
  target_cutover_date: string,
  custom_phase_specs?: object[]  // дополнительные Phase actors если клиент упомянул нестандартные требования
}) → {
  migration_actor_id: string,        // создан новый Migration actor
  roadmap_graph_id: string,
  phase_actors: object[],            // массив созданных Phase actors
  forecasted_cutover_date: string,
  roadmap_visualization_url: string  // для показа клиенту
}

escalate_to_human(input: {
  lead_actor_id: string,
  reason: "explicit_request" | "regulated_industry" | "enterprise_size"
        | "emotional_negative" | "complex_specifics" | "legal_sensitive"
        | "other",
  urgency: "immediate" | "next_business_day",
  context_summary: string
}) → { ok: boolean, handoff_scheduled_at?: string }
```

---

## 3. Граф состояний актора Lead

```
INITIAL
   │ first_message_received
   ▼
GREETING ───── language_detected ──────► PROFILE
   │ explicit_dropout                       │
   ▼                                        │
DROPPED                                     ▼
                                       BUSINESS_FLOW
                                            │
                                            ▼
                                       VOLUMES_INFRA
                                            │
                                            ▼
                                       SPECIFICS
                                            │
                                            ▼
                                       PRESENTATION
                                            │
                         ┌──────────────────┼──────────────────┐
                         ▼                  ▼                  ▼
                BRIEF_GENERATED       ESCALATED           DROPPED
                         │
                         ▼
                AWAITING_HUMAN_HANDOFF
                         │
                         ▼
                BRIEF_SIGNED ── done
```

`time_in_state` копится на каждом состоянии нативно как Account Lead'а. Это даёт метрики воронки бесплатно: средняя длительность фазы, узкие места, drop-off rate.

---

## 4. Детальные промпты по этапам

### Этап 1. Профиль (10-15 мин)

**Цель:** наполнить идентификационные Accounts Lead, понять текущую систему, триггер миграции.

**Открывающий ход:**
> «Добрый день! Я — Discovery-консультант компании Simulator. За ближайший час (можем разбить на 2-3 захода, если удобнее) я разберусь в вашем бизнесе настолько, чтобы понять, подойдёт ли вам наша платформа для замены текущей учётной системы. На выходе у вас будет персональный демо-стенд с вашими данными и предварительное коммерческое предложение. Начнём с базового — расскажите, чем занимается ваша компания?»

**Опросный фрейм:**

| Вопрос | Account Lead | Триггер follow-up'а |
|---|---|---|
| Чем занимаетесь, давно ли на рынке? | `industry`, `company_age_years` | «Производство своё или импорт?» |
| Сколько точек/салонов? | `outlets_count` | Если >5 — уточнить сетевую логику |
| Готовая / на заказ / смешанный? | `assortment_type` | На заказ — копать индивидуальные сделки в Этапе 2 |
| Сколько сотрудников всего? | `employees_count` | Если >100 — флаг на Custom |
| Какая система сейчас? | `current_system`, `current_system_version` | «1С» → украинская/российская? БАС или классика? |
| Что нравится / не нравится? | `current_system_pain` | Слушать внимательно |
| Почему меняете сейчас? | `migration_trigger` | НДС/ФОП-2027 → связать с дедлайном |
| Дедлайн перехода? | `target_deadline` | <2 месяцев → флаг (только Express) |

**Exit-критерий:** идентификационные Accounts заполнены. State Lead переходит в `BUSINESS_FLOW`.

---

### Этап 2. Бизнес-флоу (20-30 мин) — главный этап

**Цель:** реконструировать актор-граф основной сделки клиента, определить % покрытия Industry Pack по 8 кейсам.

**Открывающий ход:**
> «Теперь самое важное — давайте разберём, как устроена одна типичная сделка. Расскажите по шагам, не торопясь: клиент пришёл в салон — что происходит дальше? Я буду переспрашивать там, где нужно уточнить. В конце нарисую вам граф состояний — посмотрите, правильно ли я понял.»

**Опросный фрейм по 8 кейсам Golden House:**

| Кейс | Что выяснять | Account Lead |
|---|---|---|
| 1. Розничная продажа в салоне | Кто оформляет заказ, какие связи между менеджером, салоном, клиентом? | `case_1_retail_flow` |
| 2. Предоплата + ПРРО | Какая предоплата? В какой момент? Через какой ПРРО? | `case_2_prepayment_flow`, `prro_provider` |
| 3. Работа с поставщиком | Как отправляется заказ? Как приходит подтверждение? Что хранится про цены? | `case_3_vendor_flow`, `vendor_communication_pattern` |
| 4. Управление сроками | Кто следит за датой готовности? Что происходит при просрочке? | `case_4_lead_time_flow` |
| 5. Доплата перед отгрузкой | Когда берётся доплата? Через какой механизм уведомления? | `case_5_final_payment_flow` |
| 6. Логистика и склад | От поставщика — кто везёт? Складирование транзитное или есть свой склад? | `case_6_logistics_flow`, `warehouse_model` |
| 7. Закрытие сделки | Какие документы подписываются на выходе? Кто? | `case_7_closing_flow` |
| 8. Возвраты | Как часто? Какие причины? Через ПРРО или отдельно? | `case_8_returns_flow`, `returns_frequency` |

**Паттерн углубления:** после рассказа клиента построить ASCII-граф состояний Order и показать:

```
NEW → PREPAID → SUPPLIER_NOTIFIED → IN_PRODUCTION → READY → 
  → FULLY_PAID → AT_WAREHOUSE → SHIPPED → DELIVERED → CLOSED
```

«Где я ошибся или упростил?»

**Exit-критерий:** построен и подтверждён граф + ключевые исключения. Заполнены `case_1` через `case_8` Accounts. **После этапа** — вызов `match_industry_pack` и запись `industry_pack_match_pct`.

---

### Этап 3. Объёмы и инфраструктура (10 мин)

| Вопрос | Account Lead |
|---|---|
| Заказов в месяц? | `orders_per_month` |
| Активных контрагентов? | `contractors_count` |
| Активных поставщиков? | `vendors_count` |
| SKU? | `sku_count` |
| Дубли в базе? | `data_dirtiness_flag` |
| Можете выгрузить базу? | `db_export_available` |
| Одновременных пользователей? | `concurrent_users` |
| ПРРО — провайдер? | `prro_provider` |
| Банки? | `banks[]` |
| Логистика — провайдеры? | `logistics_providers[]` |
| Кто администрирует сейчас? | `it_ownership` |

---

### Этап 4. Специфика и ограничения (15-20 мин)

| Вопрос | Account Lead |
|---|---|
| Что НЕ хотите потерять? | `must_keep[]` |
| Что бесит больше всего? | `top_pain` |
| Какие отчёты собственник смотрит? | `owner_reports[]` |
| Owner проекта внутри? | `internal_owner` |
| Decision-maker? | `decision_maker` |
| Бюджет — диапазон? | `budget_range` |
| Прежние попытки миграции? | `previous_attempts[]` |
| Критерий успеха через год? | `success_criteria` |

`escalation_signals[]` — отдельный накопительный Account; пополняется через Dr-транзакции при любом признаке риска.

---

### Этап 5. Презентация результата (5-10 мин)

**Шаги:**
1. Резюме клиента — проекция на идентификационные Accounts.
2. Граф флоу — визуализация `flow_graph` из case_1...case_8 Accounts.
3. Industry Pack — «Ваш кейс на X% покрывается шаблоном Furniture Retail. Что нужно докрутить: [delta]».
4. Live-стенд — вызов `create_prototype_stand` → отправка `stand_url`.
5. Классификация трека — `classify_track`, объяснение reasoning.
6. **Migration Roadmap generation** — вызов `generate_roadmap_graph(lead_id, track, deployment_mode, target_cutover_date)`. Клонирует template из Onramp Process pack, кастомизирует под клиента. Получаем созданный Migration actor + roadmap_graph_id + phase_actors[].
7. **Показ персонального roadmap клиенту.** Визуализация графа фаз с конкретными датами, owner-агентами, signoffs. Клиент видит свой 8-фазный план миграции в graph/Gantt/calendar views. Сбор event `Roadmap.Approved` со signer = product_owner_клиента.
8. Коммерческое предложение — `generate_discovery_brief` → отправка ссылки.
9. **TOM blueprint** — отправляется solution architect'у как black-box draft в стиле Golden House full document.
10. Назначение финального звонка с solution architect для финализации roadmap.

---

## 5. Furniture Retail Industry Pack — по 8 кейсам Golden House

Industry Pack — это актор-шаблон в каталоге Simulator. При вызове `create_prototype_stand` он клонируется как актор `Prototype` с подстановкой данных Lead. Ниже — структура шаблона по 8 кейсам.

### Кейс 1 — Розничная продажа мебели в салоне

**Акторы:**
- `Customer` — клиент
- `Order` — заказ
- `Showroom` — салон
- `Employee` — сотрудник (валентность executor)

**Связи в графе:**
```
Customer ←→ Order ←→ Showroom ←→ Employee
```

**События:**
- `OrderCreated` (валентности: executor = менеджер)

**Accounts:**
- `Customer.LTV` — пожизненная ценность (Cr-транзакция при каждом закрытом Order)
- `Showroom.orders_count` — счётчик
- `Employee.deals_in_progress` — открытые заказы

**State Order:** `NEW`

---

### Кейс 2 — Предоплата + фискализация (ПРРО)

**События:**
- `Payment` (валентности: executor = менеджер, signer = клиент)

**Accounts (Dr/Cr на Order):**
- Dr `Order.prepayment_received` — получено
- Cr `Order.customer_obligation` — клиент должен меньше

**Также:**
- Cr `Showroom.cash_flow_today`
- Cr `Customer.payments_total`

**Corezoid-процесс:** `PRRO_Receipt` — вызывает API ПРРО, получает фискальный номер, прикладывает чек как attachment к событию.

**Переход state Order:** `NEW → PREPAID`

---

### Кейс 3 — Работа с поставщиком

**Акторы:**
- `Vendor` — поставщик (реквизиты, контакт, договор)
- `Product` — товар/модель
- `SubOrder` — подзаказ (один Order → много SubOrder)

**Accounts Product:**
- `Product.retail_price` — розничная (история в транзакциях)
- `Product.wholesale_price` — оптовая

**Accounts Vendor:**
- `Vendor.orders_active` — активные заказы
- `Vendor.amount_owed` — кредиторка
- `Vendor.avg_lead_time` — средний срок производства (растущий, автоматический)
- `Vendor.delay_incidents` — счётчик просрочек

**Accounts SubOrder:**
- `SubOrder.expected_delivery_date`
- `SubOrder.time_in_state_*` — нативно

**События:**
- `VendorOrderSent` (валентности: executor = менеджер закупок)
- `VendorConfirmation` — заполнение цены поставщика и срока

**Corezoid-процесс:** отправка заказа поставщику (email/API).

**Связи в графе:**
```
Order ←→ SubOrder ←→ Vendor
                ↓
             Product
```

---

### Кейс 4 — Управление сроками изготовления

**State SubOrder:** `SENT_TO_VENDOR → IN_PRODUCTION → READY → DELIVERED`

**Corezoid-триггеры (по due_date):**
- `-7 дней` → уведомление менеджеру «Скоро готовность, бери доплату»
- `-3 дня` → эскалация
- `0 дней` → автоматический переход в `READY`
- `+N дней` (просрочка) → создание актора-инцидента `Delay`, алерт супервайзеру

**Инвариант:**
```
SubOrder.state == IN_PRODUCTION AND now > expected_ready_date 
  → ALERT + create Delay actor
```

**Accounts (нативные):**
- `SubOrder.time_in_state_IN_PRODUCTION` — главная метрика SLA поставщиков
- `Vendor.avg_lead_time` — автоматический агрегат

---

### Кейс 5 — Доплата перед отгрузкой

**Триггер:** `SubOrder.state == READY` → Corezoid-процесс ставит задачу менеджеру.

**События:**
- `FinalPayment` (валентности: executor = менеджер, signer = клиент)

**Accounts (Dr/Cr на Order):**
- Dr `Order.final_payment_received`
- Cr `Order.customer_obligation` (закрытие остатка)

**Инвариант (условие отгрузки):**
```
Order.prepayment_received + Order.final_payment_received >= Order.total_amount
  → разрешена отгрузка
```

**Corezoid-процесс:** `PRRO_Receipt` (тот же, что в кейсе 2).

**Переход state Order:** `PREPAID → FULLY_PAID`

---

### Кейс 6 — Логистика и склад

**Акторы:**
- `Delivery` — рейс (дата, перевозчик, маршрут, стоимость)
- `Warehouse` — склад
- `Carrier` — перевозчик

**Связи:**
```
Delivery → SubOrder(s)  (один рейс — несколько подзаказов одного поставщика)
Delivery → Carrier
```

**State SubOrder:** `READY → PICKED_UP_FROM_VENDOR → AT_WAREHOUSE → SHIPPED_TO_CUSTOMER → DELIVERED`

**События:**
- `DeliveryCreated`
- `PickedUpFromVendor` → Dr `Warehouse.items_on_stock`
- `ShippedToCustomer` → Cr `Warehouse.items_on_stock`

**Accounts:**
- `Warehouse.items_on_stock`
- `Delivery.cost`
- `Order.logistics_cost` — попадает в себестоимость
- `SubOrder.time_in_warehouse` — оборачиваемость

---

### Кейс 7 — Закрытие сделки и финрезультат

**Триггер:** `Order.state == DELIVERED` → Corezoid-процесс закрытия.

**События:**
- `FinalDocumentsSigned` (валентности: signer = клиент + менеджер)

**State Order:** `DELIVERED → SIGNED → CLOSED`

**Финансовый агрегат на Order:**
```
margin = revenue − cost_of_goods − logistics_cost − fees
```

**Accounts:**
- `Order.margin`
- `Showroom.revenue_total`
- `Showroom.margin_total`
- `Employee.commission_earned` — автоматически по правилу
- `Company.PnL` — глобальный, агрегируется

**Инвариант:**
```
Order.state == CLOSED → форма read-only, события блокируются триггером
```

---

### Кейс 8 — Возвраты

**Акторы:**
- `Return` — возврат, связан графом с Order

**State Return:** `RETURN_REQUESTED → RETURN_APPROVED → ITEM_RECEIVED → REFUNDED`

**События:**
- `ReturnRequested`
- `ReturnApproved` (signer = менеджер/супервайзер)
- `ItemReceived`
- `Refunded`

**Сторно-транзакции:**
- Cr `Order.final_payment_received` (сторно)
- Dr `Order.refund_paid`
- Dr `Warehouse.items_on_stock` (товар возвращается на склад)

**Corezoid-процесс:** `PRRO_Receipt` с операцией «возврат».

**Accounts:**
- `Vendor.returns_due_to_quality` (если возврат по браку)
- `Return.reason` (учёт причин)
- `Customer.returns_count`

---

### Сводный граф 15 типов акторов

```
                Customer ←→ Order
                              │
                              ├─→ SubOrder ←→ Vendor
                              │       │
                              │       └─→ Product
                              │
                              ├─→ Delivery → Carrier
                              │       │
                              │       └─→ Warehouse
                              │
                              ├─→ Showroom ←→ Employee
                              │
                              ├─→ Payment → FiscalProvider (Bank)
                              │
                              └─→ Return

      Department:Procurement ←→ Employee
      Department:Logistics ←→ Employee
```

### Дашборды (нативные проекции на Accounts)

| Дашборд | Источник |
|---|---|
| **Операционный директор** | `Order.state` по этапам, `Order.total` неоплаченных, просрочки по `SubOrder`, `Warehouse.items_on_stock`, кассовый поток |
| **Менеджер салона** | Свои активные Order'а, заказы на доплату, своя выручка за месяц, своя комиссия |
| **Поставщики** | Рейтинг по `Vendor.avg_lead_time` + `Vendor.delay_incidents`, `Vendor.amount_owed`, возвраты по качеству |
| **Финансовый** | `Company.PnL`, `Showroom.revenue_total`, средний `cycle_time`, готовая выгрузка для НДС/ФОП |

Никакого ETL и BI поверх — дашборд = проекция на акторы.

### Автоматические метрики (бесплатные следствия модели)

**По заказу:** cycle time, время на каждом этапе, финрезультат.
**По поставщику:** средний срок производства (фактический), доля просрочек, качество (через возвраты), финансовая позиция.
**По менеджеру:** конверсия, средний чек, время реакции, личная маржа и комиссия.
**По салону:** выручка, маржа, поток заказов.
**По компании:** PnL в реальном времени, отчётность для ФОП/НДС, прогноз кассы на 30 дней.

---

## 6. Схема актора Lead

Это сердце Discovery-системы — актор в Simulator, полностью заменяющий внешний CRM.

**Идентификационные Accounts:**
- `company_name`, `contact_name`, `phone`, `email`
- `industry`, `source` (web/telegram/referral)
- `language` (uk/ru)

**State Lead:** см. граф состояний в разделе 3.

**Накопительные Accounts (Dr-транзакции при наполнении):**

*Фаза Profile:*
- `outlets_count`, `employees_count`, `company_age_years`
- `current_system`, `current_system_version`, `current_system_pain`
- `migration_trigger`, `target_deadline`
- `assortment_type`

*Фаза Business Flow (по 8 кейсам Golden House):*
- `case_1_retail_flow`
- `case_2_prepayment_flow`
- `case_3_vendor_flow`
- `case_4_lead_time_flow`
- `case_5_final_payment_flow`
- `case_6_logistics_flow`
- `case_7_closing_flow`
- `case_8_returns_flow`
- `flow_graph` (сводка)
- `flow_exceptions[]`
- `industry_pack_match_pct`
- `delta_required[]`

*Фаза Volumes & Infra:*
- `orders_per_month`, `contractors_count`, `vendors_count`, `sku_count`
- `concurrent_users`, `data_dirtiness_flag`, `db_export_available`
- `prro_provider`, `banks[]`, `logistics_providers[]`, `it_ownership`

*Фаза Specifics:*
- `must_keep[]`, `top_pain`, `owner_reports[]`
- `internal_owner`, `decision_maker`, `budget_range`
- `previous_attempts[]`, `success_criteria`
- `escalation_signals[]`

*Computed:*
- `predicted_track`, `confidence_score`
- `time_spent_minutes`, `messages_exchanged`

**События на акторе Lead:**
- `Lead.Created`
- `Discovery.Started`
- `Phase.<Name>.Completed`
- `Escalation.Triggered`
- `Prototype.Created`
- `Brief.Generated`
- `Brief.Signed`
- `Lead.Dropped`

Каждое событие генерирует Dr/Cr-транзакции на соответствующих Accounts. Воронка Discovery — это проекция на множество акторов Lead.

---

## 7. Схема актора DiscoveryAgent

AI-актор первого класса с правом подписи на events Lead.

**Идентификационные Accounts:**
- `agent_version` (для A/B-тестирования промптов)
- `llm_model`
- `system_prompt_hash`

**State:** `is_active`, `supervisor_id`.

**Накопительные Accounts (метрики):**
- `sessions_started`, `sessions_completed`, `sessions_dropped`
- `briefs_generated`, `briefs_signed`
- `conversion_rate` (briefs_signed / sessions_started)
- `avg_duration_minutes`
- `escalation_rate`
- `human_overrides_count`

Несколько версий агента могут жить параллельно (`agent_v1`, `agent_v2`) для A/B-тестирования.

---

## 8. Логика классификации трека

`classify_track` — проекция на Accounts Lead:

```python
def classify_track(lead):
    if lead.employees_count > 200: return "custom"
    if lead.industry in REGULATED: return "custom"
    if lead.has_manufacturing_bom: return "custom"
    if lead.industry_pack_match_pct < 60: return "custom"
    if lead.multi_legal_entity: return "custom"

    if (lead.employees_count <= 30
        and lead.industry_pack_match_pct >= 85
        and not lead.has_complex_specifics
        and lead.outlets_count <= 5
        and lead.has_internal_it_owner):
        return "express"

    return "standard"
```

| Трек | Срок | Команда | Параллельная работа |
|---|---|---|---|
| Express | 2-4 нед | 2-3 чел | Холодный cut-over |
| Standard | 6-10 нед | 3-4 чел | 2-3 нед параллели |
| Custom | 10-16+ нед | 5+ чел | 4-8 нед параллели |

---

## 9. Триггеры эскалации

| Триггер | Срочность | Реплика агента |
|---|---|---|
| Явный запрос человека | Immediate | «Конечно, передаю звонок solution architect.» |
| Регулируемая отрасль | Immediate | «У вас отрасль с особой спецификой.» |
| 200+ сотрудников | Immediate | То же |
| Эмоциональный негатив | Immediate | «Понимаю. Организую встречу с коллегой сегодня.» |
| Внутренний конфликт | Next BD | «Здесь нюансы координации — лучше голосом.» |
| Прежняя провалившаяся миграция | Next BD | «Ценный контекст. SA разберёт причины.» |
| Сложная специфика >40% delta | Next BD | «Кейс особенный — SA соберёт индивидуальное предложение.» |
| Юридические вопросы | Next BD | «Это за моими полномочиями.» |

---

## 10. Шаблон Discovery Brief

Brief — проекция на Accounts Lead. Структура повторяет Golden House full document для прямой развёртки в TOM.

```markdown
# Discovery Brief — {Lead.company_name}

**Дата:** {date}
**Lead actor ID:** {lead_actor_id}
**Discovery-агент:** DiscoveryAgent v{agent_version}
**Solution architect:** {assigned_solution_architect}

## 1. Резюме клиента
{Проекция на идентификационные Accounts}

## 2. Текущая ситуация
- Система: {Lead.current_system}
- Что работает / не работает: {pain / likes}
- Триггер: {Lead.migration_trigger}

## 3. Бизнес-флоу — покрытие 8 кейсов
Для каждого из case_1..case_8 — короткое описание состояния "у клиента":
- Кейс 1 (Розничная продажа): {case_1_retail_flow}
- Кейс 2 (Предоплата + ПРРО): {case_2_prepayment_flow}
- ... и т.д.

## 4. Industry Pack
- Pack: furniture_retail
- Покрытие: {Lead.industry_pack_match_pct}%
- Delta: {Lead.delta_required[]}

## 5. Объёмы и инфраструктура
{таблица из Accounts фазы Volumes}

## 6. Классификация трека
- Трек: {Lead.predicted_track}
- Обоснование: {из classify_track}

## 7. Команда и роли
- Со стороны Simulator: {роли по треку}
- Со стороны клиента: Спонсор ({Lead.decision_maker}), Owner ({Lead.internal_owner})

## 8. Риски и точки внимания
{Проекция на Lead.escalation_signals[] и Lead.previous_attempts[]}

## 9. Коммерческое предложение
{из прайса трека}

## 10. TOM blueprint (черновик для solution architect)
Auto-generated draft в формате Golden House full document — 8 кейсов
с акторами, Accounts, событиями, валентностями, состояниями,
инвариантами и Corezoid-процессами. SA доводит на финальной встрече.

## 11. Следующие шаги
- [ ] Звонок с SA — {scheduled_at}
- [ ] Подписание Brief
- [ ] Старт Фазы 1 (TOM finalization)
```

---

## 11. Roadmap MVP — 4 спринта

### Спринт 1 (Дни 1-3)
- Создать актор-схему `Lead`, `DiscoveryAgent` в Simulator с Accounts по разделам 6-7.
- Развернуть system prompt + `match_industry_pack`, `save_session`, `emit_lead_event`.
- Подключение Claude API через MCP Corezoid.

### Спринт 2 (Дни 4-7)
- Создать актор-шаблон `Furniture Retail Industry Pack` по 8 кейсам раздела 5.
- Реализовать `create_prototype_stand` — клонирование шаблона как актор `Prototype`.

### Спринт 3 (Дни 8-10)
- `classify_track` + `generate_discovery_brief` + `escalate_to_human`.
- Corezoid-процесс уведомления SA через Telegram.
- Дашборд активных Lead-акторов.

### Спринт 4 (Дни 11-14)
- Веб-виджет на simulator.company.
- Закрытое тестирование на 3-5 знакомых компаниях из мебельной отрасли.

### После MVP (Дни 15-21)
- Запуск на Golden House.
- Анализ диалога через события на акторе Lead, тюнинг промпта.

### Далее
- Industry Packs: Wholesale, Services, HoReCa, Light Manufacturing.

---

## 12. Концептуальная связка трёх документов

```
Discovery Agent Spec v1.2
        │ Discovery-агент проводит диалог
        ▼
    Lead-актор с накопленными Accounts
        │ Solution architect разворачивает на встрече
        ▼
Target Operating Model (как Golden House full document)
        │ Команда внедрения превращает в Prototype-стенд
        ▼
    MVP-демо (Спринт 2 миграции, не агента)
        │ Клиент работает → итеративная доводка
        ▼
    Production
```

**Критерий качества Discovery:** собранный Lead-актор содержит достаточно данных, чтобы SA за 1-2 встречи мог развернуть его в TOM уровня Golden House full document (8 кейсов с акторами, Accounts, событиями, валентностями, состояниями, инвариантами и Corezoid-процессами).

---

## 13. Изменения от v1.1

- Везде `Identity/State/Accumulator accounts` → `Accounts` с явными Dr/Cr-операциями.
- Добавлены **валентности** (viewer/signer/executor) на событиях.
- `time_in_state` подчёркнут как нативный Account.
- Добавлены **инварианты** на акторах (например, условие отгрузки `prepayment + final >= total`).
- Furniture Retail Industry Pack переписан **по 8 кейсам** Golden House.
- В Brief добавлен **TOM blueprint** — черновик Target Operating Model в формате Golden House full.
- В critique success: Lead-актор должен вмещать данные для развёртки в TOM уровня Golden House.

## 14. Изменения от v1.2 → v1.3

- Добавлен tool `generate_roadmap_graph` — генерация персонализированного Migration Roadmap actor graph.
- В Этапе 5 (Presentation) добавлены 2 новых шага: roadmap generation + показ клиенту персонального плана с graph/Gantt/calendar views.
- Discovery Brief теперь линкует к созданному Migration actor + roadmap_graph_id (вместо абстрактного TOM blueprint только).
- Согласовано с Master Spec v1.4 (Migration as Actor Graph с nested Phase actors).
- DiscoveryAgent теперь не только профилирует клиента — он **запускает** Migration через создание roadmap_graph.

---

**Версия:** 1.3
**Статус:** черновик для команды разработки, согласован с Master Spec v1.4
