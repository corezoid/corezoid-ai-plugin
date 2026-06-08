# Test Cases: corezoid-alias-manager & corezoid-variable-manager

**Project:** stage `510138` (develop)  
**Prerequisites:** project pulled locally via `pull-folder 510138`, `.env` contains `COREZOID_STAGE_ID=510138`

**Project processes used in tests:**

| ID | Title | Path |
|----|-------|------|
| 1307813 | Escalation | `./1307813_Escalation.conv.json` |
| 1307814 | rc - Control - Script | `./1307814_rc_-_Control_-_Script.conv.json` |
| 1307815 | /index/get | `./510139_Pages/510140__index/1307815__index_get.conv.json` |
| 1307816 | /index/send | `./510139_Pages/510140__index/1307816__index_send.conv.json` |
| 1377046 | rc - Control - Script (event Map) | `./1377046_rc_-_Control_-_Script_(event_Map).conv.json` |
| 1377048 | /index/get (event) | `./510139_Pages/531036_Event_stories/531037__index/1377048__index_get.conv.json` |
| 1377049 | /index/send (event) | `./510139_Pages/531036_Event_stories/531037__index/1377049__index_send.conv.json` |

---

## ALIAS TEST CASES

---

### TC-ALI-01 — List aliases

**Prompt:**
```
Покажи все алиасы которые есть в этом проекте
```

**Ожидаемое поведение:**
- Скилл вызывает API `list aliases` с `stage_id=510138`
- Выводит таблицу: `obj_id`, `short_name`, `title`, `obj_to_id` (на какой процесс указывает)

**Ожидаемый результат:**
```
Aliases in stage 510138:
(список пустой или показывает существующие)
```
Если алиасов нет — говорит об этом явно.

---

### TC-ALI-02 — Создать алиас для часто вызываемого процесса

**Prompt:**
```
Создай алиас для процесса Escalation (1307813)
```

**Ожидаемое поведение:**
1. Скилл находит файл `./1307813_Escalation.conv.json`
2. **Проверяет через list aliases** что `escalation` ещё не занят
3. Предлагает `short_name: "escalation"` (выводит из названия)
4. Вызывает MCP `create-alias(process_path="./1307813_Escalation.conv.json", short_name="escalation")`
5. Возвращает `alias_id`

**Ожидаемый результат:**
```
Alias 'escalation' created successfully, AliasID: <id>
```

**Проверка нарушений:**
- ❌ Если скилл не проверил список алиасов перед созданием — баг
- ❌ Если `short_name` содержит заглавные буквы или символы кроме `[a-z0-9-]` — баг
- ❌ Если скилл не вернул `alias_id` — баг

---

### TC-ALI-03 — Создать алиас с явным именем

**Prompt:**
```
Создай алиас для процесса 1307815 с именем pages-index-get
```

**Ожидаемое поведение:**
1. Находит файл `./510139_Pages/510140__index/1307815__index_get.conv.json`
2. Проверяет что `pages-index-get` свободен
3. Вызывает `create-alias(process_path="...", short_name="pages-index-get")`

**Ожидаемый результат:**
```
Alias 'pages-index-get' created successfully, AliasID: <id>
```

---

### TC-ALI-04 — Обнаружить все числовые conv_id и предложить алиасы

**Prompt:**
```
Проверь процесс rc - Control - Script (1307814) — есть ли в нём числовые conv_id без алиасов?
```

**Ожидаемое поведение:**
1. Читает `./1307814_rc_-_Control_-_Script.conv.json`
2. Находит все числовые `conv_id`:
   - `1307815` (api_rpc, 1 раз)
   - `1307816` (api_rpc, 1 раз)
   - `1307813` (api_copy, 11 раз)
   - `1254022` (api_copy, 1 раз — внешний процесс)
3. Для каждого предлагает `short_name` — но **не создаёт сам**
4. Сообщает что `1254022` — внешний (нет в проекте)

**Ожидаемый результат:**
```
Found 4 unique numeric conv_id references:
- 1307813 (Escalation) — called 11 times via api_copy — suggested alias: "escalation"
- 1307815 (/index/get) — called 1 time via api_rpc — suggested alias: "pages-index-get"
- 1307816 (/index/send) — called 1 time via api_rpc — suggested alias: "pages-index-send"
- 1254022 — 1 time via api_copy — not found in project, external dependency
```

