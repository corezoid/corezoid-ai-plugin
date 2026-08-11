package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func safetyTestNode(id, title string, objType float64, logics, sems []interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id": id, "title": title, "obj_type": objType,
		"condition": map[string]interface{}{"logics": logics, "semaphors": sems},
	}
}

func safetyGo(to string) map[string]interface{} {
	return map[string]interface{}{"type": "go", "to_node_id": to}
}

func safetyStrictPolicy(root string, max int) EffectiveProjectPolicy {
	p := defaultProjectPolicy()
	p.CycleSafety.Mode = policyModeStrict
	p.CycleSafety.MaxIterations = max
	return EffectiveProjectPolicy{ProjectPolicy: p, Root: root, Configured: true}
}

func boundedRetryProcess(limit int, withDelay bool) (map[string]interface{}, []processNode) {
	retryTarget := "increment"
	if withDelay {
		retryTarget = "delay"
	}
	nodes := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("init")}, nil),
		safetyTestNode("init", "Initialize retry", 0, []interface{}{
			map[string]interface{}{"type": "set_param", "extra": map[string]interface{}{"retry_count": float64(0)}, "extra_type": map[string]interface{}{"retry_count": "number"}},
			safetyGo("api"),
		}, nil),
		safetyTestNode("api", "Call provider", 0, []interface{}{
			map[string]interface{}{"type": "api", "err_node_id": "guard"},
			safetyGo("success"),
		}, nil),
		safetyTestNode("guard", "Retry below limit", 3, []interface{}{
			map[string]interface{}{
				"type": "go_if_const", "to_node_id": retryTarget,
				"conditions": []interface{}{map[string]interface{}{"param": "{{retry_count}}", "const": float64(limit), "fun": "less", "cast": "number"}},
			},
			safetyGo("error"),
		}, nil),
		safetyTestNode("increment", "Increment retry", 0, []interface{}{
			map[string]interface{}{"type": "set_param", "extra": map[string]interface{}{"retry_count": "{{$.math(retry_count+1)}}"}, "extra_type": map[string]interface{}{"retry_count": "number"}},
			safetyGo("api"),
		}, nil),
		safetyTestNode("success", "Success", 2, nil, nil),
		safetyTestNode("error", "Retry exhausted", 2, nil, nil),
	}
	if withDelay {
		delay := safetyTestNode("delay", "Retry delay", 0, nil, []interface{}{
			map[string]interface{}{"type": "time", "value": float64(30), "dimension": "sec", "to_node_id": "increment"},
		})
		nodes = append(nodes[:4], append([]interface{}{delay}, nodes[4:]...)...)
	}
	proc := map[string]interface{}{"obj_id": float64(100), "params": []interface{}{}, "scheme": map[string]interface{}{"nodes": nodes}}
	return proc, parseProcessNodes(nodes)
}

func TestCycleSafety_CountBoundAccepted(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, true)
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if len(report.Cycles) != 1 {
		t.Fatalf("expected one cycle, got %+v", report.Cycles)
	}
	cycle := report.Cycles[0]
	if !cycle.Bounded || cycle.BoundKind != "count" || !cycle.HasDelay {
		t.Fatalf("expected bounded count retry with delay, got %+v", cycle)
	}
	if cycleRiskCount(report) != 0 {
		t.Fatalf("bounded cycle must not require confirmation: %+v", report)
	}
}

func TestCycleSafety_CountAboveProjectCeilingRequiresConfirmation(t *testing.T) {
	proc, nodes := boundedRetryProcess(11, true)
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 || report.CycleRiskFingerprint == "" {
		t.Fatalf("expected one cycle risk with fingerprint, got %+v", report)
	}
	if !strings.Contains(report.Cycles[0].Issue, "no finite counter") {
		t.Fatalf("unexpected issue: %+v", report.Cycles[0])
	}
}

func TestCycleSafety_FingerprintIsFullSHA256AndDeterministic(t *testing.T) {
	proc, nodes := boundedRetryProcess(11, true)
	policy := safetyStrictPolicy(t.TempDir(), 10)
	first := analyzeCycleSafety(proc, nodes, policy).CycleRiskFingerprint
	second := analyzeCycleSafety(proc, nodes, policy).CycleRiskFingerprint
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(first) {
		t.Fatalf("expected a full SHA-256 confirmation fingerprint, got %q", first)
	}
	if first != second {
		t.Fatalf("same graph produced non-deterministic fingerprints: %q != %q", first, second)
	}
	proc["title"] = "changed"
	// The title is part of the human-readable call-graph evidence being
	// confirmed, so changing it must rotate the exact confirmation token.
	if changed := analyzeCycleSafety(proc, nodes, policy).CycleRiskFingerprint; changed == first {
		t.Fatalf("changed confirmation evidence retained fingerprint %q", first)
	}
}

func FuzzCycleSafetyGraphDoesNotPanic(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4})
	f.Add([]byte{255, 255, 255})
	f.Fuzz(func(t *testing.T, edges []byte) {
		if len(edges) == 0 || len(edges) > 256 {
			return
		}
		nodes := make([]processNode, len(edges))
		for i, edge := range edges {
			id := strconv.Itoa(i)
			target := strconv.Itoa(int(edge) % len(edges))
			objType := float64(0)
			if i == 0 {
				objType = 1
			}
			nodes[i] = processNode{id: id, title: id, objType: objType, logics: []map[string]interface{}{safetyGo(target)}}
		}
		proc := map[string]interface{}{"obj_id": float64(1), "scheme": map[string]interface{}{"nodes": []interface{}{}}}
		_ = analyzeCycleSafety(proc, nodes, safetyStrictPolicy("", 100))
	})
}

