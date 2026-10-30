import json, sys, collections, copy, secrets, re

ARGS = [a for a in sys.argv[1:] if not a.startswith('--')]
SPLIT_ERRORS = '--split-errors' in sys.argv[1:]
FIX_SETPARAM_ERRORS = '--fix-setparam-errors' in sys.argv[1:]
# --fix-final-icons: rule 27 -- set End icons by title (error-like word -> error, else success)
FIX_FINAL_ICONS = '--fix-final-icons' in sys.argv[1:]
# --vertical: no horizontal rows at all (rules 1/22/23 off) -- one vertical trunk, on user request
VERTICAL = '--vertical' in sys.argv[1:]
# Incremental mode (rule 19): place ONLY new nodes, keep every existing node where the user put it.
#   --only-new <before.conv.json>  new = node ids absent from that earlier copy (the pre-edit pull)
#   --nodes=id1,id2,...            new = exactly these ids (combinable with --only-new: union --
#                                  re-place existing nodes whose edges/role changed in this edit)
ONLY_NEW = None
_before, _ids = None, None
for _i, _a in enumerate(sys.argv[1:]):
    if _a == '--only-new':
        with open(sys.argv[_i + 2]) as _f:
            _before = {n['id'] for n in json.load(_f)['scheme']['nodes']}
        ARGS = [a for a in ARGS if a != sys.argv[_i + 2]]
    elif _a.startswith('--nodes='):
        _ids = set(filter(None, _a.split('=', 1)[1].split(',')))
if _before is not None or _ids is not None:
    ONLY_NEW = _before, _ids or set()
PATH = ARGS[0]

with open(PATH) as f:
    d = json.load(f)

nodes = d['scheme']['nodes']
by_id = {n['id']: n for n in nodes}


# ---- 0. (--split-errors) give every source node its own dead-end error cluster (rule 11) ----
# Changes the GRAPH, not just coordinates -- runs only with the flag, before layout.
CLUSTER_LOGICS = {'api_rpc_reply', 'go', 'go_if_const'}


def all_targets(n):
    """Every node id n can pass a task to: err_node_id, to_node_id, semaphor to_node_id."""
    c = n.get('condition', {})
    out = [lg[k] for lg in c.get('logics', []) for k in ('err_node_id', 'to_node_id') if lg.get(k)]
    out += [s['to_node_id'] for s in c.get('semaphors', []) if s.get('to_node_id')]
    return out


def final_icon(n):
    try:
        return json.loads(n.get('extra') or '{}').get('icon')
    except Exception:
        return None


def dead_end_error_cluster(t):
    """Closure of t if it is a dead-end error cluster (only replies/pure conditions,
    every exit ends in a non-success Final, nothing leads back into the flow), else None."""
    seen, stk = {t}, [t]
    while stk:
        n = by_id[stk.pop()]
        if n.get('obj_type') == 2:
            if final_icon(n) == 'success':
                return None
            continue
        c = n.get('condition', {})
        if c.get('semaphors') or not c.get('logics'):
            return None
        if any(lg.get('type') not in CLUSTER_LOGICS or lg.get('err_node_id') for lg in c['logics']):
            return None
        for v in all_targets(n):
            if v not in by_id:
                return None
            if v not in seen:
                seen.add(v)
                stk.append(v)
    return seen


def discovery_order():
    start = next(n['id'] for n in nodes if n.get('obj_type') == 1)
    order, seen, stk = [], {start}, [start]
    while stk:
        u = stk.pop()
        order.append(u)
        for v in reversed(all_targets(by_id[u])):
            if v in by_id and v not in seen:
                seen.add(v)
                stk.append(v)
    return order


def new_id():
    while True:
        i = secrets.token_hex(12)
        if i not in by_id:
            return i


def split_once():
    order = discovery_order()
    rank = {u: i for i, u in enumerate(order)}
    for t in order:
        cluster = dead_end_error_cluster(t)
        if cluster is None:
            continue
        sources = [u for u in order if u not in cluster and t in all_targets(by_id[u])]
        if len(sources) < 2:
            continue
        sources.sort(key=rank.get)
        # the original stays with the source its title already names ("Reply error —
        # build core fields" -> "Build core fields + extract refs"), else first-discovered
        named = [n for n in [t] + sorted(cluster - {t}) if '—' in (by_id[n].get('title') or '')]
        tail = by_id[named[0]]['title'].split('—', 1)[1].strip().lower() if named else ''
        owner = next((s for s in sources if tail and (tail in (by_id[s].get('title') or '').lower()
                      or (by_id[s].get('title') or '\0').lower() in tail)), sources[0])
        sources.remove(owner)
        sources.insert(0, owner)
        for src in sources[1:]:
            ids = {old: new_id() for old in cluster}
            for old in cluster:
                c = copy.deepcopy(by_id[old])
                c['id'] = ids[old]
                for lg in c.get('condition', {}).get('logics', []):
                    if lg.get('to_node_id') in ids:
                        lg['to_node_id'] = ids[lg['to_node_id']]
                    if lg.get('type') == 'api_rpc_reply':
                        c['title'] = 'Reply error — ' + (by_id[src].get('title') or src)
                nodes.append(c)
                by_id[c['id']] = c
            src_c = by_id[src].get('condition', {})
            for lg in src_c.get('logics', []):
                for k in ('err_node_id', 'to_node_id'):
                    if lg.get(k) == t:
                        lg[k] = ids[t]
            # semaphor edges count as sources in all_targets() -- retarget them too, or
            # the same source is found again on the next pass (infinite loop)
            for s in src_c.get('semaphors', []):
                if s.get('to_node_id') == t:
                    s['to_node_id'] = ids[t]
            print(f"split: '{by_id[t].get('title')}' ({len(cluster)} node(s)) -> own copy for "
                  f"'{by_id[src].get('title')}' (kept by '{by_id[sources[0]].get('title')}')")
        return True
    return False


if SPLIT_ERRORS:
    while split_once():
        pass