**Проверка нарушений:**
- ❌ Если скилл автоматически вызвал `create-alias` без запроса пользователя — баг (алиас создаётся только по явному запросу)

---

### TC-ALI-05 — Попытка создать дубликат алиаса

**Prerequisite:** TC-ALI-02 выполнен, алиас `escalation` существует.

**Prompt:**
```
Создай алиас escalation для процесса Escalation
```

**Ожидаемое поведение:**
1. Вызывает list aliases
2. Видит что `escalation` уже занят
3. Сообщает об этом и предлагает альтернативное имя (например `rc-escalation`)
4. **Не создаёт дубликат**

**Ожидаемый результат:**
```
Alias 'escalation' already exists (points to process 1307813).
Suggested alternative: 'rc-escalation'. Create it instead?
```

---

### TC-ALI-06 — Изменить название алиаса

**Prerequisite:** TC-ALI-02 выполнен, существует алиас `escalation` с `alias_id: <id>`.

**Prompt:**
```
Переименуй алиас escalation в rc-escalation
```

**Ожидаемое поведение:**
1. Получает `alias_id` через list aliases
2. Вызывает API `modify` с новым `short_name: "rc-escalation"` и `title: "rc-escalation"`
3. Предупреждает что нужно обновить все `"conv_id": "@escalation"` в `.conv.json` файлах
4. Делает grep по проекту на `@escalation`

**Ожидаемый результат:**
```
Alias renamed to 'rc-escalation'.
⚠️ Check all .conv.json files for references to "@escalation" and update them.
grep found 0 references (no processes were using @escalation yet).
```

---

### TC-ALI-07 — Перепривязать алиас на другой процесс

**Prerequisite:** Алиас `pages-index-get` указывает на `1307815`.

**Prompt:**
```
Перепривяжи алиас pages-index-get на процесс 1377048
```

**Ожидаемое поведение:**
1. Получает `alias_id` через list aliases
2. Шаг 1: API `link: false` — отвязывает от `1307815`
3. Шаг 2: API `link: true` — привязывает к `1377048`

**Ожидаемый результат:**
```
Alias 'pages-index-get' successfully repointed from 1307815 to 1377048.
```

**Проверка нарушений:**
- ❌ Если скилл попытался сделать repoint одним вызовом вместо двух (unlink + link) — баг

---

### TC-ALI-08 — Получить callback hash

**Prerequisite:** Алиас `escalation` существует с `alias_id: <id>`.

**Prompt:**
```
Дай мне callback hash для алиаса escalation
```

**Ожидаемое поведение:**
1. Получает `alias_id` через list aliases
2. Вызывает API `get callback_hash`
3. Показывает hash и готовую URL для отправки задач

**Ожидаемый результат:**
```
Callback hash: 19e339a865d676db68b776f440443821c49a0e30

External task submission URL:
POST https://api-apigw.corezoid.com/api/1/json/<WORKSPACE_ID>/19e339a865d676db68b776f440443821c49a0e30

Body:
{
  "ops": [{
    "ref": "unique-ref",
    "type": "create",
    "obj": "task",
    "data": { "key": "value" }
  }]
}
```

---

### TC-ALI-09 — Удалить алиас с проверкой зависимостей

**Prerequisite:** Алиас `pages-index-get` существует.

**Prompt:**
```
Удали алиас pages-index-get
```

**Ожидаемое поведение:**
1. Делает grep по всем `.conv.json` файлам на `@pages-index-get`
2. Если ссылок нет — запрашивает подтверждение и вызывает API `delete`
3. Если ссылки есть — показывает их и просит сначала обновить

**Ожидаемый результат (если ссылок нет):**
```
No processes reference "@pages-index-get".
Alias deleted (obj_id: <id>).
```

---

## VARIABLE TEST CASES

---

### TC-VAR-01 — Список переменных

**Prompt:**
```
Покажи все переменные в этом проекте
```

**Ожидаемое поведение:**
- Вызывает API `list env_var` с `stage_id=510138`
- Выводит таблицу: `obj_id`, `short_name`, `title`, `env_var_type`, `data_type`
- Значения `secret`-переменных не показывает (только fingerprint)