func benchmarkCycleSafetyGraph(b *testing.B, size int, closeCycle bool) {
	nodes := make([]processNode, size)
	for i := range nodes {
		id := strconv.Itoa(i)
		objType := float64(0)
		if i == 0 {
			objType = 1
		}
		var logics []map[string]interface{}
		if i+1 < len(nodes) {
			logics = []map[string]interface{}{safetyGo(strconv.Itoa(i + 1))}
		}
		nodes[i] = processNode{id: id, title: id, objType: objType, logics: logics}
	}
	if closeCycle && size > 2 {
		nodes[size-1].logics = []map[string]interface{}{safetyGo("1")}
	}
	proc := map[string]interface{}{"obj_id": float64(1), "scheme": map[string]interface{}{"nodes": []interface{}{}}}
	policy := safetyStrictPolicy("", 100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = analyzeCycleSafety(proc, nodes, policy)
	}
}

func BenchmarkCycleSafety_ThousandNodeGraph(b *testing.B) {
	benchmarkCycleSafetyGraph(b, 1000, false)
}

func BenchmarkCycleSafety_TenThousandNodeGraph(b *testing.B) {
	benchmarkCycleSafetyGraph(b, 10_000, false)
}

func BenchmarkCycleSafety_TenThousandNodeUnboundedCycle(b *testing.B) {
	benchmarkCycleSafetyGraph(b, 10_000, true)
}

func TestGraphDominators(t *testing.T) {
	tests := []struct {
		name  string
		nodes []processNode
		node  string
		want  []string
	}{
		{
			name: "chain",
			nodes: []processNode{
				{id: "start", objType: 1, logics: []map[string]interface{}{safetyGo("middle")}},
				{id: "middle", logics: []map[string]interface{}{safetyGo("end")}},
				{id: "end"},
			},
			node: "end",
			want: []string{"start", "middle", "end"},
		},
		{
			name: "diamond",
			nodes: []processNode{
				{id: "start", objType: 1, logics: []map[string]interface{}{safetyGo("left"), safetyGo("right")}},
				{id: "left", logics: []map[string]interface{}{safetyGo("join")}},
				{id: "right", logics: []map[string]interface{}{safetyGo("join")}},
				{id: "join"},
			},
			node: "join",
			want: []string{"start", "join"},
		},
		{
			name: "multiple starts",
			nodes: []processNode{
				{id: "start-a", objType: 1, logics: []map[string]interface{}{safetyGo("join")}},
				{id: "start-b", objType: 1, logics: []map[string]interface{}{safetyGo("join")}},
				{id: "join"},
			},
			node: "join",
			want: []string{"join"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := graphDominators(buildSafetyGraph(tc.nodes)).candidates(tc.node)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("dominators for %q = %v, want %v", tc.node, got, tc.want)
			}
		})
	}
}

func TestCycleSafety_MultipleStartsRespectCounterInitialization(t *testing.T) {
	t.Run("bounded cycle reachable from second start", func(t *testing.T) {
		proc, nodes := boundedRetryProcess(3, true)
		nodes[0].logics = []map[string]interface{}{safetyGo("success")}
		nodes = append(nodes, processNode{
			id: "second-start", title: "Second Start", objType: 1,
			logics: []map[string]interface{}{safetyGo("init")},
		})

		report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
		if cycleRiskCount(report) != 0 || len(report.Cycles) != 1 || !report.Cycles[0].Bounded {
			t.Fatalf("cycle initialized on every path must be bounded: %+v", report.Cycles)
		}
	})

	t.Run("bypass path invalidates counter proof", func(t *testing.T) {
		proc, nodes := boundedRetryProcess(3, true)
		nodes = append(nodes, processNode{
			id: "bypass-start", title: "Bypass Start", objType: 1,
			logics: []map[string]interface{}{safetyGo("api")},
		})

		report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
		if cycleRiskCount(report) != 1 || len(report.Cycles) != 1 || report.Cycles[0].Bounded {
			t.Fatalf("a path bypassing counter initialization must remain risky: %+v", report.Cycles)
		}
	})
}

func TestCycleSafety_NonFiniteNumbersAreNeverProof(t *testing.T) {
	for _, value := range []interface{}{math.NaN(), math.Inf(1), "NaN", "+Inf"} {
		if parsed, ok := numericLiteral(value); ok {
			t.Fatalf("non-finite value %v was accepted as %v", value, parsed)
		}
	}
}

func TestCycleSafety_CountGuardRequiresNumericCast(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, true)
	for i := range nodes {
		if nodes[i].id == "guard" {
			conditions := nodes[i].logics[0]["conditions"].([]interface{})
			delete(conditions[0].(map[string]interface{}), "cast")
		}
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("an untyped comparison must not prove a numeric iteration bound: %+v", report.Cycles)
	}
}

func TestCycleSafety_CountGuardRejectsMixedActionNode(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, true)
	for i := range nodes {
		if nodes[i].id == "guard" {
			nodes[i].logics = append(nodes[i].logics, map[string]interface{}{
				"type": "set_param", "extra": map[string]interface{}{"seen": true}, "extra_type": map[string]interface{}{"seen": "boolean"},
			})
		}
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("a mixed action/condition node is not a canonical guard: %+v", report.Cycles)
	}
}

func TestCycleSafety_InclusiveLimitAccountsForExtraIteration(t *testing.T) {
	proc, nodes := boundedRetryProcess(10, true)
	for i := range nodes {
		if nodes[i].id != "guard" {
			continue
		}
		conditions := nodes[i].logics[0]["conditions"].([]interface{})
		conditions[0].(map[string]interface{})["fun"] = "less_or_eq"
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("counter <= 10 permits 11 iterations and must exceed a ceiling of 10: %+v", report.Cycles)
	}
}

func TestCycleSafety_CanonicalGreaterComparators(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fun        string
		wantRisk   int
		maxAllowed int
	}{
		{name: "more_or_eq exits at ceiling", fun: "more_or_eq", wantRisk: 0, maxAllowed: 3},
		{name: "more exits one iteration later", fun: "more", wantRisk: 1, maxAllowed: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc, nodes := boundedRetryProcess(3, true)
			for i := range nodes {
				if nodes[i].id != "guard" {
					continue
				}
				nodes[i].logics[0]["to_node_id"] = "error"
				nodes[i].logics[0]["conditions"].([]interface{})[0].(map[string]interface{})["fun"] = tc.fun
				nodes[i].logics[1]["to_node_id"] = "delay"
			}
			report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), tc.maxAllowed))
			if got := cycleRiskCount(report); got != tc.wantRisk {
				t.Fatalf("cycle risk count = %d, want %d for %s: %+v", got, tc.wantRisk, tc.fun, report.Cycles)
			}
		})
	}
}

