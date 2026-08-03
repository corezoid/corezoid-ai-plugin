package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func contractParam(name, typ, descr string, flags ...string) map[string]interface{} {
	rawFlags := make([]interface{}, len(flags))
	for i, flag := range flags {
		rawFlags[i] = flag
	}
	return map[string]interface{}{
		"name": name, "type": typ, "descr": descr, "flags": rawFlags,
		"regex": "", "regex_error_text": "",
	}
}

func contractPolicy(root, mode, scope string) EffectiveProjectPolicy {
	p := defaultProjectPolicy()
	p.ProcessContracts.Mode = mode
	p.ProcessContracts.DependencyScope = scope
	return EffectiveProjectPolicy{ProjectPolicy: p, Root: root, Configured: true}
}

func contractNode(id, title string, objType float64, logics ...map[string]interface{}) processNode {
	return processNode{id: id, title: title, objType: objType, logics: logics}
}

func issueCodes(report *ContractReport) map[string]int {
	out := map[string]int{}
	for _, issue := range report.Issues {
		out[issue.Code]++
	}
	return out
}

func TestProcessContracts_ValidInputsAndOutputs(t *testing.T) {
	proc := map[string]interface{}{
		"params": []interface{}{
			contractParam("customer_id", "string", "Customer identifier", "required", "input"),
			contractParam("result", "object", "Operation result", "required", "output"),
		},
	}
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("reply")),
		contractNode("reply", "Reply", 0,
			map[string]interface{}{
				"type": "api_rpc_reply", "throw_exception": false,
				"res_data":      map[string]interface{}{"result": "{{customer_id}}"},
				"res_data_type": map[string]interface{}{"result": "object"},
			},
			safetyGo("final"),
		),
		contractNode("final", "Final", 2),
	}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if len(report.Issues) != 0 {
		t.Fatalf("expected a valid contract, got %+v", report.Issues)
	}
}

func FuzzProcessContractCodeDoesNotPanic(f *testing.F) {
	for _, seed := range []string{
		`data.result = data.input;`,
		`data[key] = Object.values(data);`,
		`/* data.fake */ data["safe"] = "ok";`,
		"const value = `prefix ${data.name}`;",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 1<<20 {
			return
		}
		proc := map[string]interface{}{"params": []interface{}{}}
		nodes := []processNode{
			contractNode("start", "Start", 1, safetyGo("code")),
			contractNode("code", "Code", 0,
				map[string]interface{}{"type": "api_code", "src": source}, safetyGo("final")),
			contractNode("final", "Final", 2),
		}
		_ = analyzeProcessContract(proc, nodes, contractPolicy("", policyModeStrict, "self"))
	})
}

func BenchmarkProcessContracts_ThousandNodeGraph(b *testing.B) {
	nodes := make([]processNode, 1000)
	for i := range nodes {
		id := fmt.Sprintf("node_%04d", i)
		objType := float64(0)
		if i == 0 {
			objType = 1
		}
		var logics []map[string]interface{}
		if i+1 < len(nodes) {
			logics = []map[string]interface{}{safetyGo(fmt.Sprintf("node_%04d", i+1))}
		}
		nodes[i] = processNode{id: id, title: id, objType: objType, logics: logics}
	}
	proc := map[string]interface{}{"params": []interface{}{}}
	policy := contractPolicy("", policyModeStrict, "self")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = analyzeProcessContract(proc, nodes, policy)
	}
}

func TestProcessContracts_StateDiagramIsNotTreatedAsCallableProcess(t *testing.T) {
	proc := map[string]interface{}{
		"conv_type": "state", "params": []interface{}{},
	}
	nodes := []processNode{contractNode("state", "Active state", 0, map[string]interface{}{
		"type": "go_if_const", "to_node_id": "state",
		"conditions": []interface{}{map[string]interface{}{"param": "status", "const": "active", "fun": "eq", "cast": "string"}},
	})}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "project"))
	if report.SkippedReason == "" || len(report.Issues) != 0 || blockingContractIssueCount(report) != 0 {
		t.Fatalf("state-task fields are not process input/output contract violations: %+v", report)
	}
}

func TestProcessContracts_InferMissingInputAndOutput(t *testing.T) {
	proc := map[string]interface{}{"params": []interface{}{}}
	nodes := []processNode{
		contractNode("condition", "Check customer", 0,
			map[string]interface{}{
				"type": "go_if_const", "to_node_id": "reply",
				"conditions": []interface{}{map[string]interface{}{"param": "customer_id", "const": "x", "fun": "eq", "cast": "string"}},
			},
			safetyGo("reply"),
		),
		contractNode("reply", "Reply", 0,
			map[string]interface{}{
				"type": "api_rpc_reply", "throw_exception": false,
				"res_data":      map[string]interface{}{"result": "{{customer_id}}"},
				"res_data_type": map[string]interface{}{"result": "string"},
			},
		),
	}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	codes := issueCodes(report)
	if codes["INPUT_UNDECLARED"] != 1 || codes["OUTPUT_UNDECLARED"] != 1 {
		t.Fatalf("expected inferred missing input/output, got %+v", report.Issues)
	}
}

func TestProcessContracts_ConflictingInputTypeHintsAreBlocking(t *testing.T) {
	proc := map[string]interface{}{
		"params": []interface{}{contractParam("customer_id", "string", "Customer identifier", "input")},
	}
	nodes := []processNode{
		contractNode("string-check", "String check", 0, map[string]interface{}{
			"type": "go_if_const", "to_node_id": "done",
			"conditions": []interface{}{map[string]interface{}{"param": "customer_id", "const": "x", "fun": "eq", "cast": "string"}},
		}),
		contractNode("number-check", "Number check", 0, map[string]interface{}{
			"type": "go_if_const", "to_node_id": "done",
			"conditions": []interface{}{map[string]interface{}{"param": "customer_id", "const": "1", "fun": "more", "cast": "number"}},
		}),
	}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_TYPE_CONFLICT"] != 1 || blockingContractIssueCount(report) == 0 {
		t.Fatalf("conflicting type hints must block a strict contract: %+v", report.Issues)
	}
}

