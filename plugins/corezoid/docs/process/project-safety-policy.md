# Opt-in Project Safety Policy

The Corezoid MCP server can enforce two optional project policies before a process is pushed:

- bounded cycle safety, to control tact and budget exposure;
- strict input/output contracts, to keep process interfaces typed and documented.

Both policies are disabled when no configuration exists. This preserves the existing behavior of
the plugin and of Corezoid itself. The policy is enforced by the MCP server, so the behavior is the
same in Claude Code, Codex, Kiro, and other MCP clients.

## Enable the policy

Inspect the effective policy before creating or editing a process:

```text
show-project-policy(project_path=".")
```

Only after the user opts in, enable one or both modes:

```text
configure-project-policy(
  project_path=".",
  cycle_safety="strict",
  process_contracts="strict",
  max_cycle_iterations=100,
  max_cycle_duration_seconds=86400,
  contract_dependency_scope="project"
)
```

This creates `.corezoid/policy.json`. Commit that file when the policy should apply to every
developer and MCP client working with the project.

Version 1 policy files are limited to 4096 bytes and 32 levels of JSON nesting. `null` sections,
duplicate object keys, trailing JSON values, unknown fields, and unsupported values fail closed.
These limits are intentionally far above the normal version 1 document size while preventing a
workspace-controlled policy file from consuming unbounded parser resources.

Weakening an enabled mode, increasing an active cycle ceiling, or changing contract dependency
scope from `project` to `self` is paused until the user approves the reported change and the tool is
re-run with `confirm_policy_downgrade=true`. An external administrative floor cannot be downgraded
through this tool.

### Trust boundary

The project policy is version-controlled workspace configuration, not an operating-system access
control. A client that can write arbitrary project files can also edit `.corezoid/policy.json`
directly; the downgrade confirmation protects the `configure-project-policy` tool path, while code
review and repository permissions protect direct edits. Agents must not edit the file directly to
bypass that confirmation.

For a minimum that project-file changes cannot lower, set `COREZOID_POLICY_FILE` to an
administrator-owned read-only file outside the workspace and/or set the minimum-mode environment
variables described below. The MCP server merges that floor after reading project configuration.

Each policy supports three modes:

| Mode | Behavior |
|---|---|
| `off` | No additional analysis or push gate |
| `warn` | Report findings but do not block a push |
| `strict` | Block unconfirmed cycle risks and inconsistent contracts |

## Cycle safety

Cycle safety analyzes only nodes reachable from Start. It detects in-process graph cycles, static
recursive Call Process and Copy Task (`mode: "create"`) chains in the pulled project, and process
targets that cannot be resolved anywhere in the locally reachable call graph. Copy Task
`mode: "modify"` updates an existing task and is not part of the process-call graph.
Active Call Process Stub Mode (`obj_type: 4`) also creates no call-graph edge because runtime returns
the configured mock reply without invoking the target process.

A count-bounded cycle is proven only when all of these are true:

1. A counter is initialized to a finite non-negative number before the cycle.
2. A guard is encountered on every route around the cycle.
3. The counter is increased by a static positive integer on every route around the cycle.
4. The effective inclusive/exclusive limit does not exceed `max_iterations`.
5. No other assignment, API response mapping, Code node, Call Process output, or opaque
   data-producing action can reset or dynamically rewrite the counter.

A deadline-bounded cycle is proven only when the current-time value is refreshed on every route,
the deadline is stable for the lifetime of the cycle, its maximum duration can be proven not to
exceed `max_duration_seconds`, and every iteration crosses a statically provable Delay of at least
30 seconds. A deadline limits wall-clock time but not tact count, so an unpaced deadline loop is not
accepted as budget-safe. The supported high-assurance pattern initializes the deadline before the
cycle from `$.unixtime` or `root.change_time` plus a static number of seconds.

Count-bounded cycles containing external calls must also cross that Delay on every retry. The delay
parser accounts for seconds, minutes, hours, and days. A dynamic delay is not assumed safe because
it may resolve to the current time or to the past.

The analyzer is deliberately conservative. Arbitrary Code inside a cycle, externally supplied
deadlines, dynamic delays, API response mappings, Call Process outputs, and other actions whose
writes cannot be proven are reported as risks rather than accepted as safe. This prevents a
counter or deadline that looks bounded statically from being reset by runtime output.

