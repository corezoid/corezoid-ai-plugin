---
name: corezoid-gitcall
description: >
  Corezoid Git Call node specialist — run custom code (Python, Go, Java, PHP,
  JavaScript, Clojure, Lisp, Prolog, or a custom Dockerfile) as a step inside a
  process. Use when the user needs logic that plain nodes cannot do: parsing
  files, using external libraries, cryptography, building email/attachments, or
  any custom runtime. Activate on "git call", "gitcall", "run my code",
  "parse a file", "use a library", "custom code node", "python/go/php in a
  process", or "why does push-process hang on git_call". Load this skill only
  when the task is actually about git_call — it is not needed for ordinary flows.
---

# Corezoid Git Call

A Git Call node runs your code (9 built-in languages, or a custom Docker image)
in an isolated container as one step of a process. Each task is delivered to a
`handle` function over JSON-RPC 2.0; the value you return becomes the payload of
the next node.

## 1. When to use it — the selection rule

Git Call is one of the most constrained nodes on the platform. Use it **only**
when a step needs a capability that native nodes and a Code (`api_code`) node
cannot provide — parsing a file, an external library, cryptography, or a custom
runtime — **and** the work finishes comfortably within the observed runtime
budget. For everything else use the native alternative. "One code block is
easier to write" is not one of those capabilities.

### Runtime and resource constraints

- **Observed execution deadline:** hosted sandbox measurements terminated the
  handler at approximately 60 s, wall-clock from the moment the task entered the
  node. Keep handler work comfortably below 50 s rather than relying on the exact
  cutoff, which is an observed platform behavior rather than a portable contract.
  On overrun the task was killed and routed to the error path
  (`__conveyor_git_call_return_type_tag__ = git_call_executing_error`, description
  `usercode: timeout`). In those measurements, inline cold and warm runs ended at
  the same point; custom images may have different startup overhead.
- **Default resource allocation:** 50 MB RAM and 0.1 CPU from a pool shared by
  Git Call nodes. These are defaults, not immutable hard limits: a super-admin can
  change the allocation. Shared capacity can still cause contention under
  concurrency; one live concurrent probe observed a starved task.
- **No persistent local storage:** runtime-local files, including writable
  `/tmp`, are ephemeral and must not be used as state between runs.
- **Build-time network dependency:** Git-repo mode and dependency installation
  need network access. Runtime network access is required only when the handler
  itself calls an external service.

For work that waits, polls, or retries over time, model progress as process state
transitions (`condition`+`delay`) or resume through a callback. Those patterns do
not hold one Git Call handler open, but they remain subject to normal
platform/process limits.

### Decision gate — before adding a Git Call, confirm ALL of these

1. It genuinely cannot be done with native nodes (`set_param`, `condition`, `delay`,
   `api`/`api_rpc`, `api_code`, `db_call`, `api_sum`, `api_copy`).
2. It cannot be done in a **Code (`api_code`)** node (JS with the platform's
   built-ins) — try this first for any transform/parse/compute.
3. It comfortably finishes below the observed ~50 s working budget and the
   surrounding flow does not require tighter, predictable latency under load.
4. It is **not** a long-running loop, poll, or wait — those belong in
   process state transitions (`condition`+`delay`) or a callback, never in one
   Git Call invocation.
5. It fits the configured resource allocation and does **not** need persistent
   local state.

If any answer is "no", do **not** use Git Call.

### Use Git Call ONLY for (native nodes truly cannot):

- Parse a file (download a URL and read a 1C `.1CD`, XML, PDF, QR, image, …).
- Use an external library the platform lacks (crypto, moment, pandas, okhttp, …).
- Cryptography / bespoke binary formats the platform can't express.
- A custom runtime (custom Dockerfile) for something none of the above covers.

### NEVER use Git Call for:

- Work that might approach the observed ~50 s budget, or a latency-critical path
  that requires predictable execution time under concurrency.
- **Long-running** work — loops, polling, ret/backoff, waiting on an external
  system (→ process state with `condition`+`delay`, or a callback).
- Plain HTTP calls (→ `api`/`api_rpc`).
- Simple transforms, JSON shaping, math, string work (→ `api_code`).
- Large-data / high-memory processing that does not fit the configured shared
  resource allocation.