func TestProcessContracts_DynamicConditionConstantCarriesCastHint(t *testing.T) {
	proc := map[string]interface{}{
		"params": []interface{}{
			contractParam("amount", "number", "Requested amount", "input"),
			contractParam("minimum_amount", "string", "Configured minimum", "input"),
		},
	}
	nodes := []processNode{contractNode("check", "Check minimum", 0, map[string]interface{}{
		"type": "go_if_const", "to_node_id": "done",
		"conditions": []interface{}{map[string]interface{}{"param": "amount", "const": "{{minimum_amount}}", "fun": "more", "cast": "number"}},
	})}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_TYPE_MISMATCH"] != 1 {
		t.Fatalf("dynamic condition constant must use the condition cast type: %+v", report.Issues)
	}
}

func TestProcessContracts_LocalProducerMustPrecedeEveryRead(t *testing.T) {
	proc := map[string]interface{}{"params": []interface{}{}}
	setNode := contractNode("set", "Set value", 0,
		map[string]interface{}{
			"type": "set_param", "extra": map[string]interface{}{"value": "ok"},
			"extra_type": map[string]interface{}{"value": "string"}, "err_node_id": "error",
		},
		safetyGo("done"),
	)
	checkNode := contractNode("check", "Check value", 0,
		map[string]interface{}{
			"type": "go_if_const", "to_node_id": "set",
			"conditions": []interface{}{map[string]interface{}{"param": "value", "const": "ok", "fun": "eq", "cast": "string"}},
		},
		safetyGo("set"),
	)
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("check")), checkNode, setNode,
		contractNode("done", "Done", 2), contractNode("error", "Error", 2),
	}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
		t.Fatalf("a variable read before Set Parameters remains an input: %+v", report.Issues)
	}

	setNode.logics[len(setNode.logics)-1]["to_node_id"] = "check"
	checkNode.logics[0]["to_node_id"] = "done"
	checkNode.logics[len(checkNode.logics)-1]["to_node_id"] = "done"
	nodes = []processNode{
		contractNode("start", "Start", 1, safetyGo("set")), setNode, checkNode,
		contractNode("done", "Done", 2), contractNode("error", "Error", 2),
	}
	report = analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 0 {
		t.Fatalf("a value read only after the Set Parameters success edge is locally produced: %+v", report.Issues)
	}
}

func TestProcessContracts_APIResponseDestinationIsNotAnInput(t *testing.T) {
	proc := map[string]interface{}{"params": []interface{}{}}
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("api")),
		contractNode("api", "Fetch customer", 0,
			map[string]interface{}{
				"type": "api", "url": "https://example.test/{{customer_id}}", "err_node_id": "error",
				"response": map[string]interface{}{"body": "{{customer}}"}, "response_type": map[string]interface{}{"body": "object"},
			},
			safetyGo("check"),
		),
		contractNode("check", "Check customer", 0,
			map[string]interface{}{
				"type": "go_if_const", "to_node_id": "done",
				"conditions": []interface{}{map[string]interface{}{"param": "customer.id", "const": "x", "fun": "eq", "cast": "string"}},
			},
			safetyGo("done"),
		),
		contractNode("done", "Done", 2),
		contractNode("error", "Error", 2),
	}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	codes := issueCodes(report)
	if codes["INPUT_UNDECLARED"] != 1 {
		t.Fatalf("only customer_id is an input; the API response destination is locally produced: %+v", report.Issues)
	}
	for _, issue := range report.Issues {
		if issue.Code == "INPUT_UNDECLARED" && issue.Parameter != "customer_id" {
			t.Fatalf("API response destination %q was misclassified as input: %+v", issue.Parameter, report.Issues)
		}
	}
}

func TestProcessContracts_ProducerSharedSuccessAndErrorTargetIsNotProven(t *testing.T) {
	proc := map[string]interface{}{"params": []interface{}{}}
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("set")),
		contractNode("set", "Set value", 0,
			map[string]interface{}{
				"type": "set_param", "extra": map[string]interface{}{"value": "ok"},
				"extra_type": map[string]interface{}{"value": "string"}, "err_node_id": "check",
			},
			safetyGo("check"),
		),
		contractNode("check", "Check value", 0,
			map[string]interface{}{
				"type": "go_if_const", "to_node_id": "done",
				"conditions": []interface{}{map[string]interface{}{"param": "value", "const": "ok", "fun": "eq", "cast": "string"}},
			},
			safetyGo("done"),
		),
		contractNode("done", "Done", 2),
	}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
		t.Fatalf("a producer that can fail into the same reader does not define the value on every path: %+v", report.Issues)
	}
}

func TestProcessContracts_AlternativeProducersCoverEveryPath(t *testing.T) {
	proc := map[string]interface{}{"params": []interface{}{}}
	branch := contractNode("branch", "Choose source", 0,
		map[string]interface{}{
			"type": "go_if_const", "to_node_id": "set-a",
			"conditions": []interface{}{map[string]interface{}{"param": "use_a", "const": true, "fun": "eq", "cast": "boolean"}},
		},
		safetyGo("set-b"),
	)
	setA := contractNode("set-a", "Set A", 0,
		map[string]interface{}{"type": "set_param", "extra": map[string]interface{}{"value": "a"}, "extra_type": map[string]interface{}{"value": "string"}},
		safetyGo("read"),
	)
	setB := contractNode("set-b", "Set B", 0,
		map[string]interface{}{"type": "set_param", "extra": map[string]interface{}{"value": "b"}, "extra_type": map[string]interface{}{"value": "string"}},
		safetyGo("read"),
	)
	read := contractNode("read", "Read merged value", 0,
		map[string]interface{}{
			"type": "go_if_const", "to_node_id": "done",
			"conditions": []interface{}{map[string]interface{}{"param": "value", "const": "a", "fun": "eq", "cast": "string"}},
		},
		safetyGo("done"),
	)
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("branch")), branch, setA, setB, read,
		contractNode("done", "Done", 2),
	}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
		t.Fatalf("only branch selector use_a is an input when both branches produce value: %+v", report.Issues)
	}
	for _, issue := range report.Issues {
		if issue.Code == "INPUT_UNDECLARED" && issue.Parameter != "use_a" {
			t.Fatalf("merged producer value %q was misclassified as input: %+v", issue.Parameter, report.Issues)
		}
	}

	setB.logics = []map[string]interface{}{safetyGo("read")}
	nodes[3] = setB
	report = analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 2 {
		t.Fatalf("value remains an input when one branch reaches the read without producing it: %+v", report.Issues)
	}
}

