# Smart Company Onramp — Sign-off Chart v1.1

**Документ:** approval roadmap
**Дата:** 17 мая 2026
**Цель:** структурированный список решений и артефактов, требующих явного утверждения перед запуском Onramp в production. Указаны decision-makers, due dates, и зависимости.
**Изменение от v1.0:** добавлены S15 (Pause/Resume strategy) и S16 (Migration Roadmap as Actor Graph + Onramp Process pack) — связано с обновлениями Master Spec до v1.3 и v1.4.

---

## 0. Принципы

**Каждое решение имеет одного primary decision-maker** (single accountable). Остальные могут быть consulted или informed, но не подписывают.

**Каждое sign-off — это event на акторе `OnrampLaunch` в Simulator** с валентностью signer. Дата подписи = timestamp event'а.

**Без подписи на upstream decision нельзя начинать downstream работу.** Это явно выраженные зависимости в графе.

**Re-sign требуется при существенных изменениях.** Если меняется L2 прайс — это новый event с новой подписью.

---

## 1. Roles

| Role | Кто | Что подписывает |
|---|---|---|
| **Founder** | Alexander Vityaz | Vision, product, brand, strategic roadmap, partner program |
| **Tech Lead** | TBD (внутри команды Simulator) | Технические specs, архитектура агентов, security |
| **Sales Lead / Commercial** | TBD | Pricing, commercial structure, контракты |
| **Solution Architects Lead** | Никита Lugovoyy | Industry Pack content, methodology, internal training |
| **Marketing** | TBD | Public communications, лендинг, whitepaper'ы |
| **Operations** | TBD | Партнёрская программа, supervisor-операции |
| **Legal** | внешний consultant | Партнёрские договора, KEP/compliance, customer contracts |

Если позиция пустая (TBD) — Founder fills the role до hire'а.

---

## 2. Sign-off Tree

```
┌─────────────────────────────────────────────────┐
│ S1. Vision & Brand (Founder)                     │
└─────────────────────────────────────────────────┘
                  │
                  ├──┬──────────────────────────────┐
                  │  │                              │
┌─────────────────▼┐ │                              │
│ S2. Master Spec  │ │                              │
│ (Founder + Tech) │ │                              │
└─────────────────┬┘ │                              │
                  │  │                              │
        ┌─────────┼──┴────────────┐                 │
        │         │               │                 │
┌───────▼──┐ ┌────▼─────┐  ┌──────▼─────┐ ┌────────▼────────┐
│ S3.      │ │ S4.      │  │ S5.        │ │ S6. Brand       │
│ Tech     │ │ Pricing  │  │ Partner    │ │ & Marketing     │
│ roadmap  │ │ (Sales)  │  │ program    │ │ (Marketing)     │
│ (Tech)   │ │          │  │ (Ops)      │ │                 │
└───────┬──┘ └────┬─────┘  └──────┬─────┘ └─────────────────┘
        │        │                │
   ┌────┼────┐   │                │
   │    │    │   │                │
┌──▼─┐ ┌▼──┐ ┌▼──┐                │
│ S7 │ │S8 │ │S9 │                │
│Pack│ │Pri│ │... │                │
│s   │ │ce │ │   │                │
└────┘ └───┘ └───┘                │
                                  │
                          ┌───────▼──────┐
                          │ S10. Legal   │
                          │ (Legal)      │
                          └──────────────┘
```

---

## 3. Detailed sign-off items

### S1 — Vision & Brand (must be first)

**Решение:** утвердить позиционирование Smart Company Onramp как продуктовое имя и слоган «From API to KPI».

**Detail:**
- Название «Smart Company Onramp» (или альтернатива) — финальное.
- Слоган «From API to KPI» (уже на лендинге) — закрепить как official tagline.
- Категория продукта: «Universal Software Migration to Smart Company» или другая формулировка.
- Visual identity: цветовая палитра, типография — согласовать с лендингом.

**Primary decision-maker:** Founder (Alexander).
**Consulted:** Marketing, Tech Lead.
**Due date:** ASAP (блокирует всё последующее).
**Артефакт sign-off:** event `Onramp.BrandApproved` с payload {brand_name, tagline, palette_link}.

---

