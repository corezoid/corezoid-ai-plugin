#!/usr/bin/env python3
"""
scan_stage.py — pre-merge / pre-deploy static validator for exported Corezoid stages.

Detects the defect classes that block a stage merge or deploy on the Corezoid
platform, without touching the live environment:

  [1]  status != active           processes/diagrams  -> "Only active process can be used"
  [1b] empty (no-nodes) processes (battered / recreated shells)
  [2a] broken intra-process node links (to_node_id / err_node_id / go_to pointing at a
       node id absent from the SAME process)  -> "Key 'to_node_id'. 'referenced node X does not exist'"
  [2b] broken / inactive cross-process conv_id references (api_rpc / api_copy / api_get_task
       conv_id pointing at a process missing from the stage, or one that is not active)
       -> "Only active process can be used" / "Access user to conveyor is denied in logic"
  [2c] api_get_task.node_id absent in the TARGET conv_id process

Input may be a .zip export, an extracted directory, or several of them (compare two stages).
Aliases ({{...}} / @alias conv_id) are reported as "unresolvable", never as broken,
because they resolve at deploy time against the live stage.

Usage:
  scan_stage.py PATH [PATH ...] [--label NAME] [--json OUT.json] [--quiet]

Examples:
  scan_stage.py stage_1305.zip
  scan_stage.py ./extracted_stage_dir --json report.json
  scan_stage.py source_735.zip target_1305.zip      # scan both, one report each

Exit code: 0 if no blockers found across all inputs, 1 otherwise (CI-friendly).
"""
import argparse, json, os, re, sys, glob, zipfile, tempfile, collections

HEX24 = re.compile(r'^[0-9a-f]{24}$')
DYN = re.compile(r'\{\{|^@')
NODE_LINK_FIELDS = ('to_node_id', 'err_node_id', 'go_to', 'goto')
CONV_REF_LOGICS = ('api_rpc', 'api_copy', 'api_get_task')


def is_static_node(v):
    return isinstance(v, str) and bool(HEX24.match(v))


def is_dynamic(v):
    return isinstance(v, str) and bool(DYN.search(v))


def folder_of(rel_path):
    """Human-readable folder location (where to find the object in the tree).

    Drops the filename and the ".folder" suffix from each path segment, so
    "4570_CRM.folder/4526_Push.folder/9009_x.conv.json" -> "4570_CRM / 4526_Push".
    Exported names may carry mojibake (UTF-8 shown as latin-1); the id_ prefix
    is kept so the object is always locatable by folder id.
    """
    parts = rel_path.split('/')[:-1]
    # drop the export wrapper segments ("<name>.zip", "<id>_<name>.stage")
    parts = [p for p in parts if not (p.endswith('.zip') or p.endswith('.stage'))]
    return ' / '.join(p[:-len('.folder')] if p.endswith('.folder') else p
                      for p in parts) or '(stage root)'


def collect_conv_files(path):
    """Return (root_dir, [conv.json paths]). Extracts zips to a temp dir."""
    tmp = None
    if path.lower().endswith('.zip'):
        tmp = tempfile.mkdtemp(prefix='czscan_')
        with zipfile.ZipFile(path) as z:
            z.extractall(tmp)
        root = tmp
    elif os.path.isdir(path):
        root = path
    else:
        raise SystemExit(f"error: {path} is neither a .zip nor a directory")
    files = sorted(glob.glob(os.path.join(root, '**', '*.conv.json'), recursive=True))
    return root, files, tmp