# ---- 0b. Set Parameters never own an error cluster (rule 21) ----
# The error exit of a Set Parameters goes where its `go` goes ("ignore and continue").
def default_go_of(n):
    return next((lg['to_node_id'] for lg in n.get('condition', {}).get('logics', [])
                 if lg.get('type') == 'go' and lg.get('to_node_id')), None)


def is_set_param_node(n):
    types = [lg.get('type') for lg in n.get('condition', {}).get('logics', [])]
    return 'set_param' in types and all(t in ('set_param', 'go') for t in types)


def string_only_set_param(n):
    # Rule 21 covers only Set Parameters whose keys are all strings: a string key never fails
    # (missing source -> ""), so an error path there is dead weight. With an object/array/number
    # key the error exit is a real check (e.g. a config object that is missing -> "Config is missing"),
    # so such a node may keep its own error cluster.
    return all(t == 'string' for lg in n['condition']['logics'] if lg.get('type') == 'set_param'
               for t in (lg.get('extra_type') or {}).values())


# ---- rule 27: every End carries an icon -- `error` when its title says so, else `success` ----
# Generated finals with icon "" render as plain success Ends on the canvas. Convention: a title
# with an error-like word -> icon "error"; never an empty icon. The word list is language-neutral:
# English plus a few common UI languages (de/fr/it/pt/es, and Russian/Ukrainian stems written as
# \u escapes so the source stays ASCII). Extend it if your process titles use another language.
_ERROR_END = re.compile(r'error|fail|fehler|erreur|errore|erro\b|\u043e\u0448\u0438\u0431\u043a'
                        r'|\u043f\u043e\u043c\u0438\u043b\u043a', re.I)
for _n in nodes:
    if _n.get('obj_type') != 2:
        continue
    try:
        _extra = json.loads(_n.get('extra') or '{}')
    except Exception:
        _extra = {}
    _icon = _extra.get('icon', '')
    _want = 'error' if _ERROR_END.search(_n.get('title') or '') else (_icon or 'success')
    if _icon == _want:
        continue
    if not FIX_FINAL_ICONS:
        print(f"WARNING (rule 27): End {_n.get('title')!r} has icon {_icon!r}, expected {_want!r} "
              f"(rerun with --fix-final-icons)")
        continue
    _extra['icon'] = _want
    _n['extra'] = json.dumps(_extra, ensure_ascii=False)
    print(f"fix: End {_n.get('title')!r} icon {_icon!r} -> {_want!r}")


def incoming_count(t):
    return sum(1 for n in nodes for v in all_targets(n) if v == t)


for _n in list(nodes):
    if not is_set_param_node(_n) or not string_only_set_param(_n):
        continue
    _go = default_go_of(_n)
    for _lg in _n['condition']['logics']:
        _t = _lg.get('err_node_id')
        if not _t or not _go or _t == _go or _t not in by_id:
            continue
        _cluster = dead_end_error_cluster(_t)
        _what = (f"has its own error cluster {by_id[_t].get('title')!r}" if _cluster is not None
                 else f"error exit jumps to {by_id[_t].get('title')!r} while `go` leads to {by_id[_go].get('title')!r}")
        if not FIX_SETPARAM_ERRORS:
            print(f"WARNING (rule 21): Set Parameters {_n.get('title')!r} {_what} -- its error exit must "
                  f"go where `go` goes (rerun with --fix-setparam-errors)")
            continue
        _lg['err_node_id'] = _go
        if by_id[_go].get('obj_type') == 0:
            by_id[_go]['obj_type'] = 3   # an escalation target must be obj_type 3 (else UI "convert to new format")
        print(f"fix: {_n.get('title')!r} error exit -> {by_id[_go].get('title')!r}")
        _changed = _cluster is not None
        while _changed:   # drop cluster nodes nobody points at any more
            _changed = False
            for _c in list(_cluster):
                if _c in by_id and incoming_count(_c) == 0:
                    print(f"  removed orphan {by_id[_c].get('title')!r}")
                    nodes.remove(by_id.pop(_c))
                    _cluster.discard(_c)
                    _changed = True


# ---- 0c. a fallback must rejoin where the success path rejoins (rule 21) ----
def reach(s):
    seen, stk = {s}, [s]
    while stk:
        for v in all_targets(by_id[stk.pop()]):
            if v in by_id and v not in seen:
                seen.add(v)
                stk.append(v)
    return seen


for _n in nodes:
    _go = default_go_of(_n)
    for _lg in _n.get('condition', {}).get('logics', []):
        _b = _lg.get('err_node_id')
        if _lg.get('type') not in ('api_rpc', 'api_code', 'api') or not _b or not _go or _b == _go:
            continue
        if _b not in by_id or _go not in by_id or dead_end_error_cluster(_b) is not None:
            continue
        # only a plain pair of alternatives (success step A and fallback B, one exit each) is checked;
        # a fallback that skips straight to A's successor, or A that forks, is a legit design choice
        _live = lambda n: {v for v in all_targets(by_id[n]) if v in by_id and dead_end_error_cluster(v) is None}
        _ta, _tb = _live(_go), _live(_b)
        if len(_ta) != 1 or len(_tb) != 1:
            continue
        _ga, _gb = next(iter(_ta)), next(iter(_tb))
        if _b != _ga and _gb not in (_ga, _go) and _gb in reach(_ga) - {_ga}:
            print(f"CHECK (rule 21): fallback {by_id[_b].get('title')!r} of {_n.get('title')!r} jumps to "
                  f"{by_id[_gb].get('title')!r}, but the success step {by_id[_go].get('title')!r} continues at "
                  f"{by_id[_ga].get('title')!r} -- the fallback skips it; intended?")

# ---- geometry constants (see references/tidy-tree-rules.md; measured on production router processes) ----
NODE_W_EXPAND = 200
NODE_W_COLLAPSE = 56
COL_W = NODE_W_EXPAND  # every DOWN column is one expanded node wide; collapsed nodes are centered in it
COL_INSET = (COL_W - NODE_W_COLLAPSE) // 2  # 72

BYPASS_ROW_DY = 80   # rule 23: a bypass on a horizontal row sits this far below the row top