### S2 — Master Spec (Onramp Master Spec v1.0)

**Решение:** утвердить master spec как обязующий внутренний документ.

**Detail:**
- Архитектура: 5 фаз × 9 AI-агентов как final.
- Migration как актор первого класса.
- 4 трека (L1-L4) как продуктовая структура.
- Принципиальная schema accounts на акторе Migration.

**Primary decision-maker:** Founder + Tech Lead (joint).
**Consulted:** Solution Architects Lead.
**Due date:** Week 1 после S1.
**Артефакт sign-off:** event `Onramp.MasterSpecApproved`. Версия v1.0 → frozen, изменения в v1.1+ через формальный change request.
**Зависимости:** S1.

---

### S3 — Technical roadmap (Q3 2026 — Q4 2027)

**Решение:** утвердить sequence сборки 9 AI-агентов + Industry Packs + Onramp-инфраструктуры.

**Detail:**
- Q3 2026: DiscoveryAgent + Furniture Retail Pack.
- Q4 2026: SystemProfilerAgent + DataMigrator + 2-й Pack.
- Q1 2027: ValidationAgent + CutoverAgent + 3-й Pack.
- Q2 2027: GraphSynthesizer + MappingAgent + ProcessMigrator + EvolutionAgent + 4-й Pack.
- Capacity план: сколько разработчиков на каждый sprint.

**Primary decision-maker:** Tech Lead.
**Consulted:** Founder, Solution Architects Lead.
**Due date:** Week 2 после S2.
**Артефакт sign-off:** event `Onramp.TechRoadmapApproved` с детальным quarterly breakdown.
**Зависимости:** S2.

---

### S4 — Pricing structure для 4 треков

**Решение:** утвердить публичные цены на L1/L2/L3 и framework для L4.

**Detail:**
- L1 onboarding fee, subscription/month, included scope.
- L2 onboarding fee, subscription/month, scope.
- L3 onboarding fee, subscription/month, scope.
- L4 — framework для индивидуальных контрактов.
- Discount policy (обычно нет на onboarding, есть на multi-year subscription).
- Currency и оплата (UAH / EUR / USD).
- Условия refund и cancellation.

**Primary decision-maker:** Sales Lead.
**Consulted:** Founder, Operations.
**Due date:** Week 3 после S2.
**Артефакт sign-off:** event `Onramp.PricingApproved` с явными числами.
**Зависимости:** S2.

**Note:** прайс публичный на сайте (кроме L4). Это убирает торг и ускоряет цикл сделки.

---

### S5 — Партнёрская программа

**Решение:** утвердить структуру и rules engagement для партнёров.

**Detail:**
- Три уровня: Implementer, Pack Owner, Reseller.
- Криterii сертификации (training requirements, exam).
- Маржа / revenue share на каждом уровне (от подписки клиентов, не от onboarding).
- Эксклюзивность (территория / vertical).
- Брендинг (могут ли партнёры использовать «Powered by Simulator»).
- Поддержка партнёров: каналы коммуникации, SLA на ответ.
- Расторжение и offboarding.

**Primary decision-maker:** Operations Lead.
**Consulted:** Founder, Sales, Legal.
**Due date:** Q1 2027 (за квартал до запуска партнёрской сети).
**Артефакт sign-off:** event `Onramp.PartnerProgramApproved`.
**Зависимости:** S2, S4.

---

### S6 — Marketing & public communications

**Решение:** утвердить план marketing-кампании запуска Onramp.

**Detail:**
- Whitepaper «Smart Company Onramp» под лендинг (если решено делать).
- Лендинг simulator.company — изменения под Onramp positioning.
- Каналы публикации: ResearchGate, HBR, Habr, LinkedIn, Twitter.
- Кейс Golden House — план публикации (после положительного go-live).
- Speaker engagement: conferences, podcasts.
- Бюджет marketing на квартал.

**Primary decision-maker:** Marketing Lead.
**Consulted:** Founder.
**Due date:** Q3 2026 (синхронно с DiscoveryAgent MVP).
**Артефакт sign-off:** event `Onramp.MarketingPlanApproved`.
**Зависимости:** S1.

---

### S7 — Industry Pack каталог

