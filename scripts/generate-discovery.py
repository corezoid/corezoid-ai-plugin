#!/usr/bin/env python3
"""
Generate public/.well-known/skills/index.json and public/llms.txt
from plugin SKILL.md files.

Usage:
    python3 scripts/generate-discovery.py
"""

import argparse
import json
import os
import re
import shutil
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PLUGIN_DIR = os.path.join(ROOT, "plugins", "corezoid")
SKILLS_DIR = os.path.join(PLUGIN_DIR, "skills")
STEERING_DIR = os.path.join(PLUGIN_DIR, "steering")
MCP_KIRO_JSON = os.path.join(PLUGIN_DIR, ".mcp.kiro.json")
PUBLIC_DIR = os.path.join(ROOT, "public")
DIST_KIRO = os.path.join(ROOT, "dist", "kiro")
REPO_RAW = "https://raw.githubusercontent.com/corezoid/corezoid-ai-plugin/main"
SKILLS_RAW = f"{REPO_RAW}/plugins/corezoid/skills"
DOCS_RAW = f"{REPO_RAW}/plugins/corezoid/docs"


# ---------------------------------------------------------------------------
# Frontmatter parsing (no external deps)
# ---------------------------------------------------------------------------

def _parse_description(fm):
    # Folded/literal scalar  (description: >\n  line1\n  line2)
    folded = re.search(r"^description:\s*[>|]\s*\n((?:[ \t]+[^\n]*\n?)+)", fm, re.MULTILINE)
    if folded:
        lines = folded.group(1).splitlines()
        return " ".join(ln.strip() for ln in lines if ln.strip())

    # Double-quoted inline
    dq = re.search(r'^description:\s*"(.*)"', fm, re.MULTILINE)
    if dq:
        return dq.group(1).replace('\\"', '"').strip()

    # Single-quoted inline
    sq = re.search(r"^description:\s*'(.*)'", fm, re.MULTILINE)
    if sq:
        return sq.group(1).replace("''", "'").strip()

    # Plain inline
    plain = re.search(r"^description:\s*(.+)$", fm, re.MULTILINE)
    if plain:
        return plain.group(1).strip()

    return None


def parse_frontmatter(path):
    with open(path, encoding="utf-8") as f:
        content = f.read()

    m = re.match(r"^---\n(.*?)\n---", content, re.DOTALL)
    if not m:
        return None
    fm = m.group(1)

    name_m = re.search(r"^name:\s*(.+)$", fm, re.MULTILINE)
    name = name_m.group(1).strip() if name_m else None

    return {"name": name, "description": _parse_description(fm)}


# ---------------------------------------------------------------------------
# Skills discovery
# ---------------------------------------------------------------------------

def collect_skills():
    skills = []
    for entry in sorted(os.listdir(SKILLS_DIR)):
        skill_path = os.path.join(SKILLS_DIR, entry)
        skill_md = os.path.join(skill_path, "SKILL.md")
        if not os.path.isfile(skill_md):
            continue

        fm = parse_frontmatter(skill_md)
        if not fm or not fm["name"] or not fm["description"]:
            print(f"WARN: skipping {entry} — missing name or description", file=sys.stderr)
            continue

        # List all .md files in the skill directory
        md_files = []
        for root, dirs, files in os.walk(skill_path):
            dirs.sort()
            for fname in sorted(files):
                if fname.endswith(".md"):
                    rel = os.path.relpath(os.path.join(root, fname), skill_path)
                    md_files.append(rel)

        skills.append({
            "name": fm["name"],
            "description": fm["description"],
            "dir": entry,
            "files": md_files,
        })
    return skills


# ---------------------------------------------------------------------------
# Generators
# ---------------------------------------------------------------------------

def generate_index_json(skills):
    return {
        "skills": [
            {
                "name": s["name"],
                "description": s["description"],
                "files": [f"{SKILLS_RAW}/{s['dir']}/{f}" for f in s["files"]],
            }
            for s in skills
        ]
    }


MCP_TOOLS = [
    ("login",            "Authenticate with Corezoid via OAuth2 browser flow"),
    ("pull-process",     "Export a single process definition to a local .conv.json file"),
    ("pull-folder",      "Recursively export all processes from a Corezoid folder"),
    ("push-process",     "Validate and deploy a local process file to Corezoid"),
    ("lint-process",     "Validate process structure — orphaned nodes, noop conditions, unused params"),
    ("run-task",         "Execute a task on a deployed Corezoid process"),
    ("create-process",   "Create a new empty process inside a Corezoid folder"),
    ("create-folder",    "Create a new folder inside a Corezoid folder"),
    ("create-variable",  "Create an environment variable in a Corezoid folder"),
    ("create-dashboard", "Create a dashboard for visualizing process node metrics"),
    ("add-chart",        "Add a chart (column, pie, funnel, table) to a dashboard"),
    ("list-workspaces",  "List Corezoid workspaces available to the authenticated user"),
    ("list-projects",    "List projects inside a workspace"),
    ("list-stages",      "List stages (environments) inside a project"),
    ("modify-task",      "Modify an existing task's data"),
    ("delete-task",      "Delete a task from a process"),
    ("logout",           "Remove saved Corezoid credentials from disk"),
]