def scan(path, label=None):
    label = label or os.path.basename(path.rstrip('/')).replace('.zip', '')
    root, files, tmp = collect_conv_files(path)

    procs = {}            # obj_id -> meta
    parse_errors = []
    for f in files:
        try:
            d = json.load(open(f, encoding='utf-8'))
        except Exception as e:
            parse_errors.append((os.path.relpath(f, root), str(e)))
            continue
        if not isinstance(d, dict):
            continue
        nodes = (d.get('scheme') or {}).get('nodes') or []
        procs[d.get('obj_id')] = {
            'obj_id': d.get('obj_id'),
            'status': d.get('status'),
            'conv_type': d.get('conv_type'),
            'title': d.get('title'),
            'uuid': d.get('uuid'),
            'path': os.path.relpath(f, root),
            'node_ids': {n.get('id') for n in nodes if isinstance(n, dict)},
            'nodes': nodes,
        }

    active_ids = {i for i, m in procs.items() if m['status'] == 'active'}
    inactive = [m for m in procs.values() if m['status'] != 'active']
    empty = [m for m in procs.values()
             if m['conv_type'] in ('process', 'state') and not m['node_ids']]

    broken_node_links, broken_conv_refs, broken_gettask, dynamic_refs = [], [], [], []

    for m in procs.values():
        valid = m['node_ids']
        for n in m['nodes']:
            if not isinstance(n, dict):
                continue
            nid = n.get('id')
            for lg in ((n.get('condition') or {}).get('logics') or []):
                if not isinstance(lg, dict):
                    continue
                t = lg.get('type')
                # [2a] intra-process node links
                for fld in NODE_LINK_FIELDS:
                    tgt = lg.get(fld)
                    if is_static_node(tgt) and tgt not in valid:
                        broken_node_links.append(
                            {'conv_id': m['obj_id'], 'node': nid, 'type': t,
                             'field': fld, 'target': tgt, 'path': m['path']})
                # [2b]/[2c] cross-process conv_id references
                if t in CONV_REF_LOGICS:
                    cv = lg.get('conv_id')
                    if cv is None:
                        continue
                    if is_dynamic(cv):
                        dynamic_refs.append(
                            {'conv_id': m['obj_id'], 'node': nid, 'type': t,
                             'ref': cv, 'path': m['path']})
                        continue
                    try:
                        cvi = int(cv)
                    except (TypeError, ValueError):
                        dynamic_refs.append(
                            {'conv_id': m['obj_id'], 'node': nid, 'type': t,
                             'ref': cv, 'path': m['path']})
                        continue
                    if cvi not in procs:
                        broken_conv_refs.append(
                            {'conv_id': m['obj_id'], 'node': nid, 'type': t,
                             'target_conv': cvi, 'reason': 'missing (not in stage)',
                             'path': m['path']})
                    else:
                        if procs[cvi]['status'] != 'active':
                            broken_conv_refs.append(
                                {'conv_id': m['obj_id'], 'node': nid, 'type': t,
                                 'target_conv': cvi,
                                 'reason': f"target status={procs[cvi]['status']}",
                                 'path': m['path']})
                        if t == 'api_get_task':
                            tn = lg.get('node_id')
                            if is_static_node(tn) and tn not in procs[cvi]['node_ids']:
                                broken_gettask.append(
                                    {'conv_id': m['obj_id'], 'node': nid,
                                     'target_conv': cvi, 'target_node': tn,
                                     'path': m['path']})

    report = {
        'stage': label,
        'totals': {'parsed': len(procs), 'active': len(active_ids),
                   'parse_errors': len(parse_errors)},
        'inactive': [{**{k: m[k] for k in ('obj_id', 'status', 'conv_type', 'title', 'path')},
                      'folder': folder_of(m['path'])}
                     for m in sorted(inactive, key=lambda x: (str(x['status']), x['obj_id'] or 0))],
        'empty': [{**{k: m[k] for k in ('obj_id', 'status', 'title', 'path')},
                   'folder': folder_of(m['path'])}
                  for m in sorted(empty, key=lambda x: x['obj_id'] or 0)],
        'broken_node_links': broken_node_links,
        'broken_conv_refs': broken_conv_refs,
        'broken_gettask': broken_gettask,
        'dynamic_refs_count': len(dynamic_refs),
        'parse_errors': [{'path': p, 'error': e} for p, e in parse_errors],
    }
    # annotate every finding with its folder location ("where to find it")
    for key in ('broken_node_links', 'broken_conv_refs', 'broken_gettask'):
        for x in report[key]:
            x['folder'] = folder_of(x['path'])
    return report