func TestCycleSafety_FractionalIncrementIsNotAProvenIterationBound(t *testing.T) {
	proc, nodes := boundedRetryProcess(10, true)
	for i := range nodes {
		if nodes[i].id != "increment" {
			continue
		}
		extra := nodes[i].logics[0]["extra"].(map[string]interface{})
		extra["retry_count"] = "{{$.math(retry_count+0.1)}}"
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("fractional increments can exceed the project iteration ceiling: %+v", report.Cycles)
	}
}

func TestCycleSafety_CounterResetInsideCycleInvalidatesBound(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, true)
	for i := range nodes {
		if nodes[i].id != "increment" {
			continue
		}
		reset := map[string]interface{}{
			"type": "set_param", "extra": map[string]interface{}{"retry_count": float64(0)},
			"extra_type": map[string]interface{}{"retry_count": "number"},
		}
		nodes[i].logics = append(nodes[i].logics[:1], append([]map[string]interface{}{reset}, nodes[i].logics[1:]...)...)
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("a counter reset inside the cycle must invalidate the finite-bound proof: %+v", report.Cycles)
	}
}

func TestCycleSafety_LateCounterOverrideInvalidatesInitializer(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, true)
	for i := range nodes {
		if nodes[i].id == "init" {
			nodes[i].logics[len(nodes[i].logics)-1]["to_node_id"] = "override"
		}
	}
	nodes = append(nodes, processNode{
		id: "override", title: "Override retry count", objType: 0,
		logics: []map[string]interface{}{
			{"type": "set_param", "extra": map[string]interface{}{"retry_count": "{{requested_retry_count}}"}, "extra_type": map[string]interface{}{"retry_count": "number"}},
			safetyGo("api"),
		},
	})
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("a later dynamic counter assignment must invalidate an earlier literal initializer: %+v", report.Cycles)
	}
}

func TestCycleSafety_CodeInsideCycleMakesCounterProofConservative(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, true)
	for i := range nodes {
		if nodes[i].id != "increment" {
			continue
		}
		code := map[string]interface{}{"type": "api_code", "src": "data[data.key] = 0;"}
		nodes[i].logics = append(nodes[i].logics[:1], append([]map[string]interface{}{code}, nodes[i].logics[1:]...)...)
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("computed code writes prevent a static counter guarantee: %+v", report.Cycles)
	}
}

func TestCycleSafety_APIResponseCannotResetCounterInsideCycle(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, true)
	for i := range nodes {
		if nodes[i].id == "api" {
			nodes[i].logics[0]["response"] = map[string]interface{}{"body": "{{retry_count}}"}
		}
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("an API response mapped onto the counter must invalidate the finite-bound proof: %+v", report.Cycles)
	}
}

func TestCycleSafety_CallProcessOutputMakesCounterProofConservative(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, true)
	for i := range nodes {
		if nodes[i].id == "api" {
			nodes[i].logics[0] = map[string]interface{}{
				"type": "api_rpc", "conv_id": float64(200), "err_node_id": "guard",
			}
		}
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if len(report.Cycles) != 1 || report.Cycles[0].Bounded {
		t.Fatalf("a Call Process reply can merge over the counter and must keep the proof conservative: %+v", report.Cycles)
	}
}

func TestCycleSafety_UnboundedRetryDetected(t *testing.T) {
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("api")}, nil),
		safetyTestNode("api", "Call provider", 0, []interface{}{
			map[string]interface{}{"type": "api", "err_node_id": "delay"},
			safetyGo("success"),
		}, nil),
		safetyTestNode("delay", "Retry forever", 3, nil, []interface{}{
			map[string]interface{}{"type": "time", "value": float64(30), "dimension": "sec", "to_node_id": "api"},
		}),
		safetyTestNode("success", "Success", 2, nil, nil),
	}
	proc := map[string]interface{}{"obj_id": float64(101), "params": []interface{}{}, "scheme": map[string]interface{}{"nodes": nodesRaw}}
	report := analyzeCycleSafety(proc, parseProcessNodes(nodesRaw), safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("expected unbounded cycle risk, got %+v", report)
	}
}

func TestCycleSafety_WarnModeDoesNotOfferConfirmationFingerprints(t *testing.T) {
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("loop")}, nil),
		safetyTestNode("loop", "Loop", 0, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": "{{target}}", "err_node_id": "loop"}, safetyGo("loop"),
		}, nil),
	}
	proc := map[string]interface{}{"obj_id": float64(101), "params": []interface{}{}, "scheme": map[string]interface{}{"nodes": nodesRaw}}
	policy := safetyStrictPolicy(t.TempDir(), 10)
	policy.CycleSafety.Mode = policyModeWarn
	report := analyzeCycleSafety(proc, parseProcessNodes(nodesRaw), policy)
	if cycleRiskCount(report) == 0 || len(report.UnresolvedCalls) == 0 {
		t.Fatalf("warn mode must still report risks: %+v", report)
	}
	if report.CycleRiskFingerprint != "" || report.UnresolvedRiskFingerprint != "" {
		t.Fatalf("warn mode never pauses push and must not present confirmation tokens: %+v", report)
	}
	formatted := FormatLintResult(&LintResult{
		ProcessTitle: "Warn mode", TotalNodes: len(nodesRaw), SchemaValid: true,
		EffectivePolicy: &policy, CycleSafety: report,
	})
	if strings.Contains(formatted, "Confirmation fingerprint:") {
		t.Fatalf("warn-mode MCP output must not ask for a confirmation fingerprint:\n%s", formatted)
	}
}

