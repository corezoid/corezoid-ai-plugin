package main

// Synthetic Corezoid process topologies for stress-testing the auto-layout —
// a port of the skill's former gen_test_process.py. Builders are methods on
// fixGen so every fixture gets a fresh deterministic id sequence.

import "fmt"

type fixGen struct {
	seq int
	err string // default err_node_id wired into code() nodes ("" = none)
}

func (g *fixGen) nid() string {
	g.seq++
	s := fmt.Sprintf("bbbb%04d513aa075cf700000", g.seq)
	return s[:24]
}

func (g *fixGen) code(title, to string, err ...string) map[string]interface{} {
	lg := map[string]interface{}{"type": "api_code", "lang": "js", "src": fmt.Sprintf("data.step='%s';", title)}
	e := g.err
	if len(err) > 0 && err[0] != "" {
		e = err[0]
	}
	if e != "" {
		lg["err_node_id"] = e
	}
	return map[string]interface{}{
		"id": g.nid(), "obj_type": 0, "title": title, "description": "",
		"condition": map[string]interface{}{
			"logics":    []interface{}{lg, map[string]interface{}{"type": "go", "to_node_id": to}},
			"semaphors": []interface{}{},
		},
		"x": 0, "y": 0, "extra": `{"modeForm":"expand","icon":""}`, "options": nil,
	}
}

type fixBranch struct{ param, cnst, to string }

func (g *fixGen) cond(title string, branches []fixBranch, defaultTo string) map[string]interface{} {
	lg := []interface{}{}
	for _, b := range branches {
		lg = append(lg, map[string]interface{}{
			"type": "go_if_const", "to_node_id": b.to,
			"conditions": []interface{}{map[string]interface{}{
				"param": b.param, "const": b.cnst, "fun": "eq", "cast": "string"}},
		})
	}
	lg = append(lg, map[string]interface{}{"type": "go", "to_node_id": defaultTo})
	return map[string]interface{}{
		"id": g.nid(), "obj_type": 0, "title": title, "description": "",
		"condition": map[string]interface{}{"logics": lg, "semaphors": []interface{}{}},
		"x":         0, "y": 0, "extra": `{"modeForm":"expand","icon":""}`, "options": nil,
	}
}

func (g *fixGen) delay(title, to string) map[string]interface{} {
	return map[string]interface{}{
		"id": g.nid(), "obj_type": 0, "title": title, "description": "",
		"condition": map[string]interface{}{
			"logics":    []interface{}{map[string]interface{}{"type": "go", "to_node_id": to}},
			"semaphors": []interface{}{map[string]interface{}{"type": "time", "value": 30, "dimension": "sec", "to_node_id": to}},
		},
		"x": 0, "y": 0, "extra": `{"modeForm":"expand","icon":""}`, "options": nil,
	}
}

func (g *fixGen) final(title string, ok bool) map[string]interface{} {
	icon := "success"
	var options interface{}
	if ok {
		options = `{"save_task":true}`
	} else {
		icon = "error"
	}
	return map[string]interface{}{
		"id": g.nid(), "obj_type": 2, "title": title, "description": "",
		"condition": map[string]interface{}{"logics": []interface{}{}, "semaphors": []interface{}{}},
		"x":         0, "y": 0, "extra": fmt.Sprintf(`{"modeForm":"collapse","icon":"%s"}`, icon), "options": options,
	}
}

func (g *fixGen) start(to string) map[string]interface{} {
	return map[string]interface{}{
		"id": g.nid(), "obj_type": 1, "title": "Start", "description": "",
		"condition": map[string]interface{}{
			"logics":    []interface{}{map[string]interface{}{"type": "go", "to_node_id": to}},
			"semaphors": []interface{}{},
		},
		"x": 0, "y": 0, "extra": `{"icon":"","modeForm":"collapse"}`,
		"options": `{"direct_url":true,"type_auth":"no_auth"}`,
	}
}

// setGo rewires the trailing "go" logic of a node (the PLACEHOLDER pattern).
func setGo(n map[string]interface{}, to string) {
	lgs := nodeLogics(n)
	lgs[len(lgs)-1]["to_node_id"] = to
}