def generate_llms_txt(skills, version):
    lines = [
        "# Corezoid AI Plugin",
        "",
        "> Official Claude Code plugin for Corezoid BPM platform. "
        "Provides skills and MCP tools for creating, editing, reviewing, "
        "and managing Corezoid business processes directly from the IDE.",
        "",
        "## Skills",
        "",
    ]

    for s in skills:
        url = f"{SKILLS_RAW}/{s['dir']}/SKILL.md"
        # First sentence as teaser
        teaser = s["description"].split(". ")[0].rstrip(".")
        lines.append(f"- [{s['name']}]({url}): {teaser}")

    lines += [
        "",
        "## MCP Tools",
        "",
        "The plugin bundles a Go MCP server (`convctl`) with these tools:",
        "",
    ]

    for name, desc in MCP_TOOLS:
        lines.append(f"- **{name}**: {desc}")

    lines += [
        "",
        "## Documentation",
        "",
        f"- [Node Structures]({DOCS_RAW}/node-structures.md): "
        "JSON schemas for all 24 Corezoid node types",
        f"- [Variables Guide]({DOCS_RAW}/variables-guide.md): "
        "Environment variable syntax `{{env_var[@name]}}`",
        f"- [Process Docs]({DOCS_RAW}/process/): "
        "Process format, validation rules, and error handling",
        f"- [Node Docs]({DOCS_RAW}/nodes/): "
        "Per-node-type documentation (24 types)",
        "",
        "## Optional",
        "",
        f"- [Skills Index]({REPO_RAW}/public/.well-known/skills/index.json): "
        "Machine-readable agent discovery index",
        f"- [Changelog]({REPO_RAW}/CHANGELOG.md): Release history",
        "",
    ]

    return "\n".join(lines)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def read_version():
    plugin_json = os.path.join(ROOT, "plugins", "corezoid", ".claude-plugin", "plugin.json")
    try:
        with open(plugin_json) as f:
            return json.load(f).get("version", "unknown")
    except OSError:
        return "unknown"


def emit_kiro_overlay(skills):
    """Materialise dist/kiro/.kiro/{settings,steering,skills} from the
    canonical plugin payload. Skills and steering files are COPIED (not
    symlinked) so the resulting dist/kiro is a portable artifact suitable
    for zipping and attaching to a GitHub Release."""
    kiro_root = os.path.join(DIST_KIRO, ".kiro")

    # settings/mcp.json — verbatim copy of .mcp.kiro.json
    settings_dst = os.path.join(kiro_root, "settings")
    os.makedirs(settings_dst, exist_ok=True)
    shutil.copy2(MCP_KIRO_JSON, os.path.join(settings_dst, "mcp.json"))

    # steering/<file>.md — every .md under plugins/corezoid/steering/
    steering_dst = os.path.join(kiro_root, "steering")
    os.makedirs(steering_dst, exist_ok=True)
    if os.path.isdir(STEERING_DIR):
        for name in sorted(os.listdir(STEERING_DIR)):
            if name.endswith(".md"):
                shutil.copy2(
                    os.path.join(STEERING_DIR, name),
                    os.path.join(steering_dst, name),
                )

    # skills/<dir>/SKILL.md and sibling .md files
    skills_dst = os.path.join(kiro_root, "skills")
    for s in skills:
        skill_src = os.path.join(SKILLS_DIR, s["dir"])
        skill_dst = os.path.join(skills_dst, s["dir"])
        for rel in s["files"]:
            src = os.path.join(skill_src, rel)
            dst = os.path.join(skill_dst, rel)
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            shutil.copy2(src, dst)

    print(f"Written: {os.path.relpath(DIST_KIRO, ROOT)} (.kiro/{{settings,steering,skills}})")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--kiro",
        action="store_true",
        help="Also emit a runtime .kiro/ overlay under dist/kiro for AWS Kiro installs.",
    )
    args = parser.parse_args()

    if not os.path.isdir(SKILLS_DIR):
        print(f"ERROR: skills dir not found: {SKILLS_DIR}", file=sys.stderr)
        sys.exit(1)

    skills = collect_skills()
    if not skills:
        print("ERROR: no skills found", file=sys.stderr)
        sys.exit(1)
    print(f"Found {len(skills)} skills: {[s['name'] for s in skills]}")

    version = read_version()

    # public/.well-known/skills/index.json
    skills_out_dir = os.path.join(PUBLIC_DIR, ".well-known", "skills")
    os.makedirs(skills_out_dir, exist_ok=True)
    index_path = os.path.join(skills_out_dir, "index.json")
    with open(index_path, "w", encoding="utf-8") as f:
        json.dump(generate_index_json(skills), f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(f"Written: {os.path.relpath(index_path, ROOT)}")

    # public/llms.txt
    os.makedirs(PUBLIC_DIR, exist_ok=True)
    llms_path = os.path.join(PUBLIC_DIR, "llms.txt")
    with open(llms_path, "w", encoding="utf-8") as f:
        f.write(generate_llms_txt(skills, version))
    print(f"Written: {os.path.relpath(llms_path, ROOT)}")

    if args.kiro:
        emit_kiro_overlay(skills)


if __name__ == "__main__":
    main()