func TestCycleSafety_DeadlineBoundAccepted(t *testing.T) {
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("init")}, nil),
		safetyTestNode("init", "Set deadline", 0, []interface{}{
			map[string]interface{}{"type": "set_param", "extra": map[string]interface{}{"deadline": "$.math($.unixtime()+3600)"}, "extra_type": map[string]interface{}{"deadline": "number"}},
			safetyGo("api"),
		}, nil),
		safetyTestNode("api", "Call provider", 0, []interface{}{
			map[string]interface{}{"type": "api", "err_node_id": "guard"},
			safetyGo("success"),
		}, nil),
		safetyTestNode("guard", "Before deadline", 3, []interface{}{
			map[string]interface{}{
				"type": "go_if_const", "to_node_id": "delay",
				"conditions": []interface{}{map[string]interface{}{"param": "{{root.change_time}}", "const": "{{deadline}}", "fun": "less", "cast": "number"}},
			},
			safetyGo("error"),
		}, nil),
		safetyTestNode("delay", "Retry delay", 0, nil, []interface{}{
			map[string]interface{}{"type": "time", "value": float64(30), "dimension": "sec", "to_node_id": "api"},
		}),
		safetyTestNode("success", "Success", 2, nil, nil),
		safetyTestNode("error", "Deadline reached", 2, nil, nil),
	}
	proc := map[string]interface{}{"obj_id": float64(102), "params": []interface{}{}, "scheme": map[string]interface{}{"nodes": nodesRaw}}
	report := analyzeCycleSafety(proc, parseProcessNodes(nodesRaw), safetyStrictPolicy(t.TempDir(), 10))
	if len(report.Cycles) != 1 || !report.Cycles[0].Bounded || report.Cycles[0].BoundKind != "deadline" {
		t.Fatalf("expected deadline-bounded cycle, got %+v", report.Cycles)
	}
}

func TestCycleSafety_DeadlineWithoutDelayDoesNotBoundTacts(t *testing.T) {
	proc, nodes := deadlineRetryProcess(3600)
	for i := range nodes {
		if nodes[i].id == "guard" {
			nodes[i].logics[0]["to_node_id"] = "work"
		}
	}
	nodes = append(nodes, processNode{id: "work", title: "Tight loop", objType: 0, logics: []map[string]interface{}{safetyGo("guard")}})
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 || !strings.Contains(report.Cycles[0].Issue, "wall-clock duration alone") {
		t.Fatalf("a deadline without pacing can still consume unbounded tacts: %+v", report.Cycles)
	}
}

func TestCycleSafety_DeadlineGuardRequiresNumericCast(t *testing.T) {
	proc, nodes := deadlineRetryProcess(3600)
	for i := range nodes {
		if nodes[i].id == "guard" {
			conditions := nodes[i].logics[0]["conditions"].([]interface{})
			delete(conditions[0].(map[string]interface{}), "cast")
		}
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("an untyped deadline comparison must remain unproven: %+v", report.Cycles)
	}
}

func TestCycleSafety_CurrentTimeExpressionMustMatchExactly(t *testing.T) {
	for _, value := range []interface{}{"prefix$.unixtime()", "root.change_time_backup", "$.math($.unixtime()+1)"} {
		if currentTimeExpression(value) {
			t.Fatalf("non-clock expression %q was accepted as current time", value)
		}
	}
	for _, value := range []interface{}{"$.unixtime", "{{$.unixtime()}}", "{{root.change_time}}"} {
		if !currentTimeExpression(value) {
			t.Fatalf("clock expression %q was rejected", value)
		}
	}
}

func TestCycleSafety_DeadlineClockMustRefreshOnEveryCycle(t *testing.T) {
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("init")}, nil),
		safetyTestNode("init", "Set deadline", 0, []interface{}{
			map[string]interface{}{"type": "set_param", "extra": map[string]interface{}{"deadline": "$.math($.unixtime()+3600)"}, "extra_type": map[string]interface{}{"deadline": "number"}},
			safetyGo("guard"),
		}, nil),
		safetyTestNode("guard", "Before deadline", 0, []interface{}{
			map[string]interface{}{
				"type": "go_if_const", "to_node_id": "work",
				"conditions": []interface{}{map[string]interface{}{"param": "{{now}}", "const": "{{deadline}}", "fun": "less", "cast": "number"}},
			},
			safetyGo("error"),
		}, nil),
		safetyTestNode("work", "Work", 0, []interface{}{safetyGo("route")}, nil),
		safetyTestNode("route", "Optional clock refresh", 0, []interface{}{
			map[string]interface{}{
				"type": "go_if_const", "to_node_id": "refresh",
				"conditions": []interface{}{map[string]interface{}{"param": "refresh", "const": true, "fun": "eq", "cast": "boolean"}},
			},
			safetyGo("guard"),
		}, nil),
		safetyTestNode("refresh", "Refresh now", 0, []interface{}{
			map[string]interface{}{"type": "set_param", "extra": map[string]interface{}{"now": "$.unixtime()"}, "extra_type": map[string]interface{}{"now": "number"}},
			safetyGo("guard"),
		}, nil),
		safetyTestNode("error", "Expired", 2, nil, nil),
	}
	proc := map[string]interface{}{"obj_id": float64(105), "params": []interface{}{}, "scheme": map[string]interface{}{"nodes": nodesRaw}}
	report := analyzeCycleSafety(proc, parseProcessNodes(nodesRaw), safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("a stale clock path must not prove a deadline bound: %+v", report.Cycles)
	}
}

func TestCycleSafety_DeadlineAboveDurationCeilingRequiresConfirmation(t *testing.T) {
	proc, nodes := deadlineRetryProcess(7200)
	policy := safetyStrictPolicy(t.TempDir(), 10)
	policy.CycleSafety.MaxDurationSeconds = 3600
	report := analyzeCycleSafety(proc, nodes, policy)
	if cycleRiskCount(report) != 1 {
		t.Fatalf("deadline beyond the project duration ceiling must require confirmation: %+v", report.Cycles)
	}
}