GAP = 64             # the one gap: next.top - prev.bottom in a column (rule 10), and
                     # lane.left - neighbour.right between columns / side lanes
GAP_Y_PACK = 24      # vertical clearance used only when packing a lane next to other rects

# Error stub ("Reply to process" -> End: Error), rule 9 / 9b
STUB_GAP_X = 56          # corner hook: reply.x = main.x + main_width + 56, top-aligned
STUB_FINAL_DY = 88       # corner hook: final center = (reply.x + 28, reply.y + 88)
STUB_INLINE_GAP = 16     # inline (collapsed main): reply.x = main.x + 56 + 16
STUB_INLINE_FINAL_DX = 140  # inline: final center = (reply.x + 140, reply.y + 28)
STUB_STACK_DY = 92       # 2+ inline stubs off one collapsed check: stacked 92px apart
STUB_HOOK_STACK_DY = 116 # 2+ corner hooks off one expanded node: one hook band apart

BYPASS_DX = 144      # "… failed — skip" between two trunk rows: right edge flush with the column
PAIR_GAP = 16        # rule 20: success + fallback side by side on one row, same gap as rule 9b
RETURN_CHAIN_MAX = 3  # Push X -> init -> (back to loop head): laid out leftwards on the row

# Heights: plugin's measured formula (docs/process/node-positioning-best-practices.md)
H_BASE = 98
H_ERR_ROW = 27
H_COND_ROW = 27
H_TITLE_LINE = 16
TITLE_CHARS_PER_LINE = 24


def logics_of(nid):
    return by_id[nid].get('condition', {}).get('logics', [])


def is_circle(nid):
    return by_id[nid].get('obj_type') in (1, 2)


def out_edges(n):
    """[(kind, target)] in authored order, one entry per target. kind 'go' if any go /
    go_if_const / semaphor reaches the target, else 'err' (an err_node_id that points at
    the same node as the go -- "ignore the error and continue" -- counts as go)."""
    kinds, order = {}, []
    c = n.get('condition', {})
    for lg in c.get('logics', []):
        if lg.get('err_node_id'):
            t = lg['err_node_id']
            if t not in kinds:
                kinds[t] = 'err'
                order.append(t)
    for lg in c.get('logics', []):
        if lg.get('to_node_id'):
            t = lg['to_node_id']
            if t not in kinds:
                order.append(t)
            kinds[t] = 'go'
    for s in c.get('semaphors', []):
        t = s.get('to_node_id')
        if t:
            if t not in kinds:
                order.append(t)
            kinds[t] = 'go'
    return [(kinds[t], t) for t in order if t in by_id]


def edge_kind_of(u, v):
    for lg in logics_of(u):
        if lg.get('to_node_id') == v:
            return lg.get('type')
    return 'err'


def is_router_like(nid):
    n = by_id[nid]
    if n.get('obj_type') not in (0, 3):
        return False
    types = [lg.get('type') for lg in logics_of(nid)]
    return types.count('go') == 1 and len(types) >= 2 and all(t in ('go', 'go_if_const') for t in types)


def default_go(nid):
    return next((lg['to_node_id'] for lg in logics_of(nid) if lg.get('type') == 'go' and lg.get('to_node_id')), None)


start_id = next(n['id'] for n in nodes if n.get('obj_type') == 1)

# ---- 1. ownership tree: BFS from Start -- a node belongs to its SHALLOWEST predecessor ----
# (rule 12): the merge point of an optional branch (`Clear temp data`) belongs to
# the condition that skips the branch, not to the branch's last node -- so it stays on the trunk.
parent = {start_id: None}
depth = {start_id: 0}
children = collections.defaultdict(list)   # u -> [(kind, v)] tree edges
q = collections.deque([start_id])
while q:
    u = q.popleft()
    for kind, v in out_edges(by_id[u]):
        if v not in parent:
            parent[v] = u
            depth[v] = depth[u] + 1
            children[u].append((kind, v))
            q.append(v)


def single_target(nid):
    ts = {v for _, v in out_edges(by_id[nid])}
    return next(iter(ts)) if len(ts) == 1 else None


# Rule 20: success child A and error fallback B of one node both lead only into M -- BFS gives
# M to whichever of them comes first in authored order (usually B, the err edge). M belongs to the
# success path: re-own it to A, so A/B become a row of alternatives and M continues the column.
for u in list(parent):
    oe = out_edges(by_id[u])
    for ka, a in oe:
        if ka != 'go' or parent.get(a) != u:
            continue
        for kb, b in oe:
            m = single_target(b)
            # rule 24: also when A forks (`Normalize` -> next | own fallback), as long as M is A's `go`
            if kb == 'err' and b != a and parent.get(b) == u and m and m != a and parent.get(m) == b \
                    and m in (single_target(a), default_go(a)):
                children[b] = [e for e in children[b] if e[1] != m]
                children[a].append(('go', m))
                parent[m] = a
visited = set(parent)
unreached = set(by_id) - visited
print('unreached nodes:', len(unreached), list(unreached)[:10])
extra_edges = [(u, k, v) for u in visited for k, v in out_edges(by_id[u]) if parent.get(v) != u]


def ancestors(u):
    out = []
    while u is not None:
        out.append(u)
        u = parent[u]
    return out


subtree_size = {}
for u in sorted(visited, key=lambda n: -depth[n]):
    subtree_size[u] = 1 + sum(subtree_size[v] for _, v in children[u])

# ---- 2. roles ----
def is_error_stub(r):
    if not any(lg.get('type') == 'api_rpc_reply' for lg in logics_of(r)):
        return False
    oe = out_edges(by_id[r])
    if len(oe) != 1 or oe[0][0] != 'go' or parent.get(oe[0][1]) != r:
        return False
    f = by_id[oe[0][1]]
    return f.get('obj_type') == 2 and final_icon(f) != 'success'


def is_success_reply(r):
    gos = [lg['to_node_id'] for lg in logics_of(r) if lg.get('to_node_id')]
    if not any(lg.get('type') == 'api_rpc_reply' for lg in logics_of(r)) or len(gos) != 1:
        return False
    f = by_id.get(gos[0], {})
    return f.get('obj_type') == 2 and final_icon(f) == 'success'