func TestProcessContracts_RequiredOutputMustExistOnEverySuccessReply(t *testing.T) {
	proc := map[string]interface{}{
		"params": []interface{}{contractParam("result", "string", "Result status", "required", "output")},
	}
	nodes := []processNode{
		contractNode("reply1", "Reply one", 0, map[string]interface{}{
			"type": "api_rpc_reply", "res_data": map[string]interface{}{"result": "ok"}, "res_data_type": map[string]interface{}{"result": "string"},
		}),
		contractNode("reply2", "Reply two", 0, map[string]interface{}{
			"type": "api_rpc_reply", "res_data": map[string]interface{}{}, "res_data_type": map[string]interface{}{},
		}),
	}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["OUTPUT_REQUIRED_MISSING"] != 1 {
		t.Fatalf("expected required-output path issue, got %+v", report.Issues)
	}
}

func TestProcessContracts_KeysReplyDefinesOutputsByPosition(t *testing.T) {
	proc := map[string]interface{}{
		"params": []interface{}{
			contractParam("result", "string", "Result status", "required", "output"),
			contractParam("total", "number", "Calculated total", "required", "output"),
		},
	}
	nodes := []processNode{contractNode("reply", "Reply", 0, map[string]interface{}{
		"type": "api_rpc_reply", "mode": "keys", "throw_exception": false,
		"res_data": []interface{}{"result", "total"}, "res_data_type": []interface{}{"string", "number"},
	})}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if len(report.Issues) != 0 {
		t.Fatalf("keys-mode names and types must satisfy the output contract by matching indexes: %+v", report.Issues)
	}
}

func TestProcessContracts_KeysReplyRequiresParallelUniqueEntries(t *testing.T) {
	proc := map[string]interface{}{
		"params": []interface{}{contractParam("result", "string", "Result status", "output")},
	}
	nodes := []processNode{contractNode("reply", "Reply", 0, map[string]interface{}{
		"type": "api_rpc_reply", "mode": "keys", "throw_exception": false,
		"res_data": []interface{}{"result", "result"}, "res_data_type": []interface{}{"string"},
	})}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	codes := issueCodes(report)
	if codes["OUTPUT_KEYS_LENGTH_MISMATCH"] != 1 || codes["OUTPUT_KEYS_DUPLICATE"] != 1 {
		t.Fatalf("keys-mode output names and types must be parallel and unique: %+v", report.Issues)
	}
}

func TestReplySchema_KeysModeRequiresParallelArrays(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("samples", "valid_process.json"))
	if err != nil {
		t.Fatal(err)
	}
	var proc map[string]interface{}
	if err := json.Unmarshal(data, &proc); err != nil {
		t.Fatal(err)
	}
	completePolicyTestProcess(proc)
	scheme := proc["scheme"].(map[string]interface{})
	nodes := scheme["nodes"].([]interface{})
	start := nodes[0].(map[string]interface{})
	start["condition"].(map[string]interface{})["logics"].([]interface{})[0].(map[string]interface{})["to_node_id"] = "bbccddaabbccddaabbcc0003"
	reply := map[string]interface{}{
		"id": "bbccddaabbccddaabbcc0003", "obj_type": float64(0), "title": "Reply",
		"x": float64(150), "y": float64(0), "extra": `{"modeForm":"collapse","icon":""}`, "options": nil,
		"condition": map[string]interface{}{
			"logics": []interface{}{
				map[string]interface{}{
					"type": "api_rpc_reply", "mode": "keys", "throw_exception": false,
					"res_data": []interface{}{"result", "total"}, "res_data_type": []interface{}{"string", "number"},
				},
				safetyGo("bbccddaabbccddaabbcc0002"),
			},
			"semaphors": []interface{}{},
		},
	}
	scheme["nodes"] = append(nodes, reply)
	encoded, _ := json.Marshal(proc)
	path := filepath.Join(t.TempDir(), "keys.conv.json")
	if err := os.WriteFile(path, encoded, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateJSONSchema(path, false); err != nil {
		t.Fatalf("documented keys-mode Reply must pass the embedded schema: %v", err)
	}

	replyLogic := reply["condition"].(map[string]interface{})["logics"].([]interface{})[0].(map[string]interface{})
	replyLogic["mode"] = "key_value"
	encoded, _ = json.Marshal(proc)
	if err := os.WriteFile(path, encoded, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateJSONSchema(path, false); err == nil {
		t.Fatal("key_value mode with array response fields must remain invalid")
	}
}

func TestProcessContracts_DynamicTargetIsAdvisory(t *testing.T) {
	proc := map[string]interface{}{
		"params": []interface{}{contractParam("target_process_id", "number", "Runtime target process", "required", "input")},
	}
	nodes := []processNode{
		contractNode("call", "Dispatch", 0,
			map[string]interface{}{
				"type": "api_rpc", "conv_id": "{{target_process_id}}", "group": "",
				"extra": map[string]interface{}{}, "extra_type": map[string]interface{}{},
			},
		),
	}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "project"))
	codes := issueCodes(report)
	if codes["DYNAMIC_CONTRACT_UNVERIFIED"] != 1 {
		t.Fatalf("expected dynamic contract advisory, got %+v", report.Issues)
	}
	if blockingContractIssueCount(report) != 0 {
		t.Fatalf("dynamic target must remain allowed after risk acknowledgement: %+v", report.Issues)
	}
}