**Ожидаемый результат:**
```
Variables in stage 510138:
(список или "No variables found")
```

---

### TC-VAR-02 — Создать видимую raw-переменную

**Prompt:**
```
Создай переменную для базового URL проекта: имя rc-base-url, значение https://my-project.corezoid.com
```

**Ожидаемое поведение:**
1. Проверяет `_ENV_VARS_.json` и `.processes/variables.json` — `rc-base-url` отсутствует
2. Вызывает `create-variable(stage_id="510138", name="rc-base-url", description="RC Project Base URL", value="https://my-project.corezoid.com")`
3. Обновляет `.processes/variables.json`

**Ожидаемый результат:**
```
Environment variable 'rc-base-url' created successfully.
Reference in process JSON: {{env_var[@rc-base-url]}}
```

**Проверка нарушений:**
- ❌ Если скилл не проверил оба локальных файла перед созданием — баг
- ❌ Если `short_name` в запросе содержит заглавные буквы и скилл не исправил — баг

---

### TC-VAR-03 — Создать secret-переменную через API

**Prompt:**
```
Создай секретную переменную rc-api-token со значением Bearer abc123xyz
```

**Ожидаемое поведение:**
1. Скилл замечает что нужен `secret` тип
2. Объясняет что MCP-инструмент создаёт только `visible` — нужен прямой API
3. Показывает готовый JSON-запрос с `env_var_type: "secret"` и `scopes: [{"type":"api_call","fields":"*"}]`
4. Предупреждает: значение не будет доступно после создания (только fingerprint)

**Ожидаемый результат:**
```
Secret variables require a direct API call (MCP tool only supports visible variables).

POST {COREZOID_API_URL}/api/2/json
{
  "ops": [{
    "type": "create",
    "obj": "env_var",
    "env_var_type": "secret",
    "data_type": "raw",
    "short_name": "rc-api-token",
    "title": "RC API Token",
    "value": "Bearer abc123xyz",
    "scopes": [{"type": "api_call", "fields": "*"}],
    ...
  }]
}

⚠️ Secret variables are only accessible from API Call nodes.
```

**Проверка нарушений:**
- ❌ Если скилл попытался вызвать `create-variable` для secret-переменной — баг (инструмент создаст её как visible)

---

### TC-VAR-04 — Создать JSON-переменную

**Prompt:**
```
Создай переменную rc-feature-flags типа json с таким значением:
{
  "new_design": true,
  "event_stories": false,
  "max_retries": 3
}
```

**Ожидаемое поведение:**
1. Скилл замечает `data_type: json` — нужен прямой API
2. Показывает запрос с `data_type: "json"`, value — JSON как строка
3. Использует `scopes: [{"type":"*","fields":"*"}]` (visible)

**Ожидаемый результат:**
```
JSON variables require a direct API call.

POST {COREZOID_API_URL}/api/2/json
{
  "ops": [{
    "type": "create",
    "obj": "env_var",
    "data_type": "json",
    "env_var_type": "visible",
    "short_name": "rc-feature-flags",
    "title": "RC Feature Flags",
    "value": "{\"new_design\":true,\"event_stories\":false,\"max_retries\":3}",
    "scopes": [{"type": "*", "fields": "*"}],
    ...
  }]
}
```

---

### TC-VAR-05 — Попытка создать дубликат переменной

**Prerequisite:** TC-VAR-02 выполнен, переменная `rc-base-url` существует в `.processes/variables.json`.

**Prompt:**
```
Создай переменную rc-base-url со значением https://new-project.corezoid.com
```

**Ожидаемое поведение:**
1. Читает `.processes/variables.json` — видит что `rc-base-url` уже есть
2. Сообщает об этом
3. Предлагает обновить значение через modify вместо создания нового

**Ожидаемый результат:**
```
Variable 'rc-base-url' already exists (current value: https://my-project.corezoid.com).
Use modify to update the value instead?
```

---

### TC-VAR-06 — Изменить значение переменной

**Prerequisite:** TC-VAR-02 выполнен, известен `obj_id` переменной `rc-base-url`.