#### S7.1 Furniture Retail (готов)

**Решение:** утвердить Furniture Retail Pack v1.0 как production-ready.

**Detail:**
- 15 акторов, 8 типовых кейсов (по Golden House full document).
- Состав интеграций.
- Шаблоны документов.
- Дашборды.
- Семпл-данные для prototype.

**Primary decision-maker:** Solution Architects Lead (Никита).
**Consulted:** Tech Lead.
**Due date:** до запуска Golden House как первого клиента.
**Артефакт sign-off:** event `Pack.FurnitureRetail.v1.Approved`.

#### S7.2 — Roadmap последующих packs

**Решение:** утвердить priority order для 8 следующих packs.

**Detail:**
- Wholesale FMCG (Q3 2026)
- Auto Parts (Q3 2026)
- Services / Studios (Q4 2026)
- HoReCa (Q4 2026)
- Light Manufacturing (Q1 2027)
- Construction Materials (Q1 2027)
- Apparel & Footwear (Q2 2027)
- Pharmacy (Q2 2027)

**Primary decision-maker:** Solution Architects Lead.
**Consulted:** Sales (market demand), Founder.
**Due date:** Week 4 после S2.
**Артефакт sign-off:** event `Onramp.PackRoadmapApproved`.
**Зависимости:** S2.

---

### S8 — Каждый AI-агент spec — отдельный sign-off

По мере появления detailed spec'ов:

| Agent | Spec status | Sign-off owner | Due date |
|---|---|---|---|
| DiscoveryAgent | v1.2 ready | Tech Lead | Week 2 |
| SystemProfilerAgent | v1.0 ready | Tech Lead | Week 4 |
| GraphSynthesizer | TBD | Tech Lead | Q2 2027 |
| MappingAgent | TBD | Tech Lead | Q2 2027 |
| DataMigrator | TBD | Tech Lead | Q4 2026 |
| ProcessMigrator | TBD | Tech Lead | Q2 2027 |
| ValidationAgent | TBD | Tech Lead | Q1 2027 |
| CutoverAgent | TBD | Tech Lead | Q1 2027 |
| EvolutionAgent | TBD | Tech Lead | Q2 2027 |

Каждый spec sign-off → event `Agent.<Name>.Spec.Approved`.
**Зависимости:** S2, S3.

---

### S9 — Pack Owner как новая роль

**Решение:** утвердить Pack Owner как official position в команде Simulator или партнёрской сети.