func TestProcessContracts_CrossScopeTargetDoesNotUseLocalContract(t *testing.T) {
	local := map[int]localProcessContract{
		200: {ID: 200, Title: "Local collision", Inputs: []processParameter{{Name: "token", Type: "string", Required: true}}},
	}
	nodes := []processNode{contractNode("call", "Cross-project call", 0, map[string]interface{}{
		"type": "api_rpc", "conv_id": float64(200), "project_id": float64(999), "group": "",
		"extra": map[string]interface{}{}, "extra_type": map[string]interface{}{},
	}, safetyGo("check"))}
	issues := validateCallContracts(nodes, local)
	if len(issues) != 1 || issues[0].Code != "CROSS_SCOPE_CONTRACT_UNVERIFIED" || !issues[0].Advisory {
		t.Fatalf("cross-scope call must not be validated against a colliding local conv_id: %+v", issues)
	}
	producers := calleeOutputSuccessTargets(nodes, map[int][]processParameter{
		200: {{Name: "result", Type: "string", Output: true}},
	}, "result")
	if len(producers) != 0 {
		t.Fatalf("cross-scope outputs must not be inferred from a colliding local process: %+v", producers)
	}
}

func TestProcessContracts_FractionalTargetIsNotRoundedToLocalProcess(t *testing.T) {
	local := map[int]localProcessContract{
		200: {ID: 200, Title: "Callee", Inputs: []processParameter{{Name: "token", Type: "string", Required: true}}},
	}
	nodes := []processNode{contractNode("call", "Call Callee", 0, map[string]interface{}{
		"type": "api_rpc", "conv_id": float64(200.5), "group": "",
		"extra": map[string]interface{}{}, "extra_type": map[string]interface{}{},
	})}
	issues := validateCallContracts(nodes, local)
	if len(issues) != 1 || issues[0].Code != "CALLEE_CONTRACT_UNAVAILABLE" || !issues[0].Advisory {
		t.Fatalf("a fractional conv_id must remain unresolved instead of selecting process 200: %+v", issues)
	}
}

func TestProcessContracts_StaticCalleeRequiredInputChecked(t *testing.T) {
	root := t.TempDir()
	callee := map[string]interface{}{
		"obj_id": float64(200), "title": "Callee",
		"params": []interface{}{contractParam("token", "string", "Authentication token", "required", "input")},
		"scheme": map[string]interface{}{"nodes": []interface{}{}},
	}
	data, err := json.Marshal(callee)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "200_Callee.conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	caller := map[string]interface{}{"obj_id": float64(100), "params": []interface{}{}}
	nodes := []processNode{
		contractNode("call", "Call Callee", 0,
			map[string]interface{}{
				"type": "api_rpc", "conv_id": float64(200), "group": "",
				"extra": map[string]interface{}{}, "extra_type": map[string]interface{}{},
			},
		),
	}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if issueCodes(report)["CALLEE_REQUIRED_INPUT_MISSING"] != 1 {
		t.Fatalf("expected missing required callee input, got %+v", report.Issues)
	}
}