| Aspect        | Native nodes / `api_code` | Git Call |
|---------------|---------------------------|----------|
| Runtime model | Platform node execution | Container handler; ~60 s deadline observed in hosted tests |
| Warm-up       | None for ordinary native nodes | Container build + dispatch |
| Memory / CPU  | Platform-managed | 50 MB / 0.1 CPU defaults from a shared, configurable pool |
| Concurrency   | Node-specific | Shared capacity can introduce contention |
| State/storage | Task/process state | No persistent local storage; `/tmp` is ephemeral |
| External deps | Built-in capabilities | External libraries and custom runtimes supported |

Selection rule: **default to native nodes + `api_code`. Use Git Call only when a
file to parse, an external library, cryptography, or a custom runtime leaves no
native way to do it, and keep each invocation short, bounded, and within the
configured resource allocation.**

## 2. Supported languages and runtimes

| Language   | Version       | Package manager | OS               |
|------------|---------------|-----------------|------------------|
| JavaScript | node v20      | yarn, npm       | alpine 3.17      |
| Go         | v1.23         | go mod          | alpine 3.20      |
| Python     | v3.12         | pip             | alpine 3.17      |
| Java       | v22           | gradle          | alpine 3.15      |
| PHP        | v8.3          | composer 2.5.4  | alpine 3.17      |
| Clojure    | v1.11.1       | lein 2.10       | alpine 3.17      |
| Lisp       | v2.4.8        | roswell         | Ubuntu 18.04     |
| Prolog     | swipl v9.2.7  | swipl           | debian bullseye  |
| Dockerfile | any           | —               | your own image   |

Git Call requests originate from `54.171.15.37`, `108.128.68.222`,
`63.33.226.230`. Whitelist these if a private repo or resource blocks access.

## 3. Handler contract (per language)

The handler receives the task payload and returns the next payload (or throws to
produce an error). In Git-Repo mode the entry file is `usercode.<ext>`.

```python
# python — usercode.py
def handle(data):
    data['result'] = 'ok'
    return data
```
```javascript
// js — usercode.js  (CommonJS; for ESM use import/export default, .mjs, or "type":"module")
module.exports = (data) => { data.result = 'ok'; return data; };
```
```go
// go — usercode.go
package main
import ("context"; "github.com/corezoid/gitcall-go-runner/gitcall")
func usercode(_ context.Context, data map[string]interface{}) error { data["result"]="ok"; return nil }
func main() { gitcall.Handle(usercode) }
```
```php
// php — usercode.php
<?php
function handle($data) { $data['result']="ok"; return $data; }
```
```java
// java — Usercode.java  (fully-qualified name com.corezoid.usercode.Usercode is mandatory)
package com.corezoid.usercode;
import com.corezoid.gitcall.runner.api.UsercodeHandler;
import java.util.Map;
public class Usercode implements UsercodeHandler<Map<String,String>,Map<String,String>> {
  public Map<String,String> handle(Map<String,String> data) throws Exception { data.put("result","ok"); return data; }
}
```
```prolog
% prolog — usercode.pl
:- module(usercode, [handle/2]).
handle(Data, Result) :- put_dict(result, Data, "ok", Result).
```
```lisp
;; lisp — usercode.lisp
(defpackage #:usercode (:use #:cl) (:export :handle))
(in-package #:usercode)
(defun handle (data) (setf (gethash 'result data) 'ok) data)
```
```clojure
;; clojure — usercode.clj
(ns usercode.usercode)
(defn handle [data] (assoc data :result "ok"))
```

Runnable examples (`hello_world`, `http_request`, `user_error`, dependency demos)
for every language: <https://github.com/corezoid/gitcall-examples>.

## 4. Two modes

- **Code editor (inline)** — paste the code straight into the node.
- **Git Repo** — set the repo URL, the branch/tag/commit, the path (leave empty
  if the entry file is in the repo root), and the entry file. Use an SSH key on
  the node for private repos.

## 5. Dependencies (Build command)

Install dependencies with a Build command (Code editor) or a manifest file
(Git Repo):