**Detail:**
- Job description (responsibilities, expertise, time commitment).
- Compensation model (share от продаж pack'а или fixed).
- Required certification.
- Pack Owner может быть внутренним сотрудником или партнёром.

**Primary decision-maker:** Founder + Operations.
**Consulted:** Sales, Tech Lead.
**Due date:** Q4 2026 (перед запуском 2-3 packs).
**Артефакт sign-off:** event `Onramp.PackOwnerRoleDefined`.
**Зависимости:** S5, S7.

---

### S10 — Legal framework

**Решение:** утвердить юридический пакет.

**Detail:**
- Customer subscription agreement template.
- Customer onboarding contract template.
- Partner agreement (3 уровня).
- Data Processing Agreement (GDPR / украинский аналог).
- KEP terms (использование Дія).
- IP rights в Industry Packs (кто владелец, что делать с custom packs Pack Owner'ов).
- Liability и SLA в случае неудачной миграции.

**Primary decision-maker:** Founder + Legal.
**Consulted:** Sales.
**Due date:** Q3 2026 (перед первой коммерческой сделкой).
**Артефакт sign-off:** event `Onramp.LegalFrameworkApproved`.
**Зависимости:** S2, S4, S5.

---

### S11 — Operational dashboard и supervisor процесс

**Решение:** утвердить, как команда Simulator мониторит активные миграции.

**Detail:**
- Дашборд активных Migrations (актор-проекция).
- Роль Migration Supervisor (кто и как).
- SLA на support активным клиентам.
- Escalation matrix.

**Primary decision-maker:** Operations Lead.
**Consulted:** Tech Lead, Solution Architects.
**Due date:** Q3 2026 (одновременно с DiscoveryAgent MVP).
**Артефакт sign-off:** event `Onramp.OperationsApproved`.
**Зависимости:** S2, S3.

---

### S12 — Public launch decision

**Решение:** разрешение запустить Onramp для широкой клиентской аудитории.

**Detail:**
- Прохождение всех upstream sign-offs.
- Validation: 3-5 успешных пилотов.
- Marketing-кампания готова.
- Operations team укомплектована.
- Legal framework на месте.

**Primary decision-maker:** Founder.
**Consulted:** все Lead'ы.
**Due date:** не раньше Q4 2026.
**Артефакт sign-off:** event `Onramp.PublicLaunchApproved`.
**Зависимости:** ВСЕ предыдущие.

---

### S15 — Pause/Resume strategy + ResumptionAgent (новое в v1.1)

**Решение:** утвердить подход к обработке прерванных миграций — pause mechanics, resume options, freshness assessment, 10-й AI-агент.

**Detail:**
- PAUSED как first-class state на акторе Migration.
- ResumptionAgent (10-й агент) как cross-phase actor для resume циклов.
- 4 resume options: Continue / Refresh & Continue / Restart from phase / Restart all.
- Freshness assessment по компонентам (Discovery Brief, System Profiles, Mappings, Migrated data, Validation window, Team contacts, Industry Pack version).
- Pause timeout escalation table (< 7d → quiet, > 180d → restart only, > 365d → archived).
- Retention при restart — prior знания (Lead, Discovery Briefs, System Profiles) сохраняются.
- Pause-aware billing — paused subscription model, refund provisions.

**Primary decision-maker:** Founder + Tech Lead + Sales Lead (joint).
**Consulted:** Solution Architects Lead.
**Due date:** Week 2 после S2 (вместе с master spec sign-off).
**Артефакт sign-off:** event `Onramp.PauseResumeStrategyApproved`.
**Зависимости:** S2 (master spec).

**Связан с Master Spec v1.3.**

---

### S16 — Migration Roadmap as Actor Graph + Onramp Process pack (новое в v1.1)

**Решение:** утвердить двухуровневую архитектуру Migration (overall actor + nested roadmap_graph из Phase actors) и концепцию Onramp Process pack как meta-Industry-Pack.

**Detail:**
- Migration становится двухуровневой структурой: overall control accounts + nested actor graph из первоклассных Phase actors.
- Каждая Phase actor имеет identity (phase_name, owner_agent, depends_on, target_date, signoffs_required), state (status, blocked_reason), accumulator (progress_pct, time_spent, deviation).
- DiscoveryAgent в Этапе 5 (Presentation) генерирует initial roadmap_graph через клонирование template из Onramp Process pack + кастомизация под клиента.
- SA может править roadmap в течение Migration (add custom phases, block, reschedule, reorder).
- Onramp Process pack — meta-Industry-Pack про сам процесс миграции. 4 default templates (L1-L4). Эволюционирует на основе реальных миграций.
- Forecasting cutover_date в real-time на основе текущей velocity.
- Roadmap visualization (graph/Gantt/calendar) для клиента и команды.

**Primary decision-maker:** Founder + Tech Lead + Solution Architects Lead (joint).
**Consulted:** Sales Lead (UX implications для sales-demo).
**Due date:** Week 2 после S2.
**Артефакт sign-off:** event `Onramp.RoadmapArchitectureApproved`.
**Зависимости:** S2 (master spec).

**Связан с Master Spec v1.4. Это значимое архитектурное решение** — превращает Migration из state machine в navigable executable graph, что является самореференцией второго уровня (Onramp моделирует свой процесс как actor graph в Simulator).

---

## 4. Sequence diagram (sign-off timeline)

```
Week 1:                S1 (Brand & Vision)
Week 2:                S2 (Master Spec)
Week 3:                S3 (Tech Roadmap) ──┐
                       S6 (Marketing)      │
Week 4:                S4 (Pricing)        │
                       S7.2 (Pack roadmap) │
                       S8 (DiscoveryAgent) │
                                           │
Q3 2026 (Aug-Sep):     S10 (Legal)         │
                       S11 (Operations)    │
                       S7.1 (Furniture)    │
                       S8 (SystemProfiler) │
                                           │
Q4 2026 (Oct-Dec):     S9 (Pack Owner)     │
                       S8 (DataMigrator)   │
                       S12 (Public Launch) │
                                           │
Q1 2027 (Jan-Mar):     S5 (Partners)       │
                       S8 (Validation,     │
                           Cutover)        │
                                           │
Q2 2027 onwards:       S8 (остальные)      │
                       Pack roadmap items  │
                                           │
                                           ▼
                              ongoing operations
```

---

## 5. Sign-off mechanics

### 5.1 Как осуществляется sign-off

Sign-off = подписанный документ + соответствующий event на акторе `OnrampLaunch` в Simulator. Подписи — через КЕП (Дія.Підпис) для UA, electronic signature для других.

Event payload включает:
- `signer_id` (identity актора Employee, кто подписал)
- `decision_text` (что именно утверждено)
- `attached_document_url` (PDF с деталями)
- `valid_from` (когда вступает в силу)
- `re_signature_required_if_changes` (что триггерит revisit)

### 5.2 Audit trail

Дашборд sign-offs — это проекция на акторы `OnrampLaunch` с фильтрами:
- По статусу (pending / signed / overdue / expired).
- По responsible role.
- По due date.

В любой момент можно увидеть, **что блокирует запуск Onramp** или **что задерживает следующий milestone**.

### 5.3 Re-signature triggers

| Item | Что триггерит re-sign |
|---|---|
| S1 (Brand) | Изменение названия или слогана |
| S2 (Master Spec) | Версия меняется с v1.x на v2.0 (значительные правки архитектуры) |
| S3 (Tech roadmap) | Сдвиг квартального milestone на >1 месяц |
| S4 (Pricing) | Любое изменение прайса |
| S5 (Partners) | Изменение revenue share или уровней |
| S7 (Packs) | Major version новых packs |
| S8 (Agents) | Major version specs агентов |
| S10 (Legal) | Изменение в законодательстве UA или EU |

### 5.4 Owner accountability

Если sign-off overdue >2 недели — automatic Telegram-уведомление primary decision-maker'у + founder. Это явно фиксируется на акторе Employee как `delayed_signoffs_count`.

---

## 6. Что мне нужно от вас сейчас (Alexander)

Чтобы продвинуть Onramp в production, в следующие 2-3 недели нужны 4 sign-off'а от вас лично (Founder responsibilities):

### Immediate (this week)
- [ ] **S1 — Brand & Vision.** Согласовать имя «Smart Company Onramp» или предложить альтернативу. Подтвердить «From API to KPI» как official tagline.

### Week 2
- [ ] **S2 — Master Spec v1.0.** Прочитать `smart_company_onramp_master_spec_v1.md`, подтвердить или прислать список изменений. После v1.0 — freeze, дальнейшие изменения через v1.1+.

### Week 3
- [ ] **S3 — Tech Roadmap.** Согласовать quarterly breakdown сборки 9 агентов и Industry Packs. Уточнить capacity (сколько разработчиков на спринт).
- [ ] **S6 — Marketing approach.** Подтвердить, делаем ли whitepaper под лендинг или нет, и какие каналы публикации использовать.

### Перед Q3 2026
- [ ] Hire или назначить TBD-роли: Tech Lead, Sales Lead, Marketing Lead, Operations.

---

## 7. Что важно концептуально

1. **Sign-off chart — это сам по себе актор-граф.** Sign-offs живут как events на акторе OnrampLaunch с валентностями signer. Это не Excel-список, а часть платформы.

2. **Граф зависимостей явный.** Нельзя начинать downstream работу без upstream sign-off. Это блокирует «мы по дороге решим».

3. **Каждое решение имеет одного accountable.** Это не комитет. Consulted и informed — да, но подписывает один человек.

4. **Re-signature правила явные.** Меняется prices — новый sign-off. Это создаёт healthy friction против постоянных изменений без обоснования.

5. **Audit trail встроен.** В любой момент видно, что блокирует прогресс. Дашборд sign-offs = операционный инструмент.

---

**Версия:** 1.1
**Статус:** approval roadmap для Founder и leads, обновлён с S15 (Pause/Resume) и S16 (Migration Roadmap)
**Применение:** запуск Smart Company Onramp в production