// chainOf builds a chain of k code nodes leading into to. Returns (nodes, headID).
func (g *fixGen) chainOf(k int, prefix, to string) ([]map[string]interface{}, string) {
	var nodes []map[string]interface{}
	nxt := to
	for i := k; i >= 1; i-- {
		n := g.code(fmt.Sprintf("%s-%d", prefix, i), nxt)
		nodes = append(nodes, n)
		nxt = nodeStr(n, "id")
	}
	for i, j := 0, len(nodes)-1; i < j; i, j = i+1, j-1 {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	}
	return nodes, nodeStr(nodes[0], "id")
}

func fixDoc(nodes []map[string]interface{}) map[string]interface{} {
	arr := make([]interface{}, len(nodes))
	for i, n := range nodes {
		arr[i] = n
	}
	return map[string]interface{}{"scheme": map[string]interface{}{"nodes": arr}}
}

func topoChain() []map[string]interface{} {
	g := &fixGen{}
	fe := g.final("error", false)
	g.err = nodeStr(fe, "id")
	fin := g.final("Final", true)
	body, head := g.chainOf(17, "step", nodeStr(fin, "id"))
	st := g.start(head)
	out := append([]map[string]interface{}{st}, body...)
	return append(out, fin, fe)
}

func topoStar() []map[string]interface{} {
	g := &fixGen{}
	fe := g.final("error", false)
	g.err = nodeStr(fe, "id")
	fin := g.final("Final", true)
	join := g.code("join", nodeStr(fin, "id"))
	var heads []fixBranch
	var nodes []map[string]interface{}
	for i, ln := range []int{2, 4, 3, 5, 2, 6, 3} {
		br, head := g.chainOf(ln, fmt.Sprintf("ray%d", i+1), nodeStr(join, "id"))
		nodes = append(nodes, br...)
		heads = append(heads, fixBranch{"kind", fmt.Sprintf("r%d", i+1), head})
	}
	hub := g.cond("dispatch: kind?", heads[:len(heads)-1], heads[len(heads)-1].to)
	st := g.start(nodeStr(hub, "id"))
	out := append([]map[string]interface{}{st, hub}, nodes...)
	return append(out, join, fin, fe)
}

func topoLoops() []map[string]interface{} {
	g := &fixGen{}
	finErr := g.final("error: retries exhausted", false)
	g.err = nodeStr(finErr, "id")
	fin := g.final("Final", true)
	tail := g.code("big-loop-exit", nodeStr(fin, "id"))
	big, bigHead := g.chainOf(8, "big", "PLACEHOLDER")
	backBig := g.cond("big done?", []fixBranch{{"done", "yes", nodeStr(tail, "id")}}, bigHead)
	setGo(big[len(big)-1], nodeStr(backBig, "id"))
	call := g.code("flaky-call", "PLACEHOLDER")
	wait := g.delay("retry-pause 30s", nodeStr(call, "id"))
	chk := g.cond("call ok?", []fixBranch{
		{"ok", "no", nodeStr(wait, "id")},
		{"fatal", "yes", nodeStr(finErr, "id")},
	}, bigHead)
	setGo(call, nodeStr(chk, "id"))
	pre, preHead := g.chainOf(6, "prep", nodeStr(call, "id"))
	post, postHead := g.chainOf(5, "post", nodeStr(fin, "id"))
	setGo(tail, postHead)
	st := g.start(preHead)
	out := append([]map[string]interface{}{st}, pre...)
	out = append(out, call, chk, wait)
	out = append(out, big...)
	out = append(out, backBig, tail)
	out = append(out, post...)
	return append(out, fin, finErr)
}