### Intentional unbounded cycles

Strict mode does not prohibit an intentional long-running or unbounded cycle. `push-process`
pauses and returns a graph-specific `confirm_cycle_risk` fingerprint. Supply that exact fingerprint
only after the user accepts the extra-tact and budget risk. `force=true` does not bypass this gate.

The fingerprint includes the process graph and effective cycle policy. A graph or policy change
invalidates the old confirmation.

### Unresolved process targets

Dynamic `conv_id`, aliases, explicitly cross-project/stage calls, processes absent from the local
export, unreadable graphs, and duplicate exports remain supported. The check follows statically
resolved local calls transitively and detects cycles anywhere in that reachable call graph. If any
reachable target graph becomes unresolved, strict mode asks for a separate
`confirm_unresolved_call_risk` fingerprint. This is a risk acknowledgement, not a prohibition and
not a claim that recursion exists.

## Strict process contracts

Strict contracts treat top-level `params` as the public process interface.
They apply to `conv_type: "process"`. State diagrams hold long-lived state-task fields rather than a
callable RPC interface, so contract validation is reported as not applicable for `conv_type: "state"`;
cycle safety still analyzes their persistent callback loops. Calls or Copy Task creates targeting a
locally resolved state diagram receive an advisory rather than process-contract mapping errors.

Every parameter must contain the six Corezoid fields:

```json
{
  "name": "customer_id",
  "type": "string",
  "descr": "Stable customer identifier",
  "flags": ["required", "input"],
  "regex": "",
  "regex_error_text": ""
}
```

The validator checks:

- supported types and flags, unique names, and non-empty descriptions;
- every externally read value is declared as an input;
- every success Reply value is declared as an output with the same type;
- every required output is returned by every reachable success Reply;
- direct static Call/Copy Process mappings satisfy the callee contract;
- directly called local processes do not expose malformed contracts.

Both Reply formats are checked: object-based `key_value` and parallel name/type arrays in `keys`
mode. Call Process uses `extra/extra_type`; Copy Task `mode: "create"` uses `data/data_type`.
Copy Task `mode: "modify"` does not invoke the target process Start node, so its task update fields
are not validated as process inputs.

For active Stub Mode, condition fields participate in input inference and only outputs guaranteed by
every successful mock-reply branch are treated as locally produced. The real target's outputs are
not trusted while the Stub bypasses that process; its contract can still validate the intended call
input mapping when the target is available locally.

Code-node access through literal root keys (`data.name` or `data["name"]`) participates in input and
producer analysis. Computed root access such as `data[key]`, aliasing/destructuring the root object,
or enumerating all root keys is blocking in strict mode because it can read or write arbitrary
contract fields. Put dynamic keys inside a declared object parameter (for example
`data.payload[key]`) or use warn mode when that pattern is intentional.

With `dependency_scope: "project"`, direct numeric process targets are checked against `.conv.json`
files in the pulled project. With `self`, only the current process is checked. Dynamic targets,
aliases that are not locally resolved, unavailable external processes, duplicate local exports,
and implicit `Send all parameters` mappings are reported explicitly. Unresolved runtime mappings
remain advisory because blocking them would reject valid behavior without evidence; duplicate
numeric process IDs are blocking because choosing one contract would be ambiguous.

Static inference cannot decide business optionality. The author must still decide whether each
input and output is `required`; absence of that flag means optional.

## Administrative minimum policy

An administrator can enforce a read-only minimum policy without changing project files:

```bash
export COREZOID_POLICY_FILE=/etc/corezoid/policy.json
export COREZOID_MIN_CYCLE_SAFETY_MODE=strict
export COREZOID_MIN_PROCESS_CONTRACTS_MODE=strict
```

An external policy can only make the effective project policy stricter. Project configuration
cannot lower the administrative floor. Invalid or ambiguous policy JSON and unsupported values fail
closed and block `push-process` until corrected.

## Recommended workflow

1. Run `show-project-policy` once when starting work in a project.
2. If both modes are off, offer the user `warn` or `strict`; do not enable either silently.
3. Run `lint-process` while designing or editing.
4. Fix finite-cycle and contract findings before push where possible.
5. Ask for explicit confirmation only for a deliberate unbounded cycle or unresolved process target.
6. Run `push-process` with the returned fingerprint only after that confirmation.