func TestCycleSafety_LateDeadlineOverrideInvalidatesInitializer(t *testing.T) {
	proc, nodes := deadlineRetryProcess(3600)
	for i := range nodes {
		if nodes[i].id == "init" {
			nodes[i].logics[len(nodes[i].logics)-1]["to_node_id"] = "override"
		}
	}
	nodes = append(nodes, processNode{
		id: "override", title: "Override deadline", objType: 0,
		logics: []map[string]interface{}{
			{"type": "set_param", "extra": map[string]interface{}{"deadline": "{{requested_deadline}}"}, "extra_type": map[string]interface{}{"deadline": "number"}},
			safetyGo("guard"),
		},
	})
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("a later external deadline assignment must invalidate an earlier bounded initializer: %+v", report.Cycles)
	}
}

func TestCycleSafety_APIResponseCannotResetDeadlineInsideCycle(t *testing.T) {
	proc, nodes := deadlineRetryProcess(3600)
	for i := range nodes {
		if nodes[i].id == "delay" {
			nodes[i].sems[0]["to_node_id"] = "api"
		}
	}
	nodes = append(nodes, processNode{
		id: "api", title: "Refresh deadline from API", objType: 0,
		logics: []map[string]interface{}{
			{"type": "api", "response": map[string]interface{}{"body": "{{deadline}}"}},
			safetyGo("guard"),
		},
	})
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("an API response mapped onto the deadline must invalidate the finite-bound proof: %+v", report.Cycles)
	}
}

func TestCycleSafety_APIResponseCannotResetRefreshedClockInsideCycle(t *testing.T) {
	proc, nodes := deadlineRetryProcess(3600)
	for i := range nodes {
		switch nodes[i].id {
		case "guard":
			conditions := nodes[i].logics[0]["conditions"].([]interface{})
			conditions[0].(map[string]interface{})["param"] = "{{now}}"
		case "delay":
			nodes[i].sems[0]["to_node_id"] = "refresh"
		}
	}
	nodes = append(nodes,
		processNode{id: "refresh", title: "Refresh clock", objType: 0, logics: []map[string]interface{}{
			{"type": "set_param", "extra": map[string]interface{}{"now": "$.unixtime()"}, "extra_type": map[string]interface{}{"now": "number"}},
			safetyGo("api"),
		}},
		processNode{id: "api", title: "Overwrite clock", objType: 0, logics: []map[string]interface{}{
			{"type": "api", "response": map[string]interface{}{"body": "{{now}}"}}, safetyGo("guard"),
		}},
	)
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("runtime output after a clock refresh must invalidate the deadline proof: %+v", report.Cycles)
	}
}

func deadlineRetryProcess(duration int) (map[string]interface{}, []processNode) {
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("init")}, nil),
		safetyTestNode("init", "Set deadline", 0, []interface{}{
			map[string]interface{}{"type": "set_param", "extra": map[string]interface{}{"deadline": "$.math($.unixtime()+" + strconv.Itoa(duration) + ")"}, "extra_type": map[string]interface{}{"deadline": "number"}},
			safetyGo("guard"),
		}, nil),
		safetyTestNode("guard", "Before deadline", 0, []interface{}{
			map[string]interface{}{
				"type": "go_if_const", "to_node_id": "delay",
				"conditions": []interface{}{map[string]interface{}{"param": "{{root.change_time}}", "const": "{{deadline}}", "fun": "less", "cast": "number"}},
			},
			safetyGo("error"),
		}, nil),
		safetyTestNode("delay", "Wait", 0, nil, []interface{}{
			map[string]interface{}{"type": "time", "value": float64(1), "dimension": "min", "to_node_id": "guard"},
		}),
		safetyTestNode("error", "Expired", 2, nil, nil),
	}
	proc := map[string]interface{}{"obj_id": float64(106), "params": []interface{}{}, "scheme": map[string]interface{}{"nodes": nodesRaw}}
	return proc, parseProcessNodes(nodesRaw)
}

func TestCycleSafety_MinuteDelaySatisfiesRetryPacing(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, true)
	for i := range nodes {
		if nodes[i].id == "delay" {
			nodes[i].sems[0]["value"] = float64(1)
			nodes[i].sems[0]["dimension"] = "min"
		}
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 0 {
		t.Fatalf("one minute is a valid paced retry delay: %+v", report.Cycles)
	}
}

func TestCycleSafety_APITimeoutIsNotRetryDelay(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, false)
	for i := range nodes {
		if nodes[i].id == "api" {
			nodes[i].sems = []map[string]interface{}{{
				"type": "time", "value": float64(30), "dimension": "sec", "to_node_id": "error",
			}}
		}
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 || report.Cycles[0].HasDelay {
		t.Fatalf("an API timeout must not be mistaken for a dedicated retry Delay: %+v", report.Cycles)
	}
}

func TestCycleSafety_DynamicDelayIsNotStaticallyProven(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, true)
	for i := range nodes {
		if nodes[i].id == "delay" {
			nodes[i].sems[0]["value"] = "{{retry_at}}"
		}
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("a dynamic delay may resolve to now or the past and needs confirmation: %+v", report.Cycles)
	}
}

func TestCycleSafety_UnknownDelayUnitIsNotStaticallyProven(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, true)
	for i := range nodes {
		if nodes[i].id == "delay" {
			nodes[i].sems[0]["dimension"] = "fortnight"
		}
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("an unknown delay unit must not satisfy retry pacing: %+v", report.Cycles)
	}
}