func TestProcessContracts_StaticCallRequiresExtraType(t *testing.T) {
	root := t.TempDir()
	callee := map[string]interface{}{
		"obj_id": float64(200), "title": "Callee",
		"params": []interface{}{contractParam("token", "string", "Authentication token", "required", "input")},
		"scheme": map[string]interface{}{"nodes": []interface{}{}},
	}
	data, _ := json.Marshal(callee)
	if err := os.WriteFile(filepath.Join(root, "200_Callee.conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	caller := map[string]interface{}{"obj_id": float64(100), "params": []interface{}{}}
	nodes := []processNode{contractNode("call", "Call Callee", 0, map[string]interface{}{
		"type": "api_rpc", "conv_id": float64(200), "group": "",
		"extra": map[string]interface{}{"token": "{{token}}"}, "extra_type": map[string]interface{}{},
	})}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if issueCodes(report)["CALLEE_INPUT_TYPE_MISSING"] != 1 {
		t.Fatalf("expected missing extra_type issue, got %+v", report.Issues)
	}
}

func TestProcessContracts_StaticCallRejectsTypeWithoutValue(t *testing.T) {
	local := map[int]localProcessContract{
		200: {ID: 200, Title: "Callee", Inputs: []processParameter{{Name: "token", Type: "string"}}},
	}
	nodes := []processNode{contractNode("call", "Call Callee", 0, map[string]interface{}{
		"type": "api_rpc", "conv_id": float64(200), "group": "",
		"extra": map[string]interface{}{}, "extra_type": map[string]interface{}{"token": "string"},
	})}
	issues := validateCallContracts(nodes, local)
	if len(issues) != 1 || issues[0].Code != "CALLEE_INPUT_VALUE_MISSING" {
		t.Fatalf("a stale extra_type entry must not satisfy a strict call contract: %+v", issues)
	}
}

func TestProcessContracts_SendAllStillValidatesExplicitMappings(t *testing.T) {
	local := map[int]localProcessContract{
		200: {ID: 200, Title: "Callee", Inputs: []processParameter{{Name: "token", Type: "string", Required: true}}},
	}
	nodes := []processNode{contractNode("call", "Call Callee", 0, map[string]interface{}{
		"type": "api_rpc", "conv_id": float64(200), "group": "all",
		"extra": map[string]interface{}{"token": "{{token}}"}, "extra_type": map[string]interface{}{"token": "number"},
	})}
	issues := validateCallContracts(nodes, local)
	codes := map[string]int{}
	for _, issue := range issues {
		codes[issue.Code]++
	}
	if codes["SEND_ALL_CONTRACT_UNVERIFIED"] != 1 || codes["CALLEE_INPUT_TYPE_MISMATCH"] != 1 {
		t.Fatalf("Send all is advisory, but explicit mappings must still match the callee contract: %+v", issues)
	}
}

func TestProcessContracts_CalleeAdvisoryDoesNotBecomeBlocking(t *testing.T) {
	local := map[int]localProcessContract{
		200: {
			ID: 200, Title: "Callee", Path: "200_Callee.conv.json",
			Inputs: []processParameter{{Name: "token", Type: "string", Required: true}},
			Issues: []ContractIssue{{Code: "DYNAMIC_CONTRACT_UNVERIFIED", Advisory: true, Issue: "dynamic target"}},
		},
	}
	nodes := []processNode{contractNode("call", "Call Callee", 0, map[string]interface{}{
		"type": "api_rpc", "conv_id": float64(200), "group": "",
		"extra": map[string]interface{}{"token": "{{token}}"}, "extra_type": map[string]interface{}{"token": "string"},
	})}
	issues := validateCallContracts(nodes, local)
	for _, issue := range issues {
		if issue.Code == "CALLEE_CONTRACT_INVALID" {
			t.Fatalf("an advisory inside a callee must not be promoted to a blocking caller issue: %+v", issues)
		}
	}
}

func TestProcessContracts_CopyCreateUsesDataAndDataType(t *testing.T) {
	root := t.TempDir()
	callee := map[string]interface{}{
		"obj_id": float64(200), "title": "Callee",
		"params": []interface{}{contractParam("token", "string", "Authentication token", "required", "input")},
		"scheme": map[string]interface{}{"nodes": []interface{}{}},
	}
	data, _ := json.Marshal(callee)
	if err := os.WriteFile(filepath.Join(root, "200_Callee.conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	caller := map[string]interface{}{
		"obj_id": float64(100),
		"params": []interface{}{contractParam("token", "string", "Authentication token", "required", "input")},
	}
	nodes := []processNode{contractNode("copy", "Copy to Callee", 0, map[string]interface{}{
		"type": "api_copy", "mode": "create", "conv_id": float64(200), "group": "all", "send_parent_data": false,
		"data": map[string]interface{}{"token": "{{token}}"}, "data_type": map[string]interface{}{"token": "string"},
	})}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if len(report.Issues) != 0 {
		t.Fatalf("Copy Task create must validate data/data_type and group=all is not Send all: %+v", report.Issues)
	}
}

func TestProcessContracts_CopyCreateToStateIsAdvisoryOnly(t *testing.T) {
	root := t.TempDir()
	state := map[string]interface{}{
		"obj_id": float64(200), "title": "State store", "conv_type": "state", "params": []interface{}{},
		"scheme": map[string]interface{}{"nodes": []interface{}{}},
	}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(root, "200_state.conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	caller := map[string]interface{}{
		"obj_id": float64(100), "conv_type": "process",
		"params": []interface{}{contractParam("status", "string", "Initial state", "input")},
	}
	nodes := []processNode{contractNode("copy", "Create state task", 0, map[string]interface{}{
		"type": "api_copy", "mode": "create", "conv_id": float64(200), "send_parent_data": false,
		"data": map[string]interface{}{"status": "{{status}}"}, "data_type": map[string]interface{}{"status": "string"},
	})}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if issueCodes(report)["STATE_TARGET_CONTRACT_NOT_APPLICABLE"] != 1 || blockingContractIssueCount(report) != 0 {
		t.Fatalf("state-task fields must not be validated as process inputs: %+v", report.Issues)
	}
}

func TestProcessContracts_CopyModifyDoesNotValidateProcessInputs(t *testing.T) {
	root := t.TempDir()
	callee := map[string]interface{}{
		"obj_id": float64(200), "title": "Callee",
		"params": []interface{}{contractParam("token", "string", "Authentication token", "required", "input")},
		"scheme": map[string]interface{}{"nodes": []interface{}{}},
	}
	data, _ := json.Marshal(callee)
	if err := os.WriteFile(filepath.Join(root, "200_Callee.conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	caller := map[string]interface{}{"obj_id": float64(100), "params": []interface{}{}}
	nodes := []processNode{contractNode("modify", "Modify task", 0, map[string]interface{}{
		"type": "api_copy", "mode": "modify", "conv_id": float64(200), "group": "all",
		"data": map[string]interface{}{}, "data_type": map[string]interface{}{},
	})}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if issueCodes(report)["CALLEE_REQUIRED_INPUT_MISSING"] != 0 {
		t.Fatalf("api_copy modify updates an existing task and does not invoke the process Start contract: %+v", report.Issues)
	}
}

func TestProcessContracts_CopyCreateDoesNotProduceCalleeOutputs(t *testing.T) {
	root := t.TempDir()
	callee := map[string]interface{}{
		"obj_id": float64(200), "title": "Callee",
		"params": []interface{}{contractParam("result", "string", "Result", "output")},
		"scheme": map[string]interface{}{"nodes": []interface{}{}},
	}
	data, _ := json.Marshal(callee)
	if err := os.WriteFile(filepath.Join(root, "200_Callee.conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	caller := map[string]interface{}{"obj_id": float64(100), "params": []interface{}{}}
	nodes := []processNode{
		contractNode("copy", "Copy to Callee", 0, map[string]interface{}{
			"type": "api_copy", "mode": "create", "conv_id": float64(200), "group": "", "send_parent_data": false,
			"data": map[string]interface{}{}, "data_type": map[string]interface{}{},
		}, safetyGo("check")),
		contractNode("check", "Check result", 0, map[string]interface{}{
			"type": "go_if_const", "to_node_id": "done",
			"conditions": []interface{}{map[string]interface{}{"param": "result", "const": "ok", "fun": "eq", "cast": "string"}},
		}, safetyGo("done")),
		contractNode("done", "Done", 2),
	}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
		t.Fatalf("fire-and-forget Copy Task cannot make callee outputs available to the caller: %+v", report.Issues)
	}
}

func writeContractOutputCallee(t *testing.T, root string) {
	t.Helper()
	callee := map[string]interface{}{
		"obj_id": float64(200), "title": "Callee",
		"params": []interface{}{contractParam("result", "string", "Result", "required", "output")},
		"scheme": map[string]interface{}{"nodes": []interface{}{
			safetyTestNode("start", "Start", 1, []interface{}{safetyGo("reply")}, nil),
			safetyTestNode("reply", "Reply", 0, []interface{}{
				map[string]interface{}{
					"type": "api_rpc_reply", "mode": "key_value", "throw_exception": false,
					"res_data": map[string]interface{}{"result": "ok"}, "res_data_type": map[string]interface{}{"result": "string"},
				},
				safetyGo("final"),
			}, nil),
			safetyTestNode("final", "Final", 2, nil, nil),
		}},
	}
	data, _ := json.Marshal(callee)
	if err := os.WriteFile(filepath.Join(root, "200_Callee.conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func resultCheckNode(to string) processNode {
	return contractNode("check", "Check result", 0, map[string]interface{}{
		"type": "go_if_const", "to_node_id": to,
		"conditions": []interface{}{map[string]interface{}{"param": "result", "const": "ok", "fun": "eq", "cast": "string"}},
	}, safetyGo(to))
}

func staticResultCallNode(to string) processNode {
	return contractNode("call", "Call Callee", 0, map[string]interface{}{
		"type": "api_rpc", "conv_id": float64(200), "group": "", "err_node_id": "error",
		"extra": map[string]interface{}{}, "extra_type": map[string]interface{}{},
	}, safetyGo(to))
}

func TestProcessContracts_CalleeOutputMustPrecedeEveryRead(t *testing.T) {
	root := t.TempDir()
	writeContractOutputCallee(t, root)
	caller := map[string]interface{}{"obj_id": float64(100), "params": []interface{}{}}
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("check")),
		resultCheckNode("call"),
		staticResultCallNode("done"),
		contractNode("done", "Done", 2),
		contractNode("error", "Error", 2),
	}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
		t.Fatalf("a callee output read before the call is still an external input: %+v", report.Issues)
	}
}

func TestProcessContracts_CalleeOutputOnSuccessPathIsLocallyProduced(t *testing.T) {
	root := t.TempDir()
	writeContractOutputCallee(t, root)
	caller := map[string]interface{}{"obj_id": float64(100), "params": []interface{}{}}
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("call")),
		staticResultCallNode("check"),
		resultCheckNode("done"),
		contractNode("done", "Done", 2),
		contractNode("error", "Error", 2),
	}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 0 {
		t.Fatalf("a validated callee output on the dominated success path is locally produced: %+v", report.Issues)
	}
}

func TestProcessContracts_CalleeSharedSuccessAndErrorTargetIsNotProven(t *testing.T) {
	root := t.TempDir()
	writeContractOutputCallee(t, root)
	caller := map[string]interface{}{"obj_id": float64(100), "params": []interface{}{}}
	call := staticResultCallNode("check")
	call.logics[0]["err_node_id"] = "check"
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("call")),
		call,
		resultCheckNode("done"),
		contractNode("done", "Done", 2),
	}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
		t.Fatalf("a shared success/error target cannot prove that the callee returned result: %+v", report.Issues)
	}
}

