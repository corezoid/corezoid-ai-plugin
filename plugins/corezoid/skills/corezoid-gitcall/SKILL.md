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

Reach for Git Call only when the standard nodes cannot do the job. It is heavier
than a Code (`api_code`) node — it needs a container build and a warm-up.

## 1. When to use it

Use Git Call when you need something the platform's built-in nodes lack:

- Parse files (download a URL and read a 1C `.1CD`, XML, PDF, QR, image, …).
- Use external libraries (crypto, moment, pandas, okhttp, …).
- Heavy/custom logic (matrices, cryptography, bespoke formats).
- Build an email body with attachments, generate documents, etc.

| Aspect        | Code node (`api_code`) | Git Call            |
|---------------|------------------------|---------------------|
| Speed         | faster                 | slower              |
| Warm-up       | none                   | required            |
| Complexity    | simple                 | higher              |
| Resources     | efficient              | heavier (container) |
| External deps | no                     | yes                 |
| Languages     | JS (+ limited)          | 9 languages + Docker |

Rule of thumb: simple and fast with no external deps → Code node. Files,
libraries, or custom runtimes → Git Call.

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
