package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type ContractIssue struct {
	Code          string
	Parameter     string
	NodeID        string
	NodeTitle     string
	SuggestedType string
	Advisory      bool
	Issue         string
}

type ContractReport struct {
	Mode            string
	Inputs          []string
	Outputs         []string
	Issues          []ContractIssue
	DependencyScope string
	SkippedReason   string
}

type processParameter struct {
	Name        string
	Type        string
	Description string
	Input       bool
	Output      bool
	Required    bool
	Raw         map[string]interface{}
}

type contractProducerEdge struct {
	From string
	To   string
}

var (
	templateExprRE         = regexp.MustCompile(`\{\{([^{}]+)\}\}`)
	identifierRE           = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_.]*`)
	codeDataRE             = regexp.MustCompile(`\bdata\.([A-Za-z_][A-Za-z0-9_]*)`)
	codeAssignRE           = regexp.MustCompile(`\bdata\.([A-Za-z_][A-Za-z0-9_]*)\s*=([^=]|$)`)
	codeDataBracketRE      = regexp.MustCompile(`\bdata\s*\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\]`)
	codeAssignBracketRE    = regexp.MustCompile(`\bdata\s*\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\]\s*=([^=]|$)`)
	codeBareDataRE         = regexp.MustCompile(`\bdata\b`)
	contractReservedTokens = map[string]bool{
		"math": true, "date": true, "random": true, "unixtime": true,
		"map": true, "filter": true, "base64_encode": true,
		"md5": true, "md5_hex": true, "sha1": true, "sha1_hex": true,
		"sha224": true, "sha224_hex": true, "sha256": true, "sha256_hex": true,
		"sha384": true, "sha384_hex": true, "sha512": true, "sha512_hex": true,
		"true": true, "false": true, "null": true,
	}
)

func stringFlags(v interface{}) []string {
	switch flags := v.(type) {
	case []interface{}:
		out := make([]string, 0, len(flags))
		for _, raw := range flags {
			if flag, ok := raw.(string); ok {
				out = append(out, flag)
			}
		}
		return out
	case []string:
		return append([]string(nil), flags...)
	default:
		return nil
	}
}

func parseDeclaredParameters(proc map[string]interface{}) ([]processParameter, []ContractIssue) {
	rawParams, ok := proc["params"].([]interface{})
	if !ok {
		return nil, []ContractIssue{{Code: "PARAMS_MISSING", Issue: "top-level params must be an array, even when the process has no inputs or outputs"}}
	}
	allowedTypes := map[string]bool{"string": true, "number": true, "boolean": true, "object": true, "array": true}
	requiredKeys := []string{"name", "type", "descr", "flags", "regex", "regex_error_text"}
	allowedKeys := map[string]bool{"name": true, "type": true, "descr": true, "flags": true, "regex": true, "regex_error_text": true}
	seen := map[string]bool{}
	var params []processParameter
	var issues []ContractIssue
	for i, raw := range rawParams {
		m, ok := raw.(map[string]interface{})
		if !ok {
			issues = append(issues, ContractIssue{Code: "PARAM_INVALID", Issue: fmt.Sprintf("params[%d] must be an object", i)})
			continue
		}
		missing := make([]string, 0)
		for _, key := range requiredKeys {
			if _, exists := m[key]; !exists {
				missing = append(missing, key)
			}
		}
		var unexpected []string
		for key := range m {
			if !allowedKeys[key] {
				unexpected = append(unexpected, key)
			}
		}
		sort.Strings(unexpected)
		name, _ := m["name"].(string)
		typ, _ := m["type"].(string)
		descr, _ := m["descr"].(string)
		if len(missing) > 0 {
			issues = append(issues, ContractIssue{
				Code: "PARAM_SHAPE", Parameter: name,
				Issue: fmt.Sprintf("parameter %q is missing required field(s): %s", name, strings.Join(missing, ", ")),
			})
		}
		if len(unexpected) > 0 {
			issues = append(issues, ContractIssue{
				Code: "PARAM_SHAPE", Parameter: name,
				Issue: fmt.Sprintf("parameter %q has unsupported field(s): %s", name, strings.Join(unexpected, ", ")),
			})
		}
		if strings.TrimSpace(name) == "" {
			issues = append(issues, ContractIssue{Code: "PARAM_NAME", Issue: fmt.Sprintf("params[%d] has an empty name", i)})
			continue
		}
		if seen[name] {
			issues = append(issues, ContractIssue{Code: "PARAM_DUPLICATE", Parameter: name, Issue: fmt.Sprintf("parameter %q is declared more than once", name)})
			continue
		}
		seen[name] = true
		if !allowedTypes[typ] {
			issues = append(issues, ContractIssue{Code: "PARAM_TYPE", Parameter: name, Issue: fmt.Sprintf("parameter %q has unsupported type %q", name, typ)})
		}
		if strings.TrimSpace(descr) == "" {
			issues = append(issues, ContractIssue{Code: "PARAM_DESCRIPTION", Parameter: name, Issue: fmt.Sprintf("parameter %q must have a non-empty description", name)})
		}
		for _, field := range []string{"name", "type", "descr", "regex", "regex_error_text"} {
			if value, exists := m[field]; exists {
				if _, ok := value.(string); !ok {
					issues = append(issues, ContractIssue{Code: "PARAM_FIELD_TYPE", Parameter: name, Issue: fmt.Sprintf("parameter %q field %q must be a string", name, field)})
				}
			}
		}
		if value, exists := m["flags"]; exists && value != nil {
			switch value.(type) {
			case []interface{}, []string:
			default:
				issues = append(issues, ContractIssue{Code: "PARAM_FIELD_TYPE", Parameter: name, Issue: fmt.Sprintf("parameter %q field %q must be an array", name, "flags")})
			}
		}
		flags := stringFlags(m["flags"])
		if rawFlags, ok := m["flags"].([]interface{}); ok {
			for flagIndex, rawFlag := range rawFlags {
				if _, isString := rawFlag.(string); !isString {
					issues = append(issues, ContractIssue{Code: "PARAM_FLAG_INVALID", Parameter: name, Issue: fmt.Sprintf("parameter %q flag at index %d must be a string", name, flagIndex)})
				}
			}
		}
		p := processParameter{Name: name, Type: typ, Description: descr, Raw: m}
		seenFlags := map[string]bool{}
		for _, flag := range flags {
			if seenFlags[flag] {
				issues = append(issues, ContractIssue{Code: "PARAM_FLAG_DUPLICATE", Parameter: name, Issue: fmt.Sprintf("parameter %q repeats flag %q", name, flag)})
				continue
			}
			seenFlags[flag] = true
			switch flag {
			case "input":
				p.Input = true
			case "output":
				p.Output = true
			case "required":
				p.Required = true
			case "auto-clear":
			default:
				issues = append(issues, ContractIssue{Code: "PARAM_FLAG_INVALID", Parameter: name, Issue: fmt.Sprintf("parameter %q has unsupported flag %q", name, flag)})
			}
		}
		if !p.Input && !p.Output {
			issues = append(issues, ContractIssue{Code: "PARAM_ROLE", Parameter: name, Issue: fmt.Sprintf("parameter %q must be marked input and/or output", name)})
		}
		params = append(params, p)
	}
	return params, issues
}

func contractParamMaps(params []processParameter) (map[string]processParameter, map[string]processParameter) {
	inputs := map[string]processParameter{}
	outputs := map[string]processParameter{}
	for _, p := range params {
		if p.Input {
			inputs[p.Name] = p
		}
		if p.Output {
			outputs[p.Name] = p
		}
	}
	return inputs, outputs
}

func rootContractVariable(expr string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	lower := strings.ToLower(expr)
	if contractReservedTokens[lower] {
		return ""
	}
	for _, prefix := range []string{"root.", "env_var[", "env_var.", "node[", "node.", "conv[", "conv.", "conveyor[", "conveyor.", "$"} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	if strings.HasPrefix(expr, "__") {
		return ""
	}
	if idx := strings.IndexAny(expr, ".[("); idx >= 0 {
		expr = expr[:idx]
	}
	if identifierRE.FindString(expr) != expr {
		return ""
	}
	return expr
}

func referencedVariablesInString(s string) []string {
	seen := map[string]bool{}
	for _, match := range templateExprRE.FindAllStringSubmatch(s, -1) {
		expr := match[1]
		lowerExpr := strings.ToLower(strings.TrimSpace(expr))
		if strings.HasPrefix(lowerExpr, "env_var[") || strings.HasPrefix(lowerExpr, "env_var.") || strings.HasPrefix(lowerExpr, "node[") ||
			strings.HasPrefix(lowerExpr, "conv[") || strings.HasPrefix(lowerExpr, "conveyor[") ||
			strings.HasPrefix(lowerExpr, "root.") {
			continue
		}
		for _, bounds := range identifierRE.FindAllStringIndex(expr, -1) {
			if bounds[0] >= 2 && expr[bounds[0]-2:bounds[0]] == "$." {
				continue
			}
			if bounds[0] >= 1 && expr[bounds[0]-1] == '@' {
				continue
			}
			token := expr[bounds[0]:bounds[1]]
			if variable := rootContractVariable(token); variable != "" {
				seen[variable] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for variable := range seen {
		out = append(out, variable)
	}
	sort.Strings(out)
	return out
}

func walkContractReferences(v interface{}, refs map[string]bool) {
	switch value := v.(type) {
	case string:
		for _, variable := range referencedVariablesInString(value) {
			refs[variable] = true
		}
	case []interface{}:
		for _, item := range value {
			walkContractReferences(item, refs)
		}
	case map[string]interface{}:
		for _, item := range value {
			walkContractReferences(item, refs)
		}
	}
}

func walkContractLogicReferences(logic map[string]interface{}, refs map[string]bool) {
	t, _ := logic["type"].(string)
	for field, value := range logic {
		// API response values are destination parameter names, not values read
		// from the task. Code source is analyzed separately as data.name access.
		if t == "api" && (field == "response" || field == "response_type") {
			continue
		}
		if t == "api_code" && (field == "src" || field == "code") {
			continue
		}
		walkContractReferences(value, refs)
	}
}

func exactTemplateVariable(v interface{}) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	match := templateExprRE.FindStringSubmatch(s)
	if len(match) != 2 || match[0] != s {
		return ""
	}
	return rootContractVariable(strings.TrimSpace(match[1]))
}

func addTypeHint(hints map[string]map[string]bool, variable, typ string) {
	if variable == "" || typ == "" {
		return
	}
	if hints[variable] == nil {
		hints[variable] = map[string]bool{}
	}
	hints[variable][typ] = true
}

func singleTypeHint(hints map[string]map[string]bool, variable string) string {
	if len(hints[variable]) != 1 {
		return ""
	}
	for typ := range hints[variable] {
		return typ
	}
	return ""
}

func sortedTypeHints(hints map[string]map[string]bool, variable string) []string {
	values := make([]string, 0, len(hints[variable]))
	for typ := range hints[variable] {
		values = append(values, typ)
	}
	sort.Strings(values)
	return values
}

func recordContractProducer(targets map[string]map[contractProducerEdge]bool, node processNode, variable string) {
	if variable == "" {
		return
	}
	errorTargets := map[string]bool{}
	for _, logic := range node.logics {
		if target, _ := logic["err_node_id"].(string); target != "" {
			errorTargets[target] = true
		}
	}
	for _, logic := range node.logics {
		if t, _ := logic["type"].(string); t != "go" {
			continue
		}
		target, _ := logic["to_node_id"].(string)
		if target == "" || errorTargets[target] {
			continue
		}
		if targets[variable] == nil {
			targets[variable] = map[contractProducerEdge]bool{}
		}
		targets[variable][contractProducerEdge{From: node.id, To: target}] = true
	}
}

func codeDataIsRootAt(source string, start int) bool {
	if start < 0 || start >= len(source) {
		return false
	}
	if start > 0 && source[start-1] == '$' {
		return false
	}
	for i := start - 1; i >= 0; i-- {
		switch source[i] {
		case ' ', '\t', '\r', '\n':
			continue
		case '.':
			return false
		default:
			return true
		}
	}
	return true
}

func codeReadsAfterMaskingAssignmentTargets(source string, node processNode, producerTargets map[string]map[contractProducerEdge]bool) string {
	masked := []byte(source)
	executable := stripCodeCommentsAndQuotedStrings(source)
	for _, re := range []*regexp.Regexp{codeAssignRE, codeAssignBracketRE} {
		for _, bounds := range re.FindAllStringSubmatchIndex(source, -1) {
			if len(bounds) < 4 || bounds[0] >= len(executable) || executable[bounds[0]] == ' ' || !codeDataIsRootAt(source, bounds[0]) {
				continue
			}
			name := source[bounds[2]:bounds[3]]
			recordContractProducer(producerTargets, node, name)
			equalsOffset := strings.IndexByte(source[bounds[3]:bounds[1]], '=')
			if equalsOffset < 0 {
				continue
			}
			for i := bounds[0]; i <= bounds[3]+equalsOffset; i++ {
				masked[i] = ' '
			}
		}
	}
	return string(masked)
}

func producedAndReferencedVariables(nodes []processNode) (map[string]map[contractProducerEdge]bool, map[string]bool, map[string]map[string]bool, map[string]map[string]bool) {
	producerTargets := map[string]map[contractProducerEdge]bool{}
	refs := map[string]bool{}
	hints := map[string]map[string]bool{}
	refNodes := map[string]map[string]bool{}
	for _, n := range nodes {
		nodeRefs := map[string]bool{}
		if isActiveStubNode(n) {
			walkStubContractReferences(n.stub, nodeRefs)
		}
		for _, lg := range n.logics {
			walkContractLogicReferences(lg, nodeRefs)
			t, _ := lg["type"].(string)
			switch t {
			case "set_param":
				extra, _ := lg["extra"].(map[string]interface{})
				types, _ := lg["extra_type"].(map[string]interface{})
				for name := range extra {
					recordContractProducer(producerTargets, n, name)
					if typ, _ := types[name].(string); typ != "" {
						addTypeHint(hints, name, typ)
					}
				}
			case "api":
				response, _ := lg["response"].(map[string]interface{})
				responseTypes, _ := lg["response_type"].(map[string]interface{})
				for key, rawName := range response {
					name := normalizeVariableRef(rawName)
					if variable := rootContractVariable(name); variable != "" {
						recordContractProducer(producerTargets, n, variable)
						if typ, _ := responseTypes[key].(string); typ != "" {
							addTypeHint(hints, variable, typ)
						}
					}
				}
			case "api_code":
				src, _ := lg["src"].(string)
				if src == "" {
					src, _ = lg["code"].(string)
				}
				readSource := codeReadsAfterMaskingAssignmentTargets(src, n, producerTargets)
				executable := stripCodeCommentsAndQuotedStrings(src)
				for _, bounds := range codeDataRE.FindAllStringSubmatchIndex(readSource, -1) {
					if bounds[0] < len(executable) && executable[bounds[0]] != ' ' && codeDataIsRootAt(src, bounds[0]) {
						nodeRefs[readSource[bounds[2]:bounds[3]]] = true
					}
				}
				for _, bounds := range codeDataBracketRE.FindAllStringSubmatchIndex(readSource, -1) {
					if bounds[0] < len(executable) && executable[bounds[0]] != ' ' && codeDataIsRootAt(src, bounds[0]) {
						nodeRefs[readSource[bounds[2]:bounds[3]]] = true
					}
				}
			}

			if conditions := toMapSlice(lg["conditions"]); len(conditions) > 0 {
				for _, condition := range conditions {
					cast, _ := condition["cast"].(string)
					if variable := rootContractVariable(normalizeVariableRef(condition["param"])); variable != "" {
						nodeRefs[variable] = true
						addTypeHint(hints, variable, cast)
					}
					if variable := exactTemplateVariable(condition["const"]); variable != "" {
						addTypeHint(hints, variable, cast)
					}
				}
			}
			values, _ := lg["extra"].(map[string]interface{})
			valueTypes, _ := lg["extra_type"].(map[string]interface{})
			if t == "api_copy" {
				values, _ = lg["data"].(map[string]interface{})
				valueTypes, _ = lg["data_type"].(map[string]interface{})
			}
			for key, value := range values {
				if variable := exactTemplateVariable(value); variable != "" {
					typ, _ := valueTypes[key].(string)
					addTypeHint(hints, variable, typ)
				}
			}
		}
		for _, sem := range n.sems {
			walkContractReferences(sem, nodeRefs)
		}
		for variable := range nodeRefs {
			refs[variable] = true
			if refNodes[variable] == nil {
				refNodes[variable] = map[string]bool{}
			}
			refNodes[variable][n.id] = true
		}
	}
	return producerTargets, refs, hints, refNodes
}

func walkStubContractReferences(stub map[string]interface{}, refs map[string]bool) {
	walkContractReferences(stub, refs)
	branches, _ := stub["logics"].([]interface{})
	for _, rawBranch := range branches {
		branch, _ := rawBranch.([]interface{})
		for _, rawLogic := range branch {
			logic, _ := rawLogic.(map[string]interface{})
			if t, _ := logic["type"].(string); t != "go_if_const" {
				continue
			}
			for _, condition := range toMapSlice(logic["conditions"]) {
				if variable := rootContractVariable(normalizeVariableRef(condition["param"])); variable != "" {
					refs[variable] = true
				}
			}
		}
	}
}

func codeContractIssues(nodes []processNode) []ContractIssue {
	var issues []ContractIssue
	for _, node := range nodes {
		for _, logic := range node.logics {
			if t, _ := logic["type"].(string); t != "api_code" {
				continue
			}
			source, _ := logic["src"].(string)
			if source == "" {
				source, _ = logic["code"].(string)
			}
			if codeHasUnsupportedRootAccess(source) {
				title := node.title
				if title == "" {
					title = "(untitled)"
				}
				issues = append(issues, ContractIssue{
					Code: "CODE_DYNAMIC_ROOT_ACCESS", NodeID: node.id, NodeTitle: title,
					Issue: "Code accesses the root data object through a computed key, alias, destructuring, enumeration, or another unsupported form; the process input/output contract cannot be proven statically. Use literal data.name/data[\"name\"] access, put dynamic keys inside a declared object parameter, or use warn mode for this process",
				})
			}
		}
	}
	return issues
}

// codeHasUnsupportedRootAccess keeps strict contract analysis honest without
// pretending to be a complete JavaScript parser. Literal root accesses are
// removed first; any remaining data identifier outside ordinary quoted strings
// and comments means the root object is being accessed in a shape we cannot
// prove. Backtick templates stay visible so interpolated root access fails safe.
func codeHasUnsupportedRootAccess(source string) bool {
	executable := stripCodeCommentsAndQuotedStrings(source)
	supported := map[int]bool{}
	for _, re := range []*regexp.Regexp{codeDataRE, codeDataBracketRE} {
		for _, bounds := range re.FindAllStringIndex(source, -1) {
			if bounds[0] < len(executable) && executable[bounds[0]] != ' ' && codeDataIsRootAt(source, bounds[0]) {
				supported[bounds[0]] = true
			}
		}
	}
	for _, bounds := range codeBareDataRE.FindAllStringIndex(executable, -1) {
		if codeDataIsRootAt(source, bounds[0]) && !supported[bounds[0]] {
			return true
		}
	}
	return false
}

func stripCodeCommentsAndQuotedStrings(source string) string {
	b := []byte(source)
	out := append([]byte(nil), b...)
	for i := 0; i < len(b); {
		switch {
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '/':
			out[i], out[i+1] = ' ', ' '
			i += 2
			for i < len(b) && b[i] != '\n' {
				out[i] = ' '
				i++
			}
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '*':
			out[i], out[i+1] = ' ', ' '
			i += 2
			for i < len(b) {
				if b[i] == '*' && i+1 < len(b) && b[i+1] == '/' {
					out[i], out[i+1] = ' ', ' '
					i += 2
					break
				}
				if b[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
		case b[i] == '\'' || b[i] == '"':
			quote := b[i]
			out[i] = ' '
			i++
			for i < len(b) {
				if b[i] == '\\' && i+1 < len(b) {
					out[i], out[i+1] = ' ', ' '
					i += 2
					continue
				}
				if b[i] != '\n' {
					out[i] = ' '
				}
				end := b[i] == quote
				i++
				if end {
					break
				}
			}
		default:
			i++
		}
	}
	return string(out)
}

func calleeOutputSuccessTargets(nodes []processNode, calleeOutputs map[int][]processParameter, variable string) map[contractProducerEdge]bool {
	producerTargets := map[contractProducerEdge]bool{}
	for _, node := range nodes {
		errorTargets := map[string]bool{}
		for _, logic := range node.logics {
			if target, _ := logic["err_node_id"].(string); target != "" {
				errorTargets[target] = true
			}
		}
		for _, logic := range node.logics {
			if t, _ := logic["type"].(string); t != "api_rpc" {
				continue
			}
			if !isActiveStubNode(node) {
				if hasExplicitProcessScope(logic) {
					continue
				}
				if synchronous, specified := logic["is_sync"].(bool); specified && !synchronous {
					continue
				}
			}
			providesVariable := false
			if isActiveStubNode(node) {
				providesVariable = guaranteedStubSuccessOutputs(node)[variable]
			} else {
				target, static := staticProcessTarget(logic)
				if !static {
					continue
				}
				for _, output := range calleeOutputs[target] {
					if output.Name == variable {
						providesVariable = true
						break
					}
				}
			}
			if !providesVariable {
				continue
			}
			for _, branch := range node.logics {
				if branchType, _ := branch["type"].(string); branchType == "go" {
					if successTarget, _ := branch["to_node_id"].(string); successTarget != "" && !errorTargets[successTarget] {
						producerTargets[contractProducerEdge{From: node.id, To: successTarget}] = true
					}
				}
			}
		}
	}
	return producerTargets
}

func guaranteedStubSuccessOutputs(node processNode) map[string]bool {
	if !isActiveStubNode(node) {
		return nil
	}
	branches, _ := node.stub["logics"].([]interface{})
	var guaranteed map[string]bool
	for _, rawBranch := range branches {
		branch, _ := rawBranch.([]interface{})
		var reply map[string]interface{}
		for _, rawLogic := range branch {
			logic, _ := rawLogic.(map[string]interface{})
			if t, _ := logic["type"].(string); t == "api_rpc_reply" {
				reply = logic
			}
		}
		if reply == nil {
			continue
		}
		if throws, _ := reply["throw_exception"].(bool); throws {
			continue
		}
		outputs := map[string]bool{}
		if mode, _ := reply["mode"].(string); mode == "keys" {
			rawNames, _ := reply["res_data"].([]interface{})
			for _, rawName := range rawNames {
				if name, _ := rawName.(string); name != "" {
					outputs[name] = true
				}
			}
		} else if data, _ := reply["res_data"].(map[string]interface{}); data != nil {
			for name := range data {
				outputs[name] = true
			}
		}
		if guaranteed == nil {
			guaranteed = outputs
			continue
		}
		for name := range guaranteed {
			if !outputs[name] {
				delete(guaranteed, name)
			}
		}
	}
	if guaranteed == nil {
		return map[string]bool{}
	}
	return guaranteed
}

func producerEdgesCoverAllReads(g safetyGraph, producerEdges map[contractProducerEdge]bool, readers map[string]bool) bool {
	if len(producerEdges) == 0 || len(readers) == 0 {
		return false
	}
	for reader := range readers {
		seen := map[string]bool{}
		var queue []string
		for id, node := range g.byID {
			if node.objType == 1 && g.reachable[id] {
				seen[id] = true
				queue = append(queue, id)
			}
		}
		if len(queue) == 0 {
			return false
		}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if id == reader {
				return false
			}
			for _, next := range g.adj[id] {
				if !g.reachable[next] || producerEdges[contractProducerEdge{From: id, To: next}] || seen[next] {
					continue
				}
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return true
}

type localProcessContract struct {
	ID        int
	Title     string
	Path      string
	ConvType  string
	Inputs    []processParameter
	Outputs   []processParameter
	Issues    []ContractIssue
	Ambiguous bool
	Paths     []string
}

func loadLocalProcessContracts(root string, targets map[int]bool) map[int]localProcessContract {
	out := map[int]localProcessContract{}
	for _, entry := range policyProcessEntries(root) {
		if entry.ObjID == 0 || !targets[entry.ObjID] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
		if err != nil {
			continue
		}
		var proc map[string]interface{}
		if json.Unmarshal(data, &proc) != nil {
			continue
		}
		params, _ := parseDeclaredParameters(proc)
		convType, _ := proc["conv_type"].(string)
		var inputs, outputs []processParameter
		for _, p := range params {
			if p.Input {
				inputs = append(inputs, p)
			}
			if p.Output {
				outputs = append(outputs, p)
			}
		}
		var issues []ContractIssue
		if rawNodes, nodeErr := getNodes(proc); nodeErr == nil {
			selfPolicy := contractPolicyForLocalIndex(root)
			if report := analyzeProcessContract(proc, parseProcessNodes(rawNodes), selfPolicy); report != nil {
				issues = report.Issues
			}
		} else {
			issues = append(issues, ContractIssue{Code: "CALLEE_SCHEME_INVALID", Issue: nodeErr.Error()})
		}
		contract := localProcessContract{ID: entry.ObjID, Title: entry.Title, Path: entry.Path, ConvType: convType, Inputs: inputs, Outputs: outputs, Issues: issues, Paths: []string{entry.Path}}
		if existing, duplicate := out[entry.ObjID]; duplicate {
			existing.Ambiguous = true
			existing.Paths = append(existing.Paths, entry.Path)
			out[entry.ObjID] = existing
			continue
		}
		out[entry.ObjID] = contract
	}
	return out
}

func contractStaticProcessTargets(nodes []processNode) map[int]bool {
	targets := map[int]bool{}
	for _, node := range nodes {
		for _, logic := range node.logics {
			if hasExplicitProcessScope(logic) {
				continue
			}
			if target, ok := staticProcessTarget(logic); ok {
				targets[target] = true
			}
		}
	}
	return targets
}

func contractPolicyForLocalIndex(root string) EffectiveProjectPolicy {
	p := defaultProjectPolicy()
	p.ProcessContracts.Mode = policyModeStrict
	p.ProcessContracts.DependencyScope = "self"
	return EffectiveProjectPolicy{ProjectPolicy: p, Root: root, Configured: true}
}

type successReplyContract struct {
	NodeID    string
	NodeTitle string
	Types     map[string]string
	Issues    []ContractIssue
}

func successReplyTypes(nodes []processNode) []successReplyContract {
	var replies []successReplyContract
	for _, n := range nodes {
		for _, lg := range n.logics {
			if t, _ := lg["type"].(string); t != "api_rpc_reply" {
				continue
			}
			if throws, _ := lg["throw_exception"].(bool); throws {
				continue
			}
			title := n.title
			if title == "" {
				title = "(untitled)"
			}
			types := map[string]string{}
			var shapeIssues []ContractIssue
			if mode, _ := lg["mode"].(string); mode == "keys" {
				rawNames, _ := lg["res_data"].([]interface{})
				rawTypes, _ := lg["res_data_type"].([]interface{})
				if len(rawNames) != len(rawTypes) {
					shapeIssues = append(shapeIssues, ContractIssue{
						Code: "OUTPUT_KEYS_LENGTH_MISMATCH", NodeID: n.id, NodeTitle: title,
						Issue: fmt.Sprintf("keys-mode success Reply has %d output name(s) but %d type(s)", len(rawNames), len(rawTypes)),
					})
				}
				for index, rawName := range rawNames {
					name, ok := rawName.(string)
					if !ok || name == "" {
						continue
					}
					if _, duplicate := types[name]; duplicate {
						shapeIssues = append(shapeIssues, ContractIssue{
							Code: "OUTPUT_KEYS_DUPLICATE", Parameter: name, NodeID: n.id, NodeTitle: title,
							Issue: fmt.Sprintf("keys-mode success Reply returns output %q more than once", name),
						})
					}
					typ := ""
					if index < len(rawTypes) {
						typ, _ = rawTypes[index].(string)
					}
					types[name] = typ
				}
			} else {
				rawData, _ := lg["res_data"].(map[string]interface{})
				rawTypes, _ := lg["res_data_type"].(map[string]interface{})
				if len(rawTypes) == 0 {
					rawData, _ = lg["extra"].(map[string]interface{})
					rawTypes, _ = lg["extra_type"].(map[string]interface{})
				}
				for key := range rawData {
					raw := rawTypes[key]
					if typ, ok := raw.(string); ok {
						types[key] = typ
					} else {
						types[key] = ""
					}
				}
			}
			replies = append(replies, successReplyContract{NodeID: n.id, NodeTitle: title, Types: types, Issues: shapeIssues})
		}
	}
	return replies
}

func validateOutputContract(outputs map[string]processParameter, nodes []processNode) []ContractIssue {
	replies := successReplyTypes(nodes)
	var issues []ContractIssue
	returnedAnywhere := map[string]bool{}
	for index, reply := range replies {
		issues = append(issues, reply.Issues...)
		for name, typ := range reply.Types {
			returnedAnywhere[name] = true
			declared, ok := outputs[name]
			if !ok {
				issues = append(issues, ContractIssue{Code: "OUTPUT_UNDECLARED", Parameter: name, NodeID: reply.NodeID, NodeTitle: reply.NodeTitle, SuggestedType: typ, Issue: fmt.Sprintf("success Reply %d returns %q (%s), but params does not declare it as output", index+1, name, typ)})
				continue
			}
			if declared.Type != typ {
				issues = append(issues, ContractIssue{Code: "OUTPUT_TYPE_MISMATCH", Parameter: name, NodeID: reply.NodeID, NodeTitle: reply.NodeTitle, SuggestedType: typ, Issue: fmt.Sprintf("output %q is declared as %s but success Reply %d returns %s", name, declared.Type, index+1, typ)})
			}
		}
		for name, declared := range outputs {
			if declared.Required {
				if _, ok := reply.Types[name]; !ok {
					issues = append(issues, ContractIssue{Code: "OUTPUT_REQUIRED_MISSING", Parameter: name, NodeID: reply.NodeID, NodeTitle: reply.NodeTitle, Issue: fmt.Sprintf("required output %q is absent from success Reply %d", name, index+1)})
				}
			}
		}
	}
	for name := range outputs {
		if !returnedAnywhere[name] {
			issues = append(issues, ContractIssue{Code: "OUTPUT_STALE", Parameter: name, Issue: fmt.Sprintf("output %q is declared in params but no success Reply returns it", name)})
		}
	}
	return issues
}

func validateCallContracts(nodes []processNode, local map[int]localProcessContract) []ContractIssue {
	var issues []ContractIssue
	for _, n := range nodes {
		for _, lg := range n.logics {
			t, _ := lg["type"].(string)
			if !isProcessInvocation(lg) {
				continue
			}
			title := n.title
			if title == "" {
				title = "(untitled)"
			}
			convString, isString := lg["conv_id"].(string)
			if isString && strings.Contains(convString, "{{") {
				issues = append(issues, ContractIssue{Code: "DYNAMIC_CONTRACT_UNVERIFIED", NodeID: n.id, NodeTitle: title, Advisory: true, Issue: "dynamic conv_id remains supported, but the target input/output contract cannot be checked statically"})
				continue
			}
			if isString && strings.HasPrefix(convString, "@") {
				issues = append(issues, ContractIssue{Code: "ALIAS_CONTRACT_UNVERIFIED", NodeID: n.id, NodeTitle: title, Advisory: true, Issue: fmt.Sprintf("alias target %s is not resolved by the local contract index", convString)})
				continue
			}
			if hasExplicitProcessScope(lg) {
				issues = append(issues, ContractIssue{Code: "CROSS_SCOPE_CONTRACT_UNVERIFIED", NodeID: n.id, NodeTitle: title, Advisory: true, Issue: "explicit project_id or stage_id points outside the locally indexed stage; the target contract cannot be matched safely by conv_id alone"})
				continue
			}
			target, staticTarget := staticProcessTarget(lg)
			if !staticTarget {
				issues = append(issues, ContractIssue{Code: "CALLEE_CONTRACT_UNAVAILABLE", NodeID: n.id, NodeTitle: title, Advisory: true, Issue: fmt.Sprintf("target process %v is not a positive integer ID; its contract was not checked", lg["conv_id"])})
				continue
			}
			callee, ok := local[target]
			if !ok {
				issues = append(issues, ContractIssue{Code: "CALLEE_CONTRACT_UNAVAILABLE", NodeID: n.id, NodeTitle: title, Advisory: true, Issue: fmt.Sprintf("target process %v is not available in the local project export; its contract was not checked", lg["conv_id"])})
				continue
			}
			if callee.Ambiguous {
				issues = append(issues, ContractIssue{
					Code: "CALLEE_CONTRACT_AMBIGUOUS", NodeID: n.id, NodeTitle: title,
					Issue: fmt.Sprintf("target process %d appears in multiple local files (%s); its contract cannot be selected safely", target, strings.Join(callee.Paths, ", ")),
				})
				continue
			}
			if callee.ConvType == "state" {
				issues = append(issues, ContractIssue{
					Code: "STATE_TARGET_CONTRACT_NOT_APPLICABLE", NodeID: n.id, NodeTitle: title, Advisory: true,
					Issue: fmt.Sprintf("target %s is a state diagram; its state-task fields are not a callable process input/output contract", callee.Title),
				})
				continue
			}
			if invalid := blockingContractIssues(callee.Issues); invalid > 0 {
				issues = append(issues, ContractIssue{
					Code: "CALLEE_CONTRACT_INVALID", NodeID: n.id, NodeTitle: title,
					Issue: fmt.Sprintf("target process %s has %d invalid contract declaration(s) in %s", callee.Title, invalid, callee.Path),
				})
				continue
			}
			group, _ := lg["group"].(string)
			sendsImplicitData := t == "api_rpc" && group == "all"
			if t == "api_copy" {
				sendsImplicitData, _ = lg["send_parent_data"].(bool)
			}
			if sendsImplicitData {
				issues = append(issues, ContractIssue{Code: "SEND_ALL_CONTRACT_UNVERIFIED", NodeID: n.id, NodeTitle: title, Advisory: true, Issue: fmt.Sprintf("Send all parameters is enabled for %s; required inputs may arrive implicitly, so exact mapping cannot be proven", callee.Title)})
			}
			values, _ := lg["extra"].(map[string]interface{})
			valueTypes, _ := lg["extra_type"].(map[string]interface{})
			typeField := "extra_type"
			if t == "api_copy" {
				values, _ = lg["data"].(map[string]interface{})
				valueTypes, _ = lg["data_type"].(map[string]interface{})
				typeField = "data_type"
			}
			declared := map[string]processParameter{}
			for _, p := range callee.Inputs {
				declared[p.Name] = p
				if p.Required && !sendsImplicitData {
					if _, sent := values[p.Name]; !sent {
						issues = append(issues, ContractIssue{Code: "CALLEE_REQUIRED_INPUT_MISSING", Parameter: p.Name, NodeID: n.id, NodeTitle: title, SuggestedType: p.Type, Issue: fmt.Sprintf("call to %s does not map required input %q", callee.Title, p.Name)})
					}
				}
			}
			for name := range values {
				p, declaredInput := declared[name]
				if !declaredInput {
					issues = append(issues, ContractIssue{Code: "CALLEE_INPUT_UNDECLARED", Parameter: name, NodeID: n.id, NodeTitle: title, Issue: fmt.Sprintf("call sends %q, but %s does not declare that input", name, callee.Title)})
					continue
				}
				typ, _ := valueTypes[name].(string)
				if typ == "" {
					issues = append(issues, ContractIssue{Code: "CALLEE_INPUT_TYPE_MISSING", Parameter: name, NodeID: n.id, NodeTitle: title, SuggestedType: p.Type, Issue: fmt.Sprintf("call maps %q to %s without a %s entry", name, callee.Title, typeField)})
				} else if typ != p.Type {
					issues = append(issues, ContractIssue{Code: "CALLEE_INPUT_TYPE_MISMATCH", Parameter: name, NodeID: n.id, NodeTitle: title, SuggestedType: p.Type, Issue: fmt.Sprintf("call maps %q as %s, but %s declares %s", name, typ, callee.Title, p.Type)})
				}
			}
			for name := range valueTypes {
				if _, sent := values[name]; !sent {
					issues = append(issues, ContractIssue{Code: "CALLEE_INPUT_VALUE_MISSING", Parameter: name, NodeID: n.id, NodeTitle: title, Issue: fmt.Sprintf("call has a %s entry for %q but does not send that input value", typeField, name)})
				}
			}
		}
	}
	return issues
}

func analyzeProcessContract(proc map[string]interface{}, nodes []processNode, policy EffectiveProjectPolicy) *ContractReport {
	if policy.ProcessContracts.Mode == policyModeOff {
		return nil
	}
	report := &ContractReport{Mode: policy.ProcessContracts.Mode, DependencyScope: policy.ProcessContracts.DependencyScope}
	if convType, _ := proc["conv_type"].(string); convType == "state" {
		report.SkippedReason = "state diagrams store state-task fields and do not expose a callable process input/output contract"
		return report
	}
	nodes = reachableProcessNodes(nodes)
	params, shapeIssues := parseDeclaredParameters(proc)
	report.Issues = append(report.Issues, shapeIssues...)
	inputs, outputs := contractParamMaps(params)
	for name := range inputs {
		report.Inputs = append(report.Inputs, name)
	}
	for name := range outputs {
		report.Outputs = append(report.Outputs, name)
	}
	sort.Strings(report.Inputs)
	sort.Strings(report.Outputs)

	local := map[int]localProcessContract{}
	calleeOutputs := map[int][]processParameter{}
	if policy.ProcessContracts.DependencyScope == "project" {
		local = loadLocalProcessContracts(policy.Root, contractStaticProcessTargets(nodes))
		for id, contract := range local {
			if blockingContractIssues(contract.Issues) == 0 && !contract.Ambiguous && contract.ConvType != "state" {
				calleeOutputs[id] = contract.Outputs
			}
		}
	}
	producerTargets, refs, hints, refNodes := producedAndReferencedVariables(nodes)
	graph := buildSafetyGraph(nodes)
	produced := map[string]bool{}
	for variable := range refs {
		combinedEdges := map[contractProducerEdge]bool{}
		for edge := range producerTargets[variable] {
			combinedEdges[edge] = true
		}
		for edge := range calleeOutputSuccessTargets(nodes, calleeOutputs, variable) {
			combinedEdges[edge] = true
		}
		if producerEdgesCoverAllReads(graph, combinedEdges, refNodes[variable]) {
			produced[variable] = true
		}
	}
	for variable := range refs {
		if produced[variable] {
			continue
		}
		if conflicting := sortedTypeHints(hints, variable); len(conflicting) > 1 {
			report.Issues = append(report.Issues, ContractIssue{
				Code: "INPUT_TYPE_CONFLICT", Parameter: variable,
				Issue: fmt.Sprintf("input %q is used with conflicting type hints: %s", variable, strings.Join(conflicting, ", ")),
			})
		}
		if declared, ok := inputs[variable]; ok {
			if hint := singleTypeHint(hints, variable); hint != "" && declared.Type != hint {
				report.Issues = append(report.Issues, ContractIssue{Code: "INPUT_TYPE_MISMATCH", Parameter: variable, SuggestedType: hint, Issue: fmt.Sprintf("input %q is declared as %s but process usage implies %s", variable, declared.Type, hint)})
			}
			continue
		}
		report.Issues = append(report.Issues, ContractIssue{
			Code: "INPUT_UNDECLARED", Parameter: variable, SuggestedType: singleTypeHint(hints, variable),
			Issue: fmt.Sprintf("process reads %q without producing it locally, but params does not declare it as input", variable),
		})
	}
	report.Issues = append(report.Issues, validateOutputContract(outputs, nodes)...)
	report.Issues = append(report.Issues, codeContractIssues(nodes)...)
	if policy.ProcessContracts.DependencyScope == "project" {
		report.Issues = append(report.Issues, validateCallContracts(nodes, local)...)
	}
	sort.SliceStable(report.Issues, func(i, j int) bool {
		if report.Issues[i].Advisory != report.Issues[j].Advisory {
			return !report.Issues[i].Advisory
		}
		if report.Issues[i].Code != report.Issues[j].Code {
			return report.Issues[i].Code < report.Issues[j].Code
		}
		return report.Issues[i].Parameter < report.Issues[j].Parameter
	})
	return report
}

func blockingContractIssueCount(report *ContractReport) int {
	if report == nil {
		return 0
	}
	return blockingContractIssues(report.Issues)
}

func blockingContractIssues(issues []ContractIssue) int {
	count := 0
	for _, issue := range issues {
		if !issue.Advisory {
			count++
		}
	}
	return count
}