error_stubs = {r for r in visited if r != start_id and is_error_stub(r)}
success_replies = {r for r in visited if is_success_reply(r)}

spine = set()
for u in visited:
    if is_router_like(u):
        v = default_go(u)
        if v and is_router_like(v):
            spine |= {u, v}

loop_heads = {v for u, k, v in extra_edges if v in ancestors(u)}
incoming = collections.defaultdict(list)
for u in visited:
    for k, v in out_edges(by_id[u]):
        incoming[v].append(k)


def is_reply(nid):
    return any(lg.get('type') == 'api_rpc_reply' for lg in logics_of(nid))


def set_param_only(nid):
    types = [lg.get('type') for lg in logics_of(nid)]
    return bool(types) and 'set_param' in types and all(t in ('set_param', 'go') for t in types)


def is_pure_cond(nid):
    types = [lg.get('type') for lg in logics_of(nid)]
    return 'go_if_const' in types and all(t in ('go', 'go_if_const') for t in types)


def is_error_reply(nid):
    """A Reply whose exit is an error Final (not `success`) -- no tree-ownership requirement."""
    if not is_reply(nid):
        return False
    gos = [lg['to_node_id'] for lg in logics_of(nid) if lg.get('to_node_id') in by_id]
    return bool(gos) and all(by_id[t].get('obj_type') == 2 and final_icon(by_id[t]) != 'success' for t in gos)


def has_error_fork(nid):
    """Normal continuation + an error exit to a DIFFERENT node (err == go is not a fork)."""
    lg = logics_of(nid)
    go = default_go(nid)
    return any(l.get('err_node_id') and l['err_node_id'] != go for l in lg)


def role_collapsed(nid):
    """Rule 6: expand/collapse by ROLE, never by obj_type
    (obj_type 3 only means 'some err_node_id points here' -- set by the server)."""
    if is_circle(nid) or nid in error_stubs:
        return True
    if nid in success_replies:
        return False
    if set_param_only(nid):
        if (by_id[nid].get('title') or '').strip().lower() == 'init':
            return False  # a Set Parameters named `init` is never collapsed
        return not has_error_fork(nid)  # collapsed unless it forks into go + error
    if is_pure_cond(nid) and nid not in spine and any(
            is_error_reply(lg['to_node_id']) for lg in logics_of(nid) if lg.get('to_node_id') in by_id):
        return True  # result check leading into an error Reply (any number of them)
    types = [lg.get('type') for lg in logics_of(nid)]
    if types and all(t == 'go' for t in types) and incoming[nid] and all(k == 'err' for k in incoming[nid]):
        return True  # bare "… failed - continue" relay, only reached by errors
    return False


collapsed = {nid for nid in by_id if role_collapsed(nid)}

NEW = set()
if ONLY_NEW:
    NEW = ((set(by_id) - ONLY_NEW[0]) if ONLY_NEW[0] is not None else set()) | (ONLY_NEW[1] & set(by_id))
    # existing nodes keep their modeForm, so the tidy geometry below matches what is on the canvas
    for nid in set(by_id) - NEW:
        try:
            mf = json.loads(by_id[nid].get('extra') or '{}').get('modeForm')
        except Exception:
            mf = None
        if mf in ('expand', 'collapse') and not is_circle(nid):
            (collapsed.add if mf == 'collapse' else collapsed.discard)(nid)


def will_expand(nid):
    return nid not in collapsed


def W(nid):
    return NODE_W_EXPAND if will_expand(nid) else NODE_W_COLLAPSE