func TestCycleSafety_DynamicConvIDWarnsButIsConfirmable(t *testing.T) {
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("call")}, nil),
		safetyTestNode("call", "Dispatch process", 0, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": "{{target_process_id}}", "err_node_id": "error"},
			safetyGo("success"),
		}, nil),
		safetyTestNode("success", "Success", 2, nil, nil),
		safetyTestNode("error", "Error", 2, nil, nil),
	}
	proc := map[string]interface{}{"obj_id": float64(103), "params": []interface{}{}, "scheme": map[string]interface{}{"nodes": nodesRaw}}
	report := analyzeCycleSafety(proc, parseProcessNodes(nodesRaw), safetyStrictPolicy(t.TempDir(), 10))
	if len(report.UnresolvedCalls) != 1 || report.UnresolvedRiskFingerprint == "" {
		t.Fatalf("expected confirmable dynamic call risk, got %+v", report)
	}
	if cycleRiskCount(report) != 0 {
		t.Fatalf("dynamic target must not be classified as a proven cycle: %+v", report)
	}
}

func TestCycleSafety_CrossScopeCallIsNotMatchedByLocalConvID(t *testing.T) {
	root := t.TempDir()
	localNodes := []interface{}{safetyTestNode("local", "Local", 1, nil, nil)}
	local := map[string]interface{}{"obj_id": float64(200), "title": "Local collision", "scheme": map[string]interface{}{"nodes": localNodes}}
	data, _ := json.Marshal(local)
	if err := os.WriteFile(filepath.Join(root, "200_local.conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("call")}, nil),
		safetyTestNode("call", "Cross-project call", 0, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": float64(200), "project_id": float64(999), "err_node_id": "error"}, safetyGo("done"),
		}, nil),
		safetyTestNode("done", "Done", 2, nil, nil), safetyTestNode("error", "Error", 2, nil, nil),
	}
	proc := map[string]interface{}{"obj_id": float64(100), "scheme": map[string]interface{}{"nodes": nodesRaw}}
	report := analyzeCycleSafety(proc, parseProcessNodes(nodesRaw), safetyStrictPolicy(root, 10))
	if len(report.UnresolvedCalls) != 1 || !strings.Contains(report.UnresolvedCalls[0].Issue, "explicit project_id") {
		t.Fatalf("cross-scope call must remain unresolved even when conv_id collides locally: %+v", report.UnresolvedCalls)
	}
}

func TestCycleSafety_UnreadableLocalCallGraphRemainsUnresolved(t *testing.T) {
	root := t.TempDir()
	broken := map[string]interface{}{
		"obj_id": float64(200), "title": "Broken target", "scheme": map[string]interface{}{},
	}
	data, err := json.Marshal(broken)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "200_broken.conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("call")}, nil),
		safetyTestNode("call", "Call broken target", 0, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": float64(200), "err_node_id": "error"}, safetyGo("done"),
		}, nil),
		safetyTestNode("done", "Done", 2, nil, nil),
		safetyTestNode("error", "Error", 2, nil, nil),
	}
	proc := map[string]interface{}{"obj_id": float64(100), "scheme": map[string]interface{}{"nodes": nodesRaw}}
	report := analyzeCycleSafety(proc, parseProcessNodes(nodesRaw), safetyStrictPolicy(root, 10))
	if len(report.UnresolvedCalls) != 1 || !strings.Contains(report.UnresolvedCalls[0].Issue, "no readable process graph") {
		t.Fatalf("an invalid local export must not create a false static-recursion guarantee: %+v", report.UnresolvedCalls)
	}
	if report.UnresolvedRiskFingerprint == "" {
		t.Fatal("strict mode must provide a confirmation fingerprint for the unresolved local target")
	}
}

func TestCycleSafety_TransitiveUnresolvedTargetIsReported(t *testing.T) {
	root := t.TempDir()
	calleeNodes := []interface{}{
		safetyTestNode("callee-start", "Start", 1, []interface{}{safetyGo("dynamic-call")}, nil),
		safetyTestNode("dynamic-call", "Dispatch downstream", 0, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": "{{next_process_id}}", "err_node_id": "callee-error"}, safetyGo("callee-done"),
		}, nil),
		safetyTestNode("callee-done", "Done", 2, nil, nil),
		safetyTestNode("callee-error", "Error", 2, nil, nil),
	}
	callee := map[string]interface{}{
		"obj_id": float64(200), "title": "Dispatcher", "scheme": map[string]interface{}{"nodes": calleeNodes},
	}
	data, err := json.Marshal(callee)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "200_dispatcher.conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	currentNodes := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("call")}, nil),
		safetyTestNode("call", "Call dispatcher", 0, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": float64(200), "err_node_id": "error"}, safetyGo("done"),
		}, nil),
		safetyTestNode("done", "Done", 2, nil, nil),
		safetyTestNode("error", "Error", 2, nil, nil),
	}
	proc := map[string]interface{}{"obj_id": float64(100), "title": "Caller", "scheme": map[string]interface{}{"nodes": currentNodes}}
	report := analyzeCycleSafety(proc, parseProcessNodes(currentNodes), safetyStrictPolicy(root, 10))
	if len(report.UnresolvedCalls) != 1 || report.UnresolvedCalls[0].SourceProcessID != 200 {
		t.Fatalf("a downstream unresolved target must be attributed to process 200: %+v", report.UnresolvedCalls)
	}
	if report.UnresolvedRiskFingerprint == "" {
		t.Fatal("transitive unresolved risk must require the usual graph-specific acknowledgement")
	}
}

func TestCycleSafety_AliasAndUnavailableTargetsRequireConfirmation(t *testing.T) {
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("alias")}, nil),
		safetyTestNode("alias", "Alias call", 0, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": "@billing", "err_node_id": "error"}, safetyGo("external"),
		}, nil),
		safetyTestNode("external", "External call", 0, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": float64(999), "err_node_id": "error"}, safetyGo("success"),
		}, nil),
		safetyTestNode("success", "Success", 2, nil, nil),
		safetyTestNode("error", "Error", 2, nil, nil),
	}
	proc := map[string]interface{}{"obj_id": float64(103), "params": []interface{}{}, "scheme": map[string]interface{}{"nodes": nodesRaw}}
	report := analyzeCycleSafety(proc, parseProcessNodes(nodesRaw), safetyStrictPolicy(t.TempDir(), 10))
	if len(report.UnresolvedCalls) != 2 || report.UnresolvedRiskFingerprint == "" {
		t.Fatalf("alias and unavailable process targets must require one graph-specific acknowledgement each: %+v", report)
	}
}