| Language | Build command (Code editor)                              | Manifest (Git Repo) |
|----------|----------------------------------------------------------|---------------------|
| JS       | `npm install crypto-js@4.1.1 moment@2.29.4`              | `package.json`      |
| Python   | `pip install 'pycryptodomex==3.20'`                      | `requirements.txt`  |
| Go       | (latest versions resolved automatically)                | `go.mod`            |
| Java     | a gradle command                                         | `build.gradle` (+ `./gradlew build`) |
| PHP      | `composer require guzzlehttp/guzzle:^7.0`                | `composer.json`     |
| Clojure  | `lein change :dependencies conj '[...]' && lein install` | `project.clj`       |
| Lisp     | none — `(ql:quickload '(:cl-mustache) :silent t)` in code | —                  |
| Prolog   | `swipl -g "pack_install(matrix,[interactive(false)])."`  | (same command)      |

No dependencies → empty Build command.

## 6. Deploy with the MCP

`push-process` deploys Git Call nodes automatically (as of the git_call build
support in this plugin). Just author the node like any other and push:

1. Add a node whose logic `type` is `git_call`, set `lang` and either `code`
   (inline) or `repo`/`commit`/`path`/`script` (Git Repo).
2. `push-process` — it uploads the source, builds the container on the build
   service, and commits. Every runtime (JavaScript included) is built before the
   commit; JavaScript just builds fastest (a few seconds) since it installs no
   compiler toolchain.

Builds take ~5 s (JavaScript) to ~20–120 s (compiled runtimes, first build with
dependency install), then build from cache. `run-task` reports the task settling
on a non-final node while the build runs — that is expected; the result merges
into the task payload asynchronously.

Override the build endpoint for on-prem installs with `COREZOID_WS_URL`.

### What push-process does under the hood
The container build runs on Corezoid's build service and is driven over a
WebSocket (`wss://ws.<host>/api/1/sock_json`), authenticated with the same
Simulator access token as the HTTP API. `push-process` opens the socket after
uploading the source, sends a `monitor_show`/`function_build`/`status:"on"`
frame to start the build, keeps the socket alive (client sends `"0"`, server
answers `"1"`), waits for `log:{"type":"done"}`, then commits. You do not need to
do any of this by hand — it is only useful to know when debugging a build.

## 7. JSON-RPC 2.0 protocol

Request to your code: `{"jsonrpc":"2.0","method":"handle","id":"…","params":{…}}`.
Success: `{"jsonrpc":"2.0","id":"…","result":{…}}`. Error:
`{"jsonrpc":"2.0","id":"…","error":{"code":…,"message":"…"}}`.

For a **custom Dockerfile**, run an HTTP server on `$GIT_CALL_PORT`, handle POST
requests per JSON-RPC 2.0, run as user `501:501`, and treat the container as
read-only (`/tmp` is writable).

## 8. Errors and troubleshooting

On failure the task carries these fields (routed to the auxiliary condition
output): `__conveyor_git_call_return_type_error__` (`Hardware` = system, retry;
`Software` = code/settings), `__conveyor_git_call_return_type_tag__`
(`git_call_return_format_error`, `git_call_executing_error`,
`git_call_is_not_supported` = the node is v1, use v2, `code_return_size_overflow`,
`git_call_fatal_error`), and `__conveyor_git_call_return_type_description__`.

Common issues:

- **push-process hangs / `no response from server`** — an older plugin without
  git_call build support. Upgrade the plugin.
- **`source has to be built`** — the container was not built before commit
  (should not happen via push-process; if authoring by hand, build first).
- **`usercode module has no handle function`** — the entry `handle` is missing,
  or a stale build instance — rebuild.
- **Build fails on root access** — Corezoid forbids root; keep to allowed dirs.
- **No internet** — Git Call needs network to fetch the repo/dependencies.

## 9. Resources

Each container defaults to 100 millicpu (0.1 CPU) and 50 MB RAM from a resource
pool shared by Git Call nodes. The exact scope and allocation are
deployment-specific; ask a super-admin to inspect or adjust them before relying
on Git Call for heavier or highly concurrent workloads.

## Reference

- Examples (all languages + custom Dockerfiles): <https://github.com/corezoid/gitcall-examples>
- Go runner: <https://github.com/corezoid/gitcall-go-runner>