func topoFractal(depth, arm int) []map[string]interface{} {
	g := &fixGen{}
	fe := g.final("error", false)
	g.err = nodeStr(fe, "id")
	fin := g.final("Final", true)
	join := g.code("join-all", nodeStr(fin, "id"))
	var nodes []map[string]interface{}
	var grow func(d int, name string) string
	grow = func(d int, name string) string {
		if d == 0 {
			leaf, head := g.chainOf(arm, "leaf"+name, nodeStr(join, "id"))
			nodes = append(nodes, leaf...)
			return head
		}
		left := grow(d-1, name+"L")
		right := grow(d-1, name+"R")
		pre, preHead := g.chainOf(arm, "seg"+name, "PLACEHOLDER")
		c := g.cond(fmt.Sprintf("split%s?", name), []fixBranch{{"side", "L", left}}, right)
		setGo(pre[len(pre)-1], nodeStr(c, "id"))
		nodes = append(nodes, pre...)
		nodes = append(nodes, c)
		return preHead
	}
	head := grow(depth, "")
	st := g.start(head)
	out := append([]map[string]interface{}{st}, nodes...)
	return append(out, join, fin, fe)
}

func topoMix(scale int) []map[string]interface{} {
	g := &fixGen{}
	finErr := g.final("error", false)
	fin := g.final("Final", true)
	errReply := map[string]interface{}{
		"id": g.nid(), "obj_type": 3, "title": "Reply Error", "description": "",
		"condition": map[string]interface{}{
			"logics": []interface{}{
				map[string]interface{}{"type": "api_rpc_reply", "mode": "key_value",
					"res_data": map[string]interface{}{"result": "error"}, "res_data_type": map[string]interface{}{"result": "string"},
					"throw_exception": true},
				map[string]interface{}{"type": "go", "to_node_id": nodeStr(finErr, "id")},
			},
			"semaphors": []interface{}{},
		},
		"x": 0, "y": 0, "extra": `{"modeForm":"expand","icon":""}`, "options": nil,
	}
	g.err = nodeStr(errReply, "id")
	nodes := []map[string]interface{}{errReply, finErr}

	tail, tailHead := g.chainOf(15*scale, "final-run", nodeStr(fin, "id"))
	nodes = append(nodes, tail...)

	join2 := g.code("join-fract", tailHead)
	nodes = append(nodes, join2)

	var grow func(d int, name string) string
	grow = func(d int, name string) string {
		if d == 0 {
			leaf, head := g.chainOf(3, "lf"+name, nodeStr(join2, "id"))
			nodes = append(nodes, leaf...)
			return head
		}
		left := grow(d-1, name+"L")
		right := grow(d-1, name+"R")
		pre, preHead := g.chainOf(2, "sg"+name, "PLACEHOLDER")
		c := g.cond(fmt.Sprintf("sp%s?", name), []fixBranch{{"s", "L", left}}, right)
		setGo(pre[len(pre)-1], nodeStr(c, "id"))
		nodes = append(nodes, pre...)
		nodes = append(nodes, c)
		return preHead
	}
	fractHead := grow(2+scale, "")

	call := g.code("mix-call", "PLACEHOLDER", nodeStr(errReply, "id"))
	wait := g.delay("mix-retry 30s", nodeStr(call, "id"))
	chk := g.cond("mix ok?", []fixBranch{{"ok", "no", nodeStr(wait, "id")}}, fractHead)
	setGo(call, nodeStr(chk, "id"))
	nodes = append(nodes, call, chk, wait)

	var heads []fixBranch
	rayLens := []int{3, 5, 4, 6, 3, 4}
	all := []int{}
	for s := 0; s < scale; s++ {
		all = append(all, rayLens...)
	}
	for i, ln := range all {
		br, head := g.chainOf(ln+scale, fmt.Sprintf("mray%d", i+1), nodeStr(call, "id"))
		nodes = append(nodes, br...)
		heads = append(heads, fixBranch{"kind", fmt.Sprintf("m%d", i+1), head})
	}
	hub := g.cond("mix dispatch?", heads[:len(heads)-1], heads[len(heads)-1].to)
	nodes = append(nodes, hub)

	pre, preHead := g.chainOf(12*scale, "intake", nodeStr(hub, "id"))
	for i := 0; i < len(pre); i += 3 {
		nodeLogics(pre[i])[0]["err_node_id"] = nodeStr(errReply, "id")
	}
	nodes = append(nodes, pre...)

	back := g.cond("repeat all?", []fixBranch{{"flag", "repeat", nodeStr(hub, "id")}}, nodeStr(join2, "id"))
	// a return from the middle of the tail into the star
	tail[7]["condition"] = map[string]interface{}{
		"logics": []interface{}{
			map[string]interface{}{"type": "go_if_const", "to_node_id": nodeStr(hub, "id"),
				"conditions": []interface{}{map[string]interface{}{"param": "again", "const": "yes", "fun": "eq", "cast": "string"}}},
			map[string]interface{}{"type": "go", "to_node_id": nodeStr(tail[8], "id")},
		},
		"semaphors": []interface{}{},
	}
	nodes = append(nodes, back) // also test an isolated branch (an orphan with a cycle)
	st := g.start(preHead)
	out := append([]map[string]interface{}{st}, nodes...)
	return append(out, fin)
}