func TestProcessContracts_AsyncCalleeDoesNotProduceImmediateOutput(t *testing.T) {
	root := t.TempDir()
	writeContractOutputCallee(t, root)
	caller := map[string]interface{}{"obj_id": float64(100), "params": []interface{}{}}
	call := staticResultCallNode("check")
	call.logics[0]["is_sync"] = false
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("call")),
		call,
		resultCheckNode("done"),
		contractNode("done", "Done", 2),
		contractNode("error", "Error", 2),
	}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
		t.Fatalf("an asynchronous Call Process cannot make its outputs immediately available: %+v", report.Issues)
	}
}

func TestProcessContracts_DuplicateProcessIDsAreAmbiguous(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"200_A.conv.json", "200_B.conv.json"} {
		callee := map[string]interface{}{
			"obj_id": float64(200), "title": name,
			"params": []interface{}{contractParam("token", "string", "Authentication token", "input")},
			"scheme": map[string]interface{}{"nodes": []interface{}{}},
		}
		data, _ := json.Marshal(callee)
		if err := os.WriteFile(filepath.Join(root, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	caller := map[string]interface{}{"obj_id": float64(100), "params": []interface{}{}}
	nodes := []processNode{contractNode("call", "Call duplicate", 0, map[string]interface{}{
		"type": "api_rpc", "conv_id": float64(200), "group": "",
		"extra": map[string]interface{}{}, "extra_type": map[string]interface{}{},
	})}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if issueCodes(report)["CALLEE_CONTRACT_AMBIGUOUS"] != 1 {
		t.Fatalf("duplicate process IDs must not silently select one contract: %+v", report.Issues)
	}
}

func TestProcessContracts_InvalidCalleeContractBlocksStaticTrust(t *testing.T) {
	root := t.TempDir()
	callee := map[string]interface{}{
		"obj_id": float64(200), "title": "Callee",
		"params": []interface{}{map[string]interface{}{"name": "token", "type": "string"}},
		"scheme": map[string]interface{}{"nodes": []interface{}{}},
	}
	data, _ := json.Marshal(callee)
	if err := os.WriteFile(filepath.Join(root, "200_Callee.conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	caller := map[string]interface{}{"obj_id": float64(100), "params": []interface{}{}}
	nodes := []processNode{contractNode("call", "Call Callee", 0, map[string]interface{}{
		"type": "api_rpc", "conv_id": float64(200), "group": "",
		"extra": map[string]interface{}{}, "extra_type": map[string]interface{}{},
	})}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if issueCodes(report)["CALLEE_CONTRACT_INVALID"] != 1 {
		t.Fatalf("expected invalid callee contract issue, got %+v", report.Issues)
	}
}

func TestProcessContracts_CalleeDeclarationMustMatchItsReply(t *testing.T) {
	root := t.TempDir()
	callee := map[string]interface{}{
		"obj_id": float64(200), "title": "Callee",
		"params": []interface{}{contractParam("result", "string", "Operation result", "required", "output")},
		"scheme": map[string]interface{}{"nodes": []interface{}{
			safetyTestNode("start", "Start", 1, []interface{}{safetyGo("final")}, nil),
			safetyTestNode("final", "Final", 2, nil, nil),
		}},
	}
	data, _ := json.Marshal(callee)
	if err := os.WriteFile(filepath.Join(root, "200_Callee.conv.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	caller := map[string]interface{}{"obj_id": float64(100), "params": []interface{}{}}
	nodes := []processNode{contractNode("call", "Call Callee", 0, map[string]interface{}{
		"type": "api_rpc", "conv_id": float64(200), "group": "",
		"extra": map[string]interface{}{}, "extra_type": map[string]interface{}{},
	})}
	report := analyzeProcessContract(caller, nodes, contractPolicy(root, policyModeStrict, "project"))
	if issueCodes(report)["CALLEE_CONTRACT_INVALID"] != 1 {
		t.Fatalf("a callee with a stale declared output must not be trusted: %+v", report.Issues)
	}
}

func TestProcessContracts_ParamShapeAndDescriptionAreStrict(t *testing.T) {
	proc := map[string]interface{}{
		"params": []interface{}{map[string]interface{}{
			"name": "id", "type": "string", "descr": "", "flags": []interface{}{"input"},
		}},
	}
	report := analyzeProcessContract(proc, nil, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	codes := issueCodes(report)
	if codes["PARAM_SHAPE"] != 1 || codes["PARAM_DESCRIPTION"] != 1 {
		t.Fatalf("expected exact shape and description findings, got %+v", report.Issues)
	}
}

func TestProcessContracts_BuiltinFunctionNameIsNotAnInput(t *testing.T) {
	refs := referencedVariablesInString("{{$.sha256($.math(retry_count+1))}}")
	if len(refs) != 1 || refs[0] != "retry_count" {
		t.Fatalf("expected only retry_count, got %v", refs)
	}
}

func TestProcessContracts_CodeBracketAccessIsTracked(t *testing.T) {
	proc := map[string]interface{}{
		"params": []interface{}{contractParam("customer_id", "string", "Customer identifier", "required", "input")},
	}
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("code")),
		contractNode("code", "Transform", 0, map[string]interface{}{
			"type": "api_code", "src": `data["result"] = data['customer_id'];`, "err_node_id": "error",
		}, safetyGo("done")),
		contractNode("done", "Done", 2), contractNode("error", "Error", 2),
	}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 0 || issueCodes(report)["CODE_DYNAMIC_ROOT_ACCESS"] != 0 {
		t.Fatalf("literal bracket keys must be analyzed like dot notation: %+v", report.Issues)
	}
}

func activeContractStubNode(outputName string, branches ...[]interface{}) processNode {
	node := contractNode("call", "Stubbed call", 4, map[string]interface{}{
		"type": "api_rpc", "conv_id": float64(200), "group": "all", "err_node_id": "error",
		"extra": map[string]interface{}{}, "extra_type": map[string]interface{}{},
	}, safetyGo("check"))
	if len(branches) == 0 {
		branches = append(branches, []interface{}{map[string]interface{}{
			"type": "api_rpc_reply", "mode": "keys", "throw_exception": false,
			"res_data": []interface{}{outputName}, "res_data_type": map[string]interface{}{outputName: "string"},
		}})
	}
	rawBranches := make([]interface{}, len(branches))
	for i := range branches {
		rawBranches[i] = branches[i]
	}
	node.stub = map[string]interface{}{"logics": rawBranches}
	return node
}

func TestProcessContracts_ActiveStubUsesMockOutputsNotTargetOutputs(t *testing.T) {
	proc := map[string]interface{}{"params": []interface{}{}}
	base := []processNode{
		contractNode("start", "Start", 1, safetyGo("call")),
		activeContractStubNode("result"),
		resultCheckNode("done"),
		contractNode("done", "Done", 2),
		contractNode("error", "Error", 2),
	}
	report := analyzeProcessContract(proc, base, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 0 {
		t.Fatalf("a guaranteed active-Stub output must be treated as locally produced: %+v", report.Issues)
	}

	base[1] = activeContractStubNode("different_result")
	report = analyzeProcessContract(proc, base, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
		t.Fatalf("the real target contract must not supply outputs while Stub Mode bypasses it: %+v", report.Issues)
	}
	if !contractStaticProcessTargets([]processNode{base[1]})[200] {
		t.Fatal("contract validation should still load the intended target contract for Stub input compatibility")
	}
}

func TestProcessContracts_ActiveStubConditionsParticipateInInputs(t *testing.T) {
	condition := map[string]interface{}{
		"type": "go_if_const",
		"conditions": []interface{}{map[string]interface{}{
			"param": "{{mode}}", "const": "error", "fun": "eq", "cast": "string",
		}},
	}
	exception := map[string]interface{}{
		"type": "api_rpc_reply", "mode": "key_value", "throw_exception": true,
		"res_data": map[string]interface{}{}, "res_data_type": map[string]interface{}{},
	}
	success := map[string]interface{}{
		"type": "api_rpc_reply", "mode": "keys", "throw_exception": false,
		"res_data": []interface{}{"result"}, "res_data_type": map[string]interface{}{"result": "string"},
	}
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("call")),
		activeContractStubNode("", []interface{}{condition, exception}, []interface{}{success}),
		resultCheckNode("done"), contractNode("done", "Done", 2), contractNode("error", "Error", 2),
	}
	report := analyzeProcessContract(map[string]interface{}{"params": []interface{}{}}, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
		t.Fatalf("Stub conditions evaluated against sent task fields must participate in the process contract: %+v", report.Issues)
	}
	for _, issue := range report.Issues {
		if issue.Code == "INPUT_UNDECLARED" && issue.Parameter != "mode" {
			t.Fatalf("expected only the Stub condition field mode, got %+v", report.Issues)
		}
	}
}

func TestProcessContracts_CodeEqualityIsAReadNotAnAssignment(t *testing.T) {
	for _, source := range []string{`if (data.customer_id == "x") { data.result = true; }`, `if (data["customer_id"] === "x") { data.result = true; }`} {
		proc := map[string]interface{}{"params": []interface{}{}}
		nodes := []processNode{contractNode("code", "Compare", 0, map[string]interface{}{
			"type": "api_code", "src": source,
		})}
		report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
		if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
			t.Fatalf("equality access must keep customer_id as an inferred input for %q: %+v", source, report.Issues)
		}
	}
}

func TestProcessContracts_CodeAssignmentRightHandSideRemainsARead(t *testing.T) {
	for _, source := range []string{`data.total=data.amount+1;`, `data["total"] = data['amount'] + 1;`} {
		proc := map[string]interface{}{"params": []interface{}{}}
		nodes := []processNode{contractNode("code", "Calculate", 0, map[string]interface{}{
			"type": "api_code", "src": source,
		})}
		report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
		if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
			t.Fatalf("right-hand data.amount must remain an inferred input for %q: %+v", source, report.Issues)
		}
		for _, issue := range report.Issues {
			if issue.Code == "INPUT_UNDECLARED" && issue.Parameter != "amount" {
				t.Fatalf("assignment target %q was misclassified as input for %q: %+v", issue.Parameter, source, report.Issues)
			}
		}
	}
}

func TestProcessContracts_CodeReadModifyWriteRemainsAnInput(t *testing.T) {
	proc := map[string]interface{}{"params": []interface{}{}}
	nodes := []processNode{contractNode("code", "Increment", 0, map[string]interface{}{
		"type": "api_code", "src": `data.counter = data.counter + 1;`,
	})}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
		t.Fatalf("read-modify-write requires an initial counter input unless another node dominates it: %+v", report.Issues)
	}
}

func TestProcessContracts_CodeDynamicRootAccessIsUnprovable(t *testing.T) {
	proc := map[string]interface{}{"params": []interface{}{}}
	nodes := []processNode{contractNode("code", "Transform", 0, map[string]interface{}{
		"type": "api_code", "src": `data[key] = data.value;`, "err_node_id": "error",
	})}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["CODE_DYNAMIC_ROOT_ACCESS"] != 1 {
		t.Fatalf("computed root keys must prevent a false strict-contract guarantee: %+v", report.Issues)
	}
}

func TestProcessContracts_CodeRootAliasIsUnprovable(t *testing.T) {
	proc := map[string]interface{}{"params": []interface{}{}}
	nodes := []processNode{contractNode("code", "Transform", 0, map[string]interface{}{
		"type": "api_code", "src": `const task = data; task.result = true;`, "err_node_id": "error",
	})}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["CODE_DYNAMIC_ROOT_ACCESS"] != 1 {
		t.Fatalf("aliasing the whole data object must prevent a false strict-contract guarantee: %+v", report.Issues)
	}
}

func TestProcessContracts_CodeCommentsAndStringsDoNotCreateDynamicAccess(t *testing.T) {
	source := "// data.ignored and data[key] are intentionally not used\nconst label = \"data.quoted\"; data.customer_id === 'x';"
	if codeHasUnsupportedRootAccess(source) {
		t.Fatalf("comments and ordinary strings must not create an unsupported root access finding: %s", source)
	}
	proc := map[string]interface{}{"params": []interface{}{}}
	nodes := []processNode{contractNode("code", "Inspect", 0, map[string]interface{}{
		"type": "api_code", "src": source,
	})}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 1 {
		t.Fatalf("only executable data.customer_id should be inferred: %+v", report.Issues)
	}
	for _, issue := range report.Issues {
		if issue.Code == "INPUT_UNDECLARED" && issue.Parameter != "customer_id" {
			t.Fatalf("comment/string token %q was misclassified as input: %+v", issue.Parameter, report.Issues)
		}
	}
}

func TestProcessContracts_NestedDataPropertyIsNotRootTaskData(t *testing.T) {
	source := `const total = request.data.total; request.data.result = total;`
	if codeHasUnsupportedRootAccess(source) {
		t.Fatalf("a nested property named data is not the Corezoid root data object: %s", source)
	}
	proc := map[string]interface{}{"params": []interface{}{}}
	nodes := []processNode{contractNode("code", "Inspect response", 0, map[string]interface{}{
		"type": "api_code", "src": source,
	})}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 0 {
		t.Fatalf("request.data fields must not be inferred as process inputs: %+v", report.Issues)
	}
}

func TestProcessContracts_SystemReferencesAreNotInputs(t *testing.T) {
	value := "{{env_var[@api-host]}} {{node[abc].count}} {{conv[@store].ref[{{customer_id}}].status}} {{root.task_id}}"
	refs := referencedVariablesInString(value)
	if len(refs) != 1 || refs[0] != "customer_id" {
		t.Fatalf("expected only nested task-data reference, got %v", refs)
	}
}

func TestProcessContracts_SystemLikeParameterNamesRemainInputs(t *testing.T) {
	refs := referencedVariablesInString("{{node_id}} {{conv_status}} {{conveyor_name}} {{env_variable}}")
	want := []string{"conv_status", "conveyor_name", "env_variable", "node_id"}
	if len(refs) != len(want) {
		t.Fatalf("system-like business parameter names were dropped: got %v, want %v", refs, want)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Fatalf("system-like business parameter names = %v, want %v", refs, want)
		}
	}
}