func TestCycleSafety_CopyModifyIsNotAProcessInvocation(t *testing.T) {
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("modify")}, nil),
		safetyTestNode("modify", "Modify copied task", 0, []interface{}{
			map[string]interface{}{"type": "api_copy", "mode": "modify", "conv_id": float64(999), "err_node_id": "error"}, safetyGo("success"),
		}, nil),
		safetyTestNode("success", "Success", 2, nil, nil),
		safetyTestNode("error", "Error", 2, nil, nil),
	}
	proc := map[string]interface{}{"obj_id": float64(103), "params": []interface{}{}, "scheme": map[string]interface{}{"nodes": nodesRaw}}
	report := analyzeCycleSafety(proc, parseProcessNodes(nodesRaw), safetyStrictPolicy(t.TempDir(), 10))
	if len(report.UnresolvedCalls) != 0 || len(report.InterprocessCycles) != 0 {
		t.Fatalf("api_copy modify updates a task and must not enter the process-call graph: %+v", report)
	}
}

func TestCycleSafety_UnreachableDynamicTargetDoesNotRequireConfirmation(t *testing.T) {
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("success")}, nil),
		safetyTestNode("success", "Success", 2, nil, nil),
		safetyTestNode("orphan", "Unused dynamic call", 0, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": "{{target_process_id}}", "err_node_id": "error"},
			safetyGo("success"),
		}, nil),
		safetyTestNode("error", "Error", 2, nil, nil),
	}
	proc := map[string]interface{}{"obj_id": float64(104), "params": []interface{}{}, "scheme": map[string]interface{}{"nodes": nodesRaw}}
	report := analyzeCycleSafety(proc, parseProcessNodes(nodesRaw), safetyStrictPolicy(t.TempDir(), 10))
	if len(report.UnresolvedCalls) != 0 {
		t.Fatalf("unreachable dynamic calls cannot execute and must not pause a push: %+v", report.UnresolvedCalls)
	}
}

func TestCycleSafety_InternalFiniteLoopDoesNotRequireDelay(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, false)
	for i := range nodes {
		if nodes[i].id == "api" {
			nodes[i].logics = []map[string]interface{}{safetyGo("guard")}
		}
	}
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if len(report.Cycles) != 1 || !report.Cycles[0].Bounded {
		t.Fatalf("finite internal loop should be accepted without external retry pacing: %+v", report.Cycles)
	}
}

func TestCycleSafety_ExternalRetryRequiresDelay(t *testing.T) {
	proc, nodes := boundedRetryProcess(3, false)
	report := analyzeCycleSafety(proc, nodes, safetyStrictPolicy(t.TempDir(), 10))
	if cycleRiskCount(report) != 1 {
		t.Fatalf("external retry without pacing must require confirmation: %+v", report.Cycles)
	}
	if !strings.Contains(report.Cycles[0].Issue, "no Delay") {
		t.Fatalf("unexpected issue: %+v", report.Cycles[0])
	}
}

func writeSafetyProcessFile(t *testing.T, root, name string, id int, target int) {
	t.Helper()
	callID := "final"
	var nodes []interface{}
	if target != 0 {
		callID = "call"
	}
	nodes = append(nodes, safetyTestNode("start", "Start", 1, []interface{}{safetyGo(callID)}, nil))
	if target != 0 {
		nodes = append(nodes, safetyTestNode("call", "Call target", 0, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": float64(target), "err_node_id": "error"},
			safetyGo("final"),
		}, nil))
	}
	nodes = append(nodes,
		safetyTestNode("final", "Final", 2, nil, nil),
		safetyTestNode("error", "Error", 2, nil, nil),
	)
	proc := map[string]interface{}{
		"obj_id": id, "title": name, "params": []interface{}{},
		"scheme": map[string]interface{}{"nodes": nodes},
	}
	data, err := json.Marshal(proc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name+".conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCycleSafety_InterprocessCyclesIgnoreContractScope(t *testing.T) {
	root := t.TempDir()
	writeSafetyProcessFile(t, root, "100_A", 100, 200)
	writeSafetyProcessFile(t, root, "200_B", 200, 100)
	data, err := os.ReadFile(filepath.Join(root, "100_A.conv.json"))
	if err != nil {
		t.Fatal(err)
	}
	var proc map[string]interface{}
	if err := json.Unmarshal(data, &proc); err != nil {
		t.Fatal(err)
	}
	rawNodes, _ := getNodes(proc)
	policy := safetyStrictPolicy(root, 10)
	policy.ProcessContracts.DependencyScope = "self"
	report := analyzeCycleSafety(proc, parseProcessNodes(rawNodes), policy)
	if len(report.InterprocessCycles) != 1 || report.CycleRiskFingerprint == "" {
		t.Fatalf("cycle safety must scan static process recursion independently of contract scope: %+v", report)
	}
}

func TestCycleSafety_DetectsCycleInsideReachableCalleeGraph(t *testing.T) {
	root := t.TempDir()
	writeSafetyProcessFile(t, root, "100_A", 100, 200)
	writeSafetyProcessFile(t, root, "200_B", 200, 300)
	writeSafetyProcessFile(t, root, "300_C", 300, 200)
	data, err := os.ReadFile(filepath.Join(root, "100_A.conv.json"))
	if err != nil {
		t.Fatal(err)
	}
	var proc map[string]interface{}
	if err := json.Unmarshal(data, &proc); err != nil {
		t.Fatal(err)
	}
	rawNodes, _ := getNodes(proc)
	report := analyzeCycleSafety(proc, parseProcessNodes(rawNodes), safetyStrictPolicy(root, 10))
	if len(report.InterprocessCycles) != 1 {
		t.Fatalf("a cycle in reachable callees must pause the caller push: %+v", report.InterprocessCycles)
	}
	ids := report.InterprocessCycles[0].ProcessIDs
	if len(ids) != 3 || ids[0] != 200 || ids[1] != 300 || ids[2] != 200 {
		t.Fatalf("expected concrete B -> C -> B cycle, got %v", ids)
	}
}