def H(nid):
    if not will_expand(nid):
        return NODE_W_COLLAPSE
    lg = logics_of(nid)
    h = H_BASE
    if any(l.get('err_node_id') for l in lg):
        h += H_ERR_ROW
    h += H_COND_ROW * sum(1 for l in lg if l.get('type') == 'go_if_const')
    title = by_id[nid].get('title') or ''
    h += H_TITLE_LINE * max(0, -(-len(title) // TITLE_CHARS_PER_LINE) - 1)
    return h


def colx(nid):
    return 0 if will_expand(nid) else COL_INSET


# ---- 3. primary / secondary children ----
primary = {}
secondary = {}
for u in visited:
    kids = children[u]
    if not kids:
        continue
    if len(kids) == 1:
        primary[u], secondary[u] = kids[0][1], []
        continue
    go_kids = [v for k, v in kids if k == 'go']
    if go_kids:
        best = max(go_kids, key=lambda v: (subtree_size[v], 1 if edge_kind_of(u, v) == 'go' else 0))
        primary[u] = best
    secondary[u] = [v for k, v in kids if v != primary.get(u)]

# Rule 22: on a dispatcher spine the default `go` to the next router is
# ALWAYS primary -- even when a matched branch is bigger (one branch holding most of the process).
for u in visited:
    v = default_go(u)
    if v and is_router_like(u) and is_router_like(v) and parent.get(v) == u:
        primary[u] = v
        secondary[u] = [w for _, w in children[u] if w != v]

def tree_desc(r):
    out, stk = [], [r]
    while stk:
        u = stk.pop()
        out.append(u)
        stk.extend(v for _, v in children[u])
    return out


# ---- 3b. horizontal rows: next node to the RIGHT of a node ----
check_rows = set()   # heads (corners) of rule-23 rows
row_next = {u: primary[u] for u in visited if u in primary and is_router_like(u) and is_router_like(primary[u])
            and edge_kind_of(u, primary[u]) == 'go'}


def is_check(nid):
    return is_pure_cond(nid) and will_expand(nid) and nid not in loop_heads


def is_gate(nid):
    """A business check whose other branches are exits: each ends inside its own subtree (reject /
    block + reply) or is a short fix-up of at most 2 real nodes that rejoins (`Link check result`,
    `Checks not completed`). A check that opens a bigger optional block (`Data already saved?`
    -> 4-node lane) is not a gate: rule 12 applies, the flow turns back DOWN before it."""
    if not is_check(nid):
        return False
    for s in secondary.get(nid, []):
        sub = set(tree_desc(s))
        closed = all(v in sub for u in sub for _, v in out_edges(by_id[u]))
        real = [u for u in sub if not is_circle(u) and u not in error_stubs]
        if not closed and len(real) > 2:
            return False
    return True


# Rule 23 (a block of business checks): a run of 3+ gates on the primary chain, at most one other node
# between two of them, turns the flow RIGHT at the first gate's predecessor (the corner) and back DOWN
# after the last gate (its primary child starts a column to the right, top-aligned -- the row's tail).
for c in sorted(visited, key=lambda n: depth[n]):
    f = primary.get(c)
    if f is None or c in row_next or c in row_next.values() or is_check(c) or not is_gate(f):
        continue
    if f in row_next and f not in row_next.values():
        continue   # f heads a dispatcher spine already (Start -> first router): not a check row
    run, u, gap, conds, last = [c], f, 0, 0, 0
    while u is not None and u not in error_stubs and u not in loop_heads:   # may absorb a router pair
        gap = 0 if is_gate(u) else gap + 1
        if gap > 1:
            break
        run.append(u)
        if is_gate(u):
            conds, last = conds + 1, len(run) - 1
        u = primary.get(u)
    if conds >= 3 and not VERTICAL:
        for a_, b_ in zip(run[:last], run[1:last + 1]):
            row_next[a_] = b_
        for g in run[last:]:
            row_next.pop(g, None)   # a router pair past the last gate no longer continues the row
        check_rows.add(c)
        print('row:', ' -> '.join(by_id[n].get('title', '')[:30] for n in run[:last + 1]))


if VERTICAL:
    row_next.clear()
    check_rows.clear()


def is_chain_head(v):
    """First node of a horizontal row (dispatcher spine or check run)."""
    return v in row_next and v not in set(row_next.values())


def return_chain(node, s):
    """`Push X -> init -> back to the loop head`: a short tree chain whose last node
    jumps back to `node` or one of its ancestors. Laid out leftwards on node's row."""
    chain, u = [], s
    while True:
        if u in error_stubs or len(chain) >= RETURN_CHAIN_MAX:
            return None
        chain.append(u)
        kids = children[u]
        if not kids:
            break
        if len(kids) != 1:
            return None
        u = kids[0][1]
    anc = set(ancestors(node))
    if any(v in anc for k, v in out_edges(by_id[chain[-1]])):
        return chain
    return None


def is_pair(nxt, s):
    """Rule 20: `nxt` (success child) and `s` (fallback) are both collapsed leaves of the
    row and both lead only into the same next node M (owned by `nxt`) -- alternatives of one step."""
    if nxt is None or s == nxt or will_expand(nxt) or will_expand(s) or children[s]:
        return False
    if nxt in error_stubs or s in error_stubs or is_chain_head(nxt):
        return False
    m = primary.get(nxt)
    if m is None or len(children[nxt]) != 1:
        return False
    return {v for _, v in out_edges(by_id[nxt])} == {m} == {v for _, v in out_edges(by_id[s])}


def is_bypass(node, s, nxt):
    if nxt is None or will_expand(s) or children[s]:
        return False
    return {v for _, v in out_edges(by_id[s])} == {nxt}


# ---- 4. placement: every coordinate here is a node's TOP-LEFT, circles converted at the end ----
def box(nid, x, y):
    return (x, y, x + W(nid), y + H(nid))


def overlap(a, b, gx, gy):
    return a[0] < b[2] + gx and b[0] < a[2] + gx and a[1] < b[3] + gy and b[1] < a[3] + gy


def pack(rects, lrects, dx0, dy):
    """Smallest dx >= dx0 so that lane rects (shifted by dx, dy) clear `rects` by GAP horizontally."""
    dx = dx0
    while True:
        need = dx
        for l in lrects:
            sl = (l[0] + dx, l[1] + dy, l[2] + dx, l[3] + dy)
            for e in rects:
                if overlap(sl, e, GAP, GAP_Y_PACK):
                    need = max(need, e[2] + GAP - l[0])
        if need == dx:
            return dx
        dx = need


def place(pos, rects, nid, x, y):
    pos[nid] = (x, y)
    rects.append(box(nid, x, y))


def stubs_items(node, x, y, stubs, left):
    """Error stubs of one node. Returns [(nid, x, y)] top-left."""
    out = []
    final = lambda s: primary[s]
    if not will_expand(node):
        if len(stubs) == 1:
            s = stubs[0]
            if left:   # mirror of rule 9b (rule 16: right side taken by a lane)
                rx = x - STUB_INLINE_GAP - NODE_W_COLLAPSE
                out += [(s, rx, y), (final(s), rx - (STUB_INLINE_FINAL_DX - 28), y)]
            else:      # rule 9b
                rx = x + NODE_W_COLLAPSE + STUB_INLINE_GAP
                out += [(s, rx, y), (final(s), rx + STUB_INLINE_FINAL_DX - 28, y)]
        else:          # 2+ stubs off a collapsed check: stacked outside the column (rule 17)
            rx = COL_W + STUB_GAP_X
            for i, s in enumerate(stubs):
                ry = y + i * STUB_STACK_DY
                out += [(s, rx, ry), (final(s), rx + STUB_INLINE_FINAL_DX - 28, ry)]
    else:
        rx = (x - STUB_GAP_X - NODE_W_COLLAPSE) if left else (x + W(node) + STUB_GAP_X)
        for i, s in enumerate(stubs):
            ry = y + i * STUB_HOOK_STACK_DY
            out += [(s, rx, ry), (final(s), rx, ry + STUB_FINAL_DY - 28)]
    return out


def lowest_top(lpos, pred=None):
    c = [(y, n) for n, (x, y) in lpos.items() if not is_circle(n) and (pred is None or pred(n))]
    return max(c)[0] if c else 0


def lay_column(r):
    """A DOWN column with r at its top. Returns (pos, rects) relative to column left/top."""
    pos, rects = {}, []
    lanes = []  # (sid, kind, row_top, wait_flag)
    y, node, col_bottom = 0, r, 0
    pair_mate = {}  # success child -> its fallback, placed on the same row (rule 20)
    hooks = []  # (node, x, y, stubs) placed on the right -- may flip left once lanes are packed (rule 25)
    while node is not None:
        if is_chain_head(node):
            # rule 23: the column turns RIGHT here (Start above the first router, `Normalize` corner)
            rpos, rrects = lay_row(node)
            for n, (lx_, ly_) in rpos.items():
                pos[n] = (lx_, ly_ + y)
            rects += [(a, b + y, c, d + y) for a, b, c, d in rrects]
            col_bottom = max(col_bottom, y + H(node))
            break
        x = colx(node)
        if node in pair_mate:
            x = (COL_W - 2 * NODE_W_COLLAPSE - PAIR_GAP) // 2
            place(pos, rects, pair_mate[node], x + NODE_W_COLLAPSE + PAIR_GAP, y)
        place(pos, rects, node, x, y)
        row_bottom = y + H(node)
        p = primary.get(node)
        secs = list(secondary.get(node, []))
        nxt = p
        if p is not None and p in error_stubs:
            secs.insert(0, p)
            nxt = None
        elif (p is not None and not secs and is_pure_cond(node) and edge_kind_of(node, p) == 'go_if_const'
              and default_go(node) not in (None, p)):
            # a real fork whose default exit is owned elsewhere (`Has image?` -> back to the
            # trunk): the matched branch still steps aside into its own lane (a "staircase")
            secs.insert(0, p)
            nxt = None
        stubs = [s for s in secs if s in error_stubs]
        pairs = [s for s in secs if s not in stubs and is_pair(nxt, s)][:1]
        if pairs:
            pair_mate[nxt] = pairs[0]
            secs.remove(pairs[0])
        bypass = [s for s in secs if s not in stubs and is_bypass(node, s, nxt)]
        rets = [(s, return_chain(node, s)) for s in secs if s not in stubs and s not in bypass]
        rets = [(s, c) for s, c in rets if c]
        lane_s = [s for s in secs if s not in stubs and s not in bypass and s not in dict(rets)]
        ext_bottom = row_bottom
        if stubs:
            for nid, ix, iy in stubs_items(node, x, y, stubs, left=bool(lane_s)):
                place(pos, rects, nid, ix, iy)
                ext_bottom = max(ext_bottom, iy + NODE_W_COLLAPSE)
            if not lane_s:
                hooks.append((node, x, y, stubs))
        cur_right = -GAP
        for s, chain in rets:
            for c in chain:
                cx = cur_right - W(c)
                place(pos, rects, c, cx, y)
                cur_right = cx - GAP
        next_y = row_bottom + GAP
        if will_expand(node) and stubs:
            next_y = max(next_y, ext_bottom + GAP)       # corner hooks hang below the row
        elif nxt is not None and stubs and any(s in error_stubs for s in secondary.get(nxt, []) + [primary.get(nxt)]):
            next_y = max(next_y, ext_bottom + GAP)       # next row has its own stubs on the right
        if bypass:
            b = bypass[0]
            place(pos, rects, b, BYPASS_DX, row_bottom + GAP)
            next_y = max(next_y, row_bottom + GAP + NODE_W_COLLAPSE + GAP)
            for extra_b in bypass[1:]:
                lane_s.append(extra_b)
        for s in lane_s:
            if is_chain_head(s):
                lpos, lrects = lay_row(s)
                lanes.append((s, lpos, lrects, y))
                next_y = max(next_y, y + max(rr[3] for rr in lrects) + GAP)
                continue
            lpos, lrects = lay_column(s)
            lanes.append((s, lpos, lrects, y))
            lane_nodes = set(lpos)
            anc = set(ancestors(node))
            loops = [n for n in lane_nodes if any(v in anc for _, v in out_edges(by_id[n]))]
            merges = [n for n in lane_nodes if nxt is not None and any(v == nxt for _, v in out_edges(by_id[n]))]
            if loops:     # loop body: the trunk continues after the whole body (rule 13)
                next_y = max(next_y, y + lowest_top(lpos))
            if merges:    # optional branch: merge node on the row of the lane's last returning node
                next_y = max(next_y, y + lowest_top(lpos, lambda n: n in merges))
        col_bottom = max(col_bottom, row_bottom)
        if nxt is None:
            break
        y, node = next_y, nxt
    rects.append((0, 0, COL_W, col_bottom))  # the column itself is a corridor -- no lane may cross it
    for s, lpos, lrects, row_top in lanes:
        dx = pack(rects, lrects, COL_W + GAP, row_top)
        for n, (lx, ly) in lpos.items():
            pos[n] = (lx + dx, ly + row_top)
        rects += [(a + dx, b + row_top, c + dx, d + row_top) for a, b, c, d in lrects]
    # rule 25 (`Build response` <- `Save result`): a hook that would sit on the
    # horizontal merge edge of a lane node on the same row (packed right against it) flips LEFT (rule 16)
    for node, x, y, stubs in hooks:
        grp = [n for s in stubs for n in (s, primary[s])]
        gb = [box(n, *pos[n]) for n in grp]
        top, bot, right = min(b[1] for b in gb), max(b[3] for b in gb), max(b[2] for b in gb)
        lane_boxes = [box(n, *pos[n]) for _, lpos, _, _ in lanes for n in lpos
                      if any(v == node for _, v in out_edges(by_id[n]))]   # lane nodes that merge INTO node
        if not any(r[1] < y + H(node) and y < r[3] and 0 <= r[0] - right <= GAP + 8 for r in lane_boxes):
            continue
        items = stubs_items(node, x, y, stubs, left=True)
        others = [r for r in rects if r not in gb]
        if any(overlap(box(n, ix, iy), o, PAIR_GAP, 0) for n, ix, iy in items for o in others):
            continue
        rects = others + [box(n, ix, iy) for n, ix, iy in items]
        for n, ix, iy in items:
            pos[n] = (ix, iy)
    return pos, rects


def lay_row(r):
    """A RIGHT-running row starting at r: a dispatcher spine (routers) or a run of business checks
    (rule 23). Each node's other branches hang below it; a node's position is the first x at which it
    and everything below it clear what is already placed by GAP -- profile packing, so a deep branch
    far below does not push the next node away (rule 22, replaced the old fixed STEP_SPINE=700)."""
    pos, rects = {}, []
    x_cand, node = 0, r
    leaves = []   # collapsed below-leaves that may move down to the row of the node they merge into
    while node is not None:
        nxt = row_next.get(node)
        p = primary.get(node)
        secs = list(secondary.get(node, []))
        tail = None
        if nxt is None and p is not None:
            if p in error_stubs:
                secs.insert(0, p)
            else:
                tail = p
        bypass = [s for s in secs if s not in error_stubs and is_bypass(node, s, nxt)][:1]
        secs = [s for s in secs if s not in bypass]
        # below items, relative to node.x = 0
        below_pos, below_rects, bx = {}, [box(node, 0, 0)], 0
        for s in secs:
            if s in error_stubs:
                rx = W(node) // 2 - NODE_W_COLLAPSE // 2
                ry = H(node) + STUB_GAP_X
                items = [(s, bx + rx, ry), (primary[s], bx + rx, ry + STUB_FINAL_DY - 28)]
                for nid, ix, iy in items:
                    below_pos[nid] = (ix, iy)
                    below_rects.append(box(nid, ix, iy))
            else:
                lpos, lrects = lay_column(s)
                top = H(node) + GAP
                dx = pack(below_rects[1:], lrects, bx, top) if len(below_rects) > 1 else bx
                for n, (lx, ly) in lpos.items():
                    below_pos[n] = (lx + dx, ly + top)
                below_rects += [(a + dx, b + top, c + dx, d + top) for a, b, c, d in lrects]
                if not children[s] and not will_expand(s):
                    leaves.append(s)
            bx = max(rr[2] for rr in below_rects) + GAP
        x = pack(rects, below_rects, x_cand, 0) if rects else x_cand
        pos[node] = (x, 0)
        for n, (bxx, byy) in below_pos.items():
            pos[n] = (bxx + x, byy)
        rects += [(a + x, b, c + x, d) for a, b, c, d in below_rects]
        right = x + W(node)
        if bypass and node == r and r in check_rows:   # the corner's bypass stays under it, centered
            place(pos, rects, bypass[0], x + colx(bypass[0]), H(node) + GAP)
        elif bypass:   # rule 15 turned sideways: between this node and the next one, a little below the row
            place(pos, rects, bypass[0], right + GAP, BYPASS_ROW_DY)
            right += GAP + NODE_W_COLLAPSE
        if p in error_stubs and nxt is None:
            for nid, ix, iy in stubs_items(node, x, 0, [p], left=False):
                place(pos, rects, nid, ix, iy)
            break
        if tail is not None:
            lpos, lrects = lay_column(tail)
            dx = pack(rects, lrects, right + GAP, 0)
            for n, (lx, ly) in lpos.items():
                pos[n] = (lx + dx, ly)
            rects += [(a + dx, b, c + dx, d) for a, b, c, d in lrects]
            break
        x_cand, node = right + GAP, nxt
    # rule 26 (`Checks not completed` -> `Data already saved?`): a collapsed below-leaf whose exits all go
    # to nodes placed further down moves down to the row of the nearest of them, if that row is free
    for s in leaves:
        ys = [pos[t][1] for _, t in out_edges(by_id[s]) if t in pos and pos[t][1] > pos[s][1]]
        if not ys:
            continue
        old, new = box(s, *pos[s]), box(s, pos[s][0], min(ys))
        others = [o for o in rects if o != old]
        if not any(overlap(new, o, PAIR_GAP, PAIR_GAP) for o in others):
            rects = others + [new]
            pos[s] = (new[0], new[1])
    return pos, rects


pos, rects = lay_column(start_id)

lx = max((r_[2] for r_ in rects), default=0) + 400
for i, nid in enumerate(sorted(unreached)):
    pos[nid] = (lx + (i % 6) * 264, (i // 6) * 200)


def compact_lane(cur, a, group):
    """A new side lane (new nodes RIGHT of their anchor's column, with their existing tree
    descendants) took its x offset from the full tidy layout, where neighbouring lanes may occupy
    other columns than on the user's canvas. Pull it back left to the first free column (GAP clear
    of everything else), never past the anchor's column + GAP, never to the right."""
    side = [n for n in group if cur[n][0] >= cur[a][0] + W(a)]
    if not side:
        return
    lane = set(side)
    for n in side:
        lane |= {v for v in tree_desc(n) if v in cur}
    others = [box(o, *cur[o]) for o in cur if o not in lane]
    lane_boxes = [box(n, *cur[n]) for n in lane]
    left = min(cur[n][0] - colx(n) for n in lane)    # leftmost COLUMN edge of the lane
    floor = cur[a][0] + W(a) + GAP
    for dx in range(min(0, floor - left), 1, 4):
        if left + dx < floor:
            continue
        moved = [(b[0] + dx, b[1], b[2] + dx, b[3]) for b in lane_boxes]
        if not any(overlap(m, o, GAP, GAP_Y_PACK) for m in moved for o in others):
            if dx:
                for n in lane:
                    cur[n][0] += dx
                print(f'compact: {dx} on x for lane of {by_id[side[0]].get("title")!r}')
            return


def apply_incremental():
    """Rule 19: every new node is placed at the same offset from its nearest EXISTING tree
    ancestor (the anchor) as the full tidy layout would give it; existing nodes that hang below
    the insertion (tree descendants of a new node) are pushed down/right together with everything
    past them on that axis, by just enough to restore the tidy gap. Nothing is ever pulled closer."""
    def tl(nid):   # current stored coords -> top-left
        n = by_id[nid]
        return (n['x'] - 28, n['y'] - 28) if is_circle(nid) else (n['x'], n['y'])
    cur = {nid: list(tl(nid)) for nid in by_id if nid not in NEW}
    groups = collections.defaultdict(list)   # anchor -> new nodes
    for nid in NEW:
        if nid not in parent:
            print('WARNING: new node unreached from Start, left as is:', by_id[nid].get('title'))
            continue
        a = parent[nid]
        while a in NEW:
            a = parent[a]
        groups[a].append(nid)
    for a in sorted(groups, key=lambda a: (cur[a][1], cur[a][0])):
        ax, ay = cur[a]
        for nid in groups[a]:
            cur[nid] = [ax + pos[nid][0] - pos[a][0], ay + pos[nid][1] - pos[a][1]]
        # a new side-lane node standing over an EXISTING child in the same tidy column takes that
        # child's column (the user's), and the child only moves down if it is now too close
        # (e.g. a new collapsed `Fetch unavailable` fallback landing over the user's `Reply success`)
        aligned = set()
        for nid in groups[a]:
            if pos[nid][0] - pos[a][0] < W(a):
                continue
            for _, e in children[nid]:
                if e in NEW or pos[e][0] - colx(e) != pos[nid][0] - colx(nid):
                    continue
                cur[nid][0] = cur[e][0] - colx(e) + colx(nid)
                aligned.add(e)
                dy = cur[nid][1] + pos[e][1] - pos[nid][1] - cur[e][1]
                if dy > 0:
                    for v in tree_desc(e):
                        if v in cur and v not in NEW:
                            cur[v][1] += dy
                    print(f'shift: +{dy} on y (lane) from {by_id[e].get("title")!r}')
        # existing nodes to push: tree children of the new nodes, plus the anchor's own existing
        # children that now sit PAST a new node on the same axis (a rule-15 bypass inserted between
        # the anchor and its next row: `Lookup unavailable` between `Get role` and `Init`)
        between = []
        for _, e in children[a]:
            if e in NEW:
                continue
            dx, dy = pos[e][0] - pos[a][0], pos[e][1] - pos[a][1]
            if abs(dy) < abs(dx):
                continue   # only the next ROW (a side hook/lane of the anchor is not "after" anything)
            if any(0 < pos[n][1] - pos[a][1] < dy for n in groups[a]):
                between.append(e)
        for e in [v for nid in groups[a] for _, v in children[nid] if v not in NEW and v not in aligned] + between:
            dx, dy = pos[e][0] - pos[a][0], pos[e][1] - pos[a][1]
            axis = 1 if abs(dy) >= abs(dx) else 0
            want = cur[a][axis] + (dy, dx)[axis == 0]
            delta = want - cur[e][axis]
            if delta <= 0:
                continue
            thr = cur[e][axis]
            # down: everything past the threshold moves (rows below the insertion); right: only e's
            # own subtree -- a global x-shift dragged unrelated rows above it too
            movers = (cur if axis == 1 else [n for n in tree_desc(e) if n in cur and n not in NEW])
            for nid in movers:
                if nid not in groups[a] and cur[nid][axis] >= thr:
                    cur[nid][axis] += delta
            print(f'shift: +{delta} on {"xy"[axis]} from {by_id[e].get("title")!r}')
        compact_lane(cur, a, groups[a])
    placed_new = [nid for nid in NEW if nid in cur]
    for nid in placed_new:
        b = box(nid, *cur[nid])
        for o in cur:
            if o != nid and o not in placed_new and overlap(b, box(o, *cur[o]), 0, 0):
                print(f'WARNING: new {by_id[nid].get("title")!r} overlaps {by_id[o].get("title")!r}')
    for nid, (x, y) in cur.items():
        by_id[nid]['x'], by_id[nid]['y'] = (round(x + 28), round(y + 28)) if is_circle(nid) else (round(x), round(y))
    for nid in placed_new:
        extra = json.loads(by_id[nid].get('extra') or '{}')
        extra['modeForm'] = 'expand' if will_expand(nid) else 'collapse'
        by_id[nid]['extra'] = json.dumps(extra, ensure_ascii=False)
        print('new:', by_id[nid].get('title'), extra['modeForm'], by_id[nid]['x'], by_id[nid]['y'])
    with open(PATH, 'w') as f:
        json.dump(d, f, ensure_ascii=False, separators=(',', ':'))


if ONLY_NEW:
    if not NEW:
        print('no new nodes -- nothing to place')
        # structural fixes (--fix-setparam-errors / --split-errors) must still be saved
        with open(PATH, 'w') as f:
            json.dump(d, f, ensure_ascii=False, separators=(',', ':'))
    else:
        apply_incremental()
    sys.exit(0)

# ---- 5. apply: center the drawing on (0,0); circles store their CENTER, the rest TOP-LEFT ----
minx = min(p_[0] for p_ in pos.values()); maxx = max(p_[0] + W(n) for n, p_ in pos.items())
miny = min(p_[1] for p_ in pos.values()); maxy = max(p_[1] + H(n) for n, p_ in pos.items())
xs, ys = round((minx + maxx) / 2), round((miny + maxy) / 2)
for nid, (x, y) in pos.items():
    if is_circle(nid):
        by_id[nid]['x'], by_id[nid]['y'] = round(x - xs + 28), round(y - ys + 28)
    else:
        by_id[nid]['x'], by_id[nid]['y'] = round(x - xs), round(y - ys)

for n in nodes:
    try:
        extra = json.loads(n.get('extra') or '{}')
    except Exception:
        extra = {}
    extra['modeForm'] = 'expand' if will_expand(n['id']) else 'collapse'
    n['extra'] = json.dumps(extra, ensure_ascii=False)

missing = set(by_id) - set(pos)
print('nodes total:', len(nodes), '| placed:', len(pos), '| missing:', len(missing))
print('error stubs:', len(error_stubs), '| loop heads:', len(loop_heads), '| collapsed:', len(collapsed))
span_x, span_y = maxx - minx, maxy - miny
if span_x > 20000 or span_y > 20000:
    print(f'WARNING: canvas span exceeds Corezoid limit (width={span_x}, height={span_y}, limit=20000 each). '
          'Do NOT shrink the constants -- split this process into sub-processes (see SKILL.md).')

with open(PATH, 'w') as f:
    json.dump(d, f, ensure_ascii=False, separators=(',', ':'))