func TestProcessContracts_InvalidFieldAndFlagTypes(t *testing.T) {
	param := contractParam("customer_id", "string", "Customer identifier", "input", "mystery", "input")
	param["flags"] = append(param["flags"].([]interface{}), float64(7))
	param["regex"] = float64(7)
	proc := map[string]interface{}{"params": []interface{}{param}}
	report := analyzeProcessContract(proc, nil, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	codes := issueCodes(report)
	if codes["PARAM_FIELD_TYPE"] != 1 || codes["PARAM_FLAG_INVALID"] != 2 || codes["PARAM_FLAG_DUPLICATE"] != 1 {
		t.Fatalf("expected strict field and flag validation, got %+v", report.Issues)
	}
}

func TestProcessContracts_UnreachableNodesDoNotDefineContract(t *testing.T) {
	proc := map[string]interface{}{"params": []interface{}{}, "scheme": map[string]interface{}{}}
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("final")),
		contractNode("final", "Final", 2),
		contractNode("orphan", "Unused", 0, map[string]interface{}{
			"type": "go_if_const", "to_node_id": "final",
			"conditions": []interface{}{map[string]interface{}{"param": "unused_input", "const": "x", "fun": "eq", "cast": "string"}},
		}),
	}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	if issueCodes(report)["INPUT_UNDECLARED"] != 0 {
		t.Fatalf("unreachable nodes must not expand the public process contract: %+v", report.Issues)
	}
}

func TestProcessContracts_ReplyTypeWithoutValueDoesNotSatisfyOutput(t *testing.T) {
	proc := map[string]interface{}{
		"params": []interface{}{contractParam("result", "string", "Result", "required", "output")},
	}
	nodes := []processNode{
		contractNode("start", "Start", 1, safetyGo("reply")),
		contractNode("reply", "Reply", 0, map[string]interface{}{
			"type": "api_rpc_reply", "res_data": map[string]interface{}{},
			"res_data_type": map[string]interface{}{"result": "string"},
		}),
	}
	report := analyzeProcessContract(proc, nodes, contractPolicy(t.TempDir(), policyModeStrict, "self"))
	codes := issueCodes(report)
	if codes["OUTPUT_REQUIRED_MISSING"] != 1 || codes["OUTPUT_STALE"] != 1 {
		t.Fatalf("a type-only reply entry must not count as returned data: %+v", report.Issues)
	}
}