func TestCycleSafety_ActiveStubDoesNotCreateProcessCallEdge(t *testing.T) {
	root := t.TempDir()
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("call")}, nil),
		safetyTestNode("call", "Stubbed call", 4, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": float64(200)}, safetyGo("final"),
		}, nil),
		safetyTestNode("final", "Final", 2, nil, nil),
	}
	proc := map[string]interface{}{
		"obj_id": float64(100), "title": "Caller", "scheme": map[string]interface{}{"nodes": nodesRaw},
	}
	report := analyzeCycleSafety(proc, parseProcessNodes(nodesRaw), safetyStrictPolicy(root, 10))
	if len(report.UnresolvedCalls) != 0 || len(report.InterprocessCycles) != 0 {
		t.Fatalf("active Stub Mode bypasses the target process at runtime and must not create a call-graph risk: %+v", report)
	}
}

func TestCycleSafety_IgnoresHiddenDirectoryProcessCopies(t *testing.T) {
	root := t.TempDir()
	writeSafetyProcessFile(t, root, "100_A", 100, 0)
	for _, name := range []string{".git-context", ".archive"} {
		hidden := filepath.Join(root, name)
		if err := os.MkdirAll(hidden, 0755); err != nil {
			t.Fatal(err)
		}
		writeSafetyProcessFile(t, hidden, "100_A", 100, 200)
		writeSafetyProcessFile(t, hidden, "200_B", 200, 100)
	}
	if risks := findInterprocessCycleRisks(root, 100, "A", nil); len(risks) != 0 {
		t.Fatalf("hidden-directory copies are not the deployable project graph: %+v", risks)
	}
}

func TestCycleSafety_IgnoresSymlinkedProcessFiles(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	writeSafetyProcessFile(t, external, "200_B", 200, 100)
	link := filepath.Join(root, "200_B.conv.json")
	if err := os.Symlink(filepath.Join(external, "200_B.conv.json"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("call")}, nil),
		safetyTestNode("call", "Call B", 0, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": float64(200)}, safetyGo("final"),
		}, nil),
		safetyTestNode("final", "Final", 2, nil, nil),
	}
	if risks := findInterprocessCycleRisks(root, 100, "A", parseProcessNodes(nodesRaw)); len(risks) != 0 {
		t.Fatalf("symlinked files must not import a call graph from outside the project: %+v", risks)
	}
}

func TestCycleSafety_DuplicateProcessIDsUnionCallTargets(t *testing.T) {
	root := t.TempDir()
	writeSafetyProcessFile(t, root, "200_A", 200, 100)
	writeSafetyProcessFile(t, root, "200_Z", 200, 0)
	nodesRaw := []interface{}{
		safetyTestNode("start", "Start", 1, []interface{}{safetyGo("call")}, nil),
		safetyTestNode("call", "Call B", 0, []interface{}{
			map[string]interface{}{"type": "api_rpc", "conv_id": float64(200)}, safetyGo("final"),
		}, nil),
		safetyTestNode("final", "Final", 2, nil, nil),
	}
	risks := findInterprocessCycleRisks(root, 100, "A", parseProcessNodes(nodesRaw))
	if len(risks) != 1 {
		t.Fatalf("duplicate exports must be unioned conservatively instead of hiding recursion: %+v", risks)
	}
}

func TestInterprocessCycleRisks_BranchingDAGCompletesLinearly(t *testing.T) {
	const processes = 30
	entries := make(map[int]processCallGraphEntry, processes)
	for id := 1; id <= processes; id++ {
		entry := processCallGraphEntry{ID: id, Title: fmt.Sprintf("P%d", id)}
		for step := 1; step <= 3 && id+step <= processes; step++ {
			entry.Targets = append(entry.Targets, id+step)
		}
		entries[id] = entry
	}
	started := time.Now()
	if risks := interprocessCycleRisks(entries, 1); len(risks) != 0 {
		t.Fatalf("acyclic call graph reported recursion: %+v", risks)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("30-process branching DAG took %s; cycle detection must remain O(V+E)", elapsed)
	}
}

func TestInterprocessCycleRisks_ReportsEachReachableSCCOnce(t *testing.T) {
	entries := map[int]processCallGraphEntry{
		1: {ID: 1, Title: "Root", Targets: []int{2, 4}},
		2: {ID: 2, Title: "A", Targets: []int{3}},
		3: {ID: 3, Title: "B", Targets: []int{2}},
		4: {ID: 4, Title: "Self", Targets: []int{4}},
	}
	risks := interprocessCycleRisks(entries, 1)
	if len(risks) != 2 {
		t.Fatalf("recursive SCCs = %d, want 2: %+v", len(risks), risks)
	}
	if got := fmt.Sprint(risks[0].ProcessIDs); got != "[2 3 2]" {
		t.Fatalf("first concrete cycle = %s, want [2 3 2]", got)
	}
	if got := fmt.Sprint(risks[1].ProcessIDs); got != "[4 4]" {
		t.Fatalf("self-cycle = %s, want [4 4]", got)
	}
}

func BenchmarkInterprocessCycleRisks_TenThousandProcessDAG(b *testing.B) {
	const processes = 10_000
	entries := make(map[int]processCallGraphEntry, processes)
	for id := 1; id <= processes; id++ {
		entry := processCallGraphEntry{ID: id}
		for step := 1; step <= 3 && id+step <= processes; step++ {
			entry.Targets = append(entry.Targets, id+step)
		}
		entries[id] = entry
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if risks := interprocessCycleRisks(entries, 1); len(risks) != 0 {
			b.Fatalf("acyclic call graph reported recursion: %+v", risks)
		}
	}
}