**Prompt:**
```
Обнови значение переменной rc-base-url на https://prod.corezoid.com
```

**Ожидаемое поведение:**
1. Получает `obj_id` через list API
2. Вызывает API `modify` с полным payload (все поля обязательны)
3. Подтверждает успех

**Ожидаемый результат:**
```
Variable 'rc-base-url' updated successfully.
New value: https://prod.corezoid.com
```

**Проверка нарушений:**
- ❌ Если скилл отправил modify без `short_name`, `title`, `data_type`, `env_var_type` или `scopes` — баг (partial update не работает)

---

### TC-VAR-07 — Удалить переменную с проверкой зависимостей

**Prerequisite:** TC-VAR-02 выполнен, переменная `rc-base-url` создана, нигде не используется.

**Prompt:**
```
Удали переменную rc-base-url
```

**Ожидаемое поведение:**
1. Делает grep по `.conv.json` файлам на `env_var[@rc-base-url]`
2. Ссылок нет — вызывает API `delete`

**Ожидаемый результат:**
```
No processes reference {{env_var[@rc-base-url]}}.
Variable 'rc-base-url' deleted (obj_id: <id>).
```

---

### TC-VAR-08 — Удалить переменную которая используется

**Prerequisite:** Переменная `rc-base-url` создана И добавлена в один из `.conv.json` файлов как `{{env_var[@rc-base-url]}}`.

**Prompt:**
```
Удали переменную rc-base-url
```

**Ожидаемое поведение:**
1. Grep находит `env_var[@rc-base-url]` в `1307814_rc_-_Control_-_Script.conv.json`
2. Отказывается удалять — показывает список файлов с ссылками
3. Объясняет что `push-process` упадёт с ошибкой если переменная будет удалена

**Ожидаемый результат:**
```
⚠️ Cannot delete: variable '@rc-base-url' is referenced in:
  - ./1307814_rc_-_Control_-_Script.conv.json

Remove the references first, then run push-process, then delete the variable.
```

---

### TC-VAR-09 — Проверить что push-process валидирует переменные

**Prerequisite:** В `1307814_rc_-_Control_-_Script.conv.json` добавить `{{env_var[@nonexistent-var]}}` в один из узлов вручную, переменная не существует.

**Prompt:**
```
Задеплой процесс rc - Control - Script
```

**Ожидаемое поведение:**
- `push-process` завершается с ошибкой

**Ожидаемый результат:**
```
Validation failed: env variable '@nonexistent-var' referenced in process does not exist
```

**Проверка:** Это тест встроенной валидации MCP-сервера — убеждаемся что `push-process` действительно проверяет все `{{env_var[@name]}}` перед деплоем.

---

## Сводная таблица

| ID | Скилл | Операция | Сложность |
|----|-------|----------|-----------|
| TC-ALI-01 | alias | List | 🟢 простой |
| TC-ALI-02 | alias | Create (по имени процесса) | 🟢 простой |
| TC-ALI-03 | alias | Create (явный short_name) | 🟢 простой |
| TC-ALI-04 | alias | Audit numeric conv_id | 🟡 средний |
| TC-ALI-05 | alias | Duplicate prevention | 🟡 средний |
| TC-ALI-06 | alias | Modify (rename) | 🟡 средний |
| TC-ALI-07 | alias | Repoint (unlink + link) | 🔴 сложный |
| TC-ALI-08 | alias | Callback hash | 🟢 простой |
| TC-ALI-09 | alias | Delete + safety check | 🟡 средний |
| TC-VAR-01 | variable | List | 🟢 простой |
| TC-VAR-02 | variable | Create visible raw (MCP) | 🟢 простой |
| TC-VAR-03 | variable | Create secret (direct API) | 🟡 средний |
| TC-VAR-04 | variable | Create JSON (direct API) | 🟡 средний |
| TC-VAR-05 | variable | Duplicate prevention | 🟡 средний |
| TC-VAR-06 | variable | Modify | 🟡 средний |
| TC-VAR-07 | variable | Delete (no refs) | 🟢 простой |
| TC-VAR-08 | variable | Delete (with refs — blocked) | 🔴 сложный |
| TC-VAR-09 | variable | push-process validation | 🔴 сложный |
