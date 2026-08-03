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

Git Call is the most constrained node on the platform (hard limits below). Use it
**only** when a step needs a capability that native nodes and a Code (`api_code`)
node cannot provide — parsing a file, an external library, cryptography, or a custom
runtime — **and** the work finishes within the ~50 s budget. For everything else use
the native alternative. "One code block is easier to write" is not one of those
capabilities.

### Hard limits (verified live — do not design around, design to avoid)

- **~60 s hard execution timeout**, wall-clock from the moment the task enters the
  node. On overrun the task is killed and routed to the error path
  (`__conveyor_git_call_return_type_tag__ = git_call_executing_error`, description
  `usercode: timeout`). Usable handler time is only **~50 s** — container
  dispatch/warm-up eats into the window. (For inline code, cold and warm starts are
  the same, ~60 s; a custom Docker image can add more cold-start overhead.)
- **50 MB RAM and 0.1 CPU, shared GLOBALLY across every Git Call node** in the
  workspace. Under concurrency the pool saturates and nodes get starved — Git Call
  is not just slow, it is **unreliable under load**.
- **Stateless**: no local storage, nothing persists between runs.
- **Network required**: fetches repo/dependencies; fails offline.

Contrast: every other node can run for as long as it needs — a `condition`+`delay`
loop can poll for hours; only `api`/`api_rpc` share a per-call limit (60 s). Git
Call is the one node that will kill your logic mid-run.

### Decision gate — before adding a Git Call, confirm ALL of these

1. It genuinely cannot be done with native nodes (`set_param`, `condition`, `delay`,
   `api`/`api_rpc`, `api_code`, `db_call`, `api_sum`, `api_copy`).
2. It cannot be done in a **Code (`api_code`)** node (JS with the platform's
   built-ins) — try this first for any transform/parse/compute.
3. It is **not time-sensitive** and comfortably finishes in well under ~50 s.
4. It is **not** a long-running loop, poll, or wait — those belong in
   `condition`+`delay` (unlimited) or `api_rpc`+callback, never in Git Call.
5. It does **not** need large memory or to hold state.

If any answer is "no", do **not** use Git Call.

### Use Git Call ONLY for (native nodes truly cannot):

- Parse a file (download a URL and read a 1C `.1CD`, XML, PDF, QR, image, …).
- Use an external library the platform lacks (crypto, moment, pandas, okhttp, …).
- Cryptography / bespoke binary formats the platform can't express.
- A custom runtime (custom Dockerfile) for something none of the above covers.

### NEVER use Git Call for:

- Anything **time-sensitive** or that might approach ~50 s.
- **Long-running** work — loops, polling, ret/backoff, waiting on an external
  system (→ `condition`+`delay`, or `api_rpc`+callback; both effectively unlimited).
- Plain HTTP calls (→ `api`/`api_rpc`).
- Simple transforms, JSON shaping, math, string work (→ `api_code`).
- Large-data / high-memory processing (50 MB cap, shared).

| Aspect        | Native nodes / `api_code`     | Git Call                          |
|---------------|-------------------------------|-----------------------------------|
| Time limit    | none (api/api_rpc: 60 s)      | **~60 s hard kill (~50 s usable)** |
| Warm-up       | none                          | container build + dispatch        |
| Memory        | platform-managed              | **50 MB, shared globally**        |
| Reliability   | stable                        | **flaky under load**              |
| State/storage | task payload                  | stateless, none                   |
| External deps | no                            | yes                               |

Selection rule: **default to native nodes + `api_code`. Use Git Call only when a
file to parse, an external library, cryptography, or a custom runtime leaves no
native way to do it — and never for anything long-running or time-sensitive.**

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

Each container defaults to 100 millicpu (0.1 CPU) and 50 MB RAM, allocated
globally across all Git Call nodes. For heavy workloads (crypto, large datasets)
ask a super-admin to raise the global limit.

## Reference

- Examples (all languages + custom Dockerfiles): <https://github.com/corezoid/gitcall-examples>
- Go runner: <https://github.com/corezoid/gitcall-go-runner>