func topoErrheavy() []map[string]interface{} {
	g := &fixGen{}
	fin := g.final("Done", true)
	var N []map[string]interface{}
	chain, head := g.chainOf(12, "biz", nodeStr(fin, "id"))
	N = append(N, chain...)
	for i, node := range chain {
		if i%2 == 1 {
			continue
		}
		ef := g.final(fmt.Sprintf("err-%d", i), false)
		esc := map[string]interface{}{
			"id": g.nid(), "obj_type": 3, "title": fmt.Sprintf("reply-err-%d", i), "description": "",
			"condition": map[string]interface{}{
				"logics": []interface{}{
					map[string]interface{}{"type": "api_rpc_reply", "mode": "key_value",
						"res_data": map[string]interface{}{"result": "error"}, "res_data_type": map[string]interface{}{"result": "string"},
						"throw_exception": true},
					map[string]interface{}{"type": "go", "to_node_id": nodeStr(ef, "id")},
				},
				"semaphors": []interface{}{},
			},
			"x": 0, "y": 0, "extra": `{"modeForm":"expand","icon":""}`, "options": nil,
		}
		for _, lg := range nodeLogics(node) {
			if nodeStr(lg, "type") == "api_code" {
				lg["err_node_id"] = nodeStr(esc, "id")
			}
		}
		N = append(N, esc, ef)
	}
	st := g.start(head)
	out := append([]map[string]interface{}{st}, N...)
	return append(out, fin)
}

func topoTable3() []map[string]interface{} {
	g := &fixGen{}
	fe := g.final("error", false)
	g.err = nodeStr(fe, "id")
	fin := g.final("Final", true)
	tail, tailHead := g.chainOf(2, "post", nodeStr(fin, "id"))
	join := g.code("join", tailHead)
	var heads []fixBranch
	var cols []map[string]interface{}
	for i := 0; i < 3; i++ {
		br, head := g.chainOf(6, fmt.Sprintf("pipe%d", i), nodeStr(join, "id"))
		cols = append(cols, br...)
		heads = append(heads, fixBranch{"entity", fmt.Sprintf("e%d", i), head})
	}
	hub := g.cond("dispatch: entity?", heads[:len(heads)-1], heads[len(heads)-1].to)
	pre, preHead := g.chainOf(2, "prep", nodeStr(hub, "id"))
	st := g.start(preHead)
	out := append([]map[string]interface{}{st}, pre...)
	out = append(out, hub)
	out = append(out, cols...)
	out = append(out, join)
	out = append(out, tail...)
	return append(out, fin, fe)
}

