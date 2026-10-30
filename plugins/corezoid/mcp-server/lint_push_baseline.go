package main

import (
	"fmt"
	"strings"
)

// The push lint gate judges the CHANGE, not the whole process. An overridable
// finding the pulled version already had was deployed before and is not what
// this push is about: blocking on it left force=true as the only way to ship a
// one-line edit to a process with old warnings, and a model that has to pass
// force on every push stops reading what force is waiving. So findings that
// match the pulled copy are reported, not blocked on, and force is left for
// what the edit itself introduces.
//
// Only the caller-contract findings of the overridable bucket are diffed
// (unreplied terminals, rpc reply mismatches): the server accepts those, so a
// version that had them really did deploy. Missing default go and short timers
// are rejected by the server on every deploy, as are incomplete params elements
// and Call Process data blocks, and literal reply values hang
// its commit — "the pulled version had it too" does not make the next deploy
// succeed, it only trades a clear block for a server failure past the
// concurrency gate. Those block whatever their age, like structural findings.
// Advisory findings never block to begin with.

// lintAncestor lints the scheme recorded at pull time (the merge ancestor).
// nil when none was recorded or it does not parse: every finding then counts
// as new, which is the behaviour before baselines existed.
func lintAncestor(dir string, procID int) *LintResult {
	if procID == 0 {
		return nil
	}
	conv, ok := readAncestorScheme(dir, procID)
	if !ok {
		return nil
	}
	res, err := lintProcessData([]byte(conv))
	if err != nil {
		return nil
	}
	return res
}

// splitInheritedLint returns copies of cur's overridable findings split into
// those this push introduced and those base already had. A nil base inherits
// nothing, and the kinds the server rejects are always introduced. Both
// results carry cur's header fields, so FormatLintResult prints them as a
// normal report.
func splitInheritedLint(cur, base *LintResult) (introduced, inherited *LintResult) {
	introduced, inherited = overridableHeader(cur), overridableHeader(cur)
	if base == nil {
		base = &LintResult{}
	}
	introduced.MissingDefaultGo = cur.MissingDefaultGo
	introduced.ShortTimers = cur.ShortTimers
	introduced.LiteralReplyValues = cur.LiteralReplyValues
	introduced.IncompleteParams = cur.IncompleteParams
	introduced.IncompleteCallProcess = cur.IncompleteCallProcess
	// A reply node that gains another untyped key is a new problem even though
	// the node was already flagged, so the keys are part of the identity.
	introduced.RpcReplyMismatches, inherited.RpcReplyMismatches = splitByKey(cur.RpcReplyMismatches, base.RpcReplyMismatches,
		func(f RpcReplyMismatch) string {
			return f.NodeID + "|" + f.DataField + "|" + strings.Join(f.UntypedKeys, ",") + "|" + strings.Join(f.UnusedTypes, ",")
		})
	introduced.UnrepliedTerminals, inherited.UnrepliedTerminals = splitByKey(cur.UnrepliedTerminals, base.UnrepliedTerminals,
		func(f UnrepliedTerminal) string { return f.ID })
	return introduced, inherited
}

// overridableHeader is an empty result carrying cur's report header. Schema
// validity is not this gate's question — push validated the schema already.
func overridableHeader(cur *LintResult) *LintResult {
	return &LintResult{ProcessTitle: cur.ProcessTitle, TotalNodes: cur.TotalNodes, ReachableCount: cur.ReachableCount, SchemaValid: true}
}

func splitByKey[T any](cur, base []T, key func(T) string) (fresh, old []T) {
	seen := make(map[string]bool, len(base))
	for _, f := range base {
		seen[key(f)] = true
	}
	for _, f := range cur {
		if seen[key(f)] {
			old = append(old, f)
		} else {
			fresh = append(fresh, f)
		}
	}
	return fresh, old
}

// inheritedLintNote is the one line a push result spends on findings it did
// not block on because the pulled version already had them.
func inheritedLintNote(n int, blocked bool) string {
	if n == 0 {
		return ""
	}
	if blocked {
		return fmt.Sprintf(" %d further finding(s) of these kinds were already in the pulled version; they do not block this push and are not listed (run lint-process for the full report).", n)
	}
	return fmt.Sprintf(" %d finding(s) that block new changes were already in the pulled version, so they did not block this push.", n)
}