def distinct(rows, key='conv_id'):
    return sorted({r[key] for r in rows})


def print_report(r, quiet=False):
    print(f"\n{'='*78}\nSTAGE: {r['stage']}\n{'='*78}")
    t = r['totals']
    print(f"parsed={t['parsed']}  active={t['active']}  parse_errors={t['parse_errors']}")

    print(f"\n[1] status != active : {len(r['inactive'])} "
          f"-> {distinct(r['inactive'], 'obj_id')}")
    if not quiet:
        for m in r['inactive']:
            print(f"    id={m['obj_id']:<7} {str(m['status']):<8} {m['conv_type']:<8} "
                  f"{m['title']!r}\n        folder: {m['folder']}\n        file:   {m['path']}")

    print(f"\n[1b] empty (no nodes): {len(r['empty'])} "
          f"-> {distinct(r['empty'], 'obj_id')}")
    if not quiet:
        for m in r['empty']:
            print(f"    id={m['obj_id']:<7} {str(m['status']):<8} {m['title']!r}"
                  f"\n        folder: {m['folder']}")

    bnl = r['broken_node_links']
    print(f"\n[2a] broken node links: {len(bnl)} link(s) in "
          f"{len(distinct(bnl))} process(es) -> {distinct(bnl)}")
    if not quiet:
        for x in bnl:
            print(f"    conv={x['conv_id']:<7} node={x['node']} "
                  f"{x['type']}.{x['field']} -> {x['target']}  MISSING"
                  f"\n        folder: {x['folder']}")

    bcr = r['broken_conv_refs']
    print(f"\n[2b] broken/inactive conv refs: {len(bcr)} in "
          f"{len(distinct(bcr))} process(es) -> {distinct(bcr)}")
    if not quiet:
        for x in bcr:
            print(f"    conv={x['conv_id']:<7} node={x['node']} {x['type']} -> "
                  f"conv_id={x['target_conv']}  [{x['reason']}]\n        folder: {x['folder']}")

    bgt = r['broken_gettask']
    print(f"\n[2c] api_get_task node missing in target: {len(bgt)}")
    if not quiet:
        for x in bgt:
            print(f"    conv={x['conv_id']:<7} -> conv {x['target_conv']} "
                  f"node {x['target_node']} MISSING\n        folder: {x['folder']}")

    print(f"\n(unresolvable dynamic/@alias conv refs, not checked: {r['dynamic_refs_count']})")


def has_blockers(r):
    return bool(r['inactive'] or r['empty'] or r['broken_node_links']
                or r['broken_conv_refs'] or r['broken_gettask'] or r['totals']['parse_errors'])


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('paths', nargs='+', help='stage .zip file(s) or extracted dir(s)')
    ap.add_argument('--label', help='label for a single input (default: filename)')
    ap.add_argument('--json', dest='json_out', help='write combined JSON report to this file')
    ap.add_argument('--quiet', action='store_true', help='summary counts only')
    args = ap.parse_args()

    reports = []
    for i, p in enumerate(args.paths):
        lbl = args.label if (args.label and len(args.paths) == 1) else None
        r = scan(p, lbl)
        print_report(r, quiet=args.quiet)
        reports.append(r)

    if args.json_out:
        json.dump(reports if len(reports) > 1 else reports[0],
                  open(args.json_out, 'w'), indent=2, ensure_ascii=False)
        print(f"\n[written {args.json_out}]")

    blockers = any(has_blockers(r) for r in reports)
    print(f"\n{'BLOCKERS FOUND' if blockers else 'No blockers found'}.")
    sys.exit(1 if blockers else 0)


if __name__ == '__main__':
    main()