func topoTables2() []map[string]interface{} {
	g := &fixGen{}
	fe := g.final("error", false)
	g.err = nodeStr(fe, "id")
	fin := g.final("Final", true)
	join2 := g.code("join-2", nodeStr(fin, "id"))
	var cols2 []map[string]interface{}
	var heads2 []fixBranch
	for i := 0; i < 4; i++ {
		br, head := g.chainOf(3, fmt.Sprintf("t2c%d", i), nodeStr(join2, "id"))
		cols2 = append(cols2, br...)
		heads2 = append(heads2, fixBranch{"ch", fmt.Sprintf("c%d", i), head})
	}
	hub2 := g.cond("stage-2 route?", heads2[:len(heads2)-1], heads2[len(heads2)-1].to)
	mid := g.code("between tables", nodeStr(hub2, "id"))
	join1 := g.code("join-1", nodeStr(mid, "id"))
	var cols1 []map[string]interface{}
	var heads1 []fixBranch
	for i := 0; i < 3; i++ {
		br, head := g.chainOf(4, fmt.Sprintf("t1c%d", i), nodeStr(join1, "id"))
		cols1 = append(cols1, br...)
		heads1 = append(heads1, fixBranch{"seg", fmt.Sprintf("s%d", i), head})
	}
	hub1 := g.cond("stage-1 route?", heads1[:len(heads1)-1], heads1[len(heads1)-1].to)
	st := g.start(nodeStr(hub1, "id"))
	out := append([]map[string]interface{}{st, hub1}, cols1...)
	out = append(out, join1, mid, hub2)
	out = append(out, cols2...)
	return append(out, join2, fin, fe)
}

func topoCombo() []map[string]interface{} {
	g := &fixGen{}
	fe := g.final("error", false)
	g.err = nodeStr(fe, "id")
	fin := g.final("Final", true)
	join2 := g.code("join-table", nodeStr(fin, "id"))
	var colsT []map[string]interface{}
	var headsT []fixBranch
	for i := 0; i < 3; i++ {
		br, head := g.chainOf(4, fmt.Sprintf("tc%d", i), nodeStr(join2, "id"))
		colsT = append(colsT, br...)
		headsT = append(headsT, fixBranch{"ent", fmt.Sprintf("e%d", i), head})
	}
	hub2 := g.cond("table route?", headsT[:len(headsT)-1], headsT[len(headsT)-1].to)
	mid := g.code("between regions", nodeStr(hub2, "id"))
	join1 := g.code("join-star", nodeStr(mid, "id"))
	var rays []map[string]interface{}
	var headsS []fixBranch
	for i, d := range []int{1, 2, 3, 4, 5} {
		br, head := g.chainOf(d, fmt.Sprintf("ray%d", i), nodeStr(join1, "id"))
		rays = append(rays, br...)
		headsS = append(headsS, fixBranch{"ch", fmt.Sprintf("r%d", i), head})
	}
	hub1 := g.cond("star route?", headsS[:len(headsS)-1], headsS[len(headsS)-1].to)
	st := g.start(nodeStr(hub1, "id"))
	out := append([]map[string]interface{}{st, hub1}, rays...)
	out = append(out, join1, mid, hub2)
	out = append(out, colsT...)
	return append(out, join2, fin, fe)
}

func topoStar4() []map[string]interface{} {
	g := &fixGen{}
	fe := g.final("error", false)
	g.err = nodeStr(fe, "id")
	fin := g.final("Final", true)
	join := g.code("join-4", nodeStr(fin, "id"))
	var rays []map[string]interface{}
	var heads []fixBranch
	for i, d := range []int{1, 2, 3, 4} {
		br, head := g.chainOf(d, fmt.Sprintf("r4_%d", i), nodeStr(join, "id"))
		rays = append(rays, br...)
		heads = append(heads, fixBranch{"ch", fmt.Sprintf("c%d", i), head})
	}
	hub := g.cond("route-4?", heads[:len(heads)-1], heads[len(heads)-1].to)
	pre, preHead := g.chainOf(16, "pre", nodeStr(hub, "id")) // pad above the 25-node strategy threshold
	st := g.start(preHead)
	out := append([]map[string]interface{}{st}, pre...)
	out = append(out, hub)
	out = append(out, rays...)
	return append(out, join, fin, fe)
}

type namedFixture struct {
	name  string
	nodes []map[string]interface{}
}

func allFixtures() []namedFixture {
	return []namedFixture{
		{"chain", topoChain()},
		{"star", topoStar()},
		{"loops", topoLoops()},
		{"fractal", topoFractal(3, 2)},
		{"fractal100", topoFractal(4, 2)},
		{"mix", topoMix(1)},
		{"mega", topoMix(2)},
		{"errheavy", topoErrheavy()},
		{"table3", topoTable3()},
		{"tables2", topoTables2()},
		{"combo", topoCombo()},
		{"star4", topoStar4()},
	}
}
