#!/usr/bin/env python3
"""
Generate public/.well-known/skills/index.json and public/llms.txt
from plugin SKILL.md files.

Usage:
    python3 scripts/generate-discovery.py
"""

import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SKILLS_DIR = os.path.join(ROOT, "plugins", "corezoid", "skills")
PUBLIC_DIR = os.path.join(ROOT, "public")
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


TOOLS_REGISTRY = os.path.join(
    ROOT, "plugins", "corezoid", "mcp-server", "tools_registry.go"
)

# Matches one registry entry's Name/Description pair. Description values are
# single Go string literals with escaped inner quotes; [^"\\] plus an escape
# alternative is what keeps the match from stopping at the first \".
_TOOL_RE = re.compile(
    r'Name:\s*"(?P<name>[a-z0-9-]+)",\s*\n\s*'
    r'Description:\s*"(?P<desc>(?:[^"\\]|\\.)*)"',
    re.MULTILINE,
)


def _first_sentence(text):
    """First sentence of a tool description, for the one-line index entry.

    Tool descriptions are long on purpose — they are the model's instructions —
    but llms.txt is an index, so only the opening claim belongs here.
    """
    text = text.replace('\\"', '"').replace("\\\\", "\\")
    # A sentence end is ". " followed by a capital; requiring that keeps
    # decimals and mid-sentence abbreviations ("e.g. promote", "i.e. the
    # stage") from cutting the line in half.
    head = re.split(r"(?<=[a-z0-9)\]])\.\s+(?=[A-Z])", text, maxsplit=1)[0]
    return head.rstrip(".").strip()


def read_mcp_tools():
    """Read the MCP tool list from tools_registry.go — the single source of truth.

    This used to be a hand-maintained list in this file. It drifted to 18 of 71
    tools, so llms.txt and the skills index advertised a plugin several releases
    old and omitted whole feature areas (snapshots, groups, layout, deploy,
    create-communications-orchestrator). A generated list cannot drift: a tool
    that exists is listed, and one that is renamed is renamed here too.
    """
    with open(TOOLS_REGISTRY, encoding="utf-8") as fh:
        src = fh.read()

    tools = [(m.group("name"), _first_sentence(m.group("desc")))
             for m in _TOOL_RE.finditer(src)]

    if not tools:
        sys.exit(
            f"generate-discovery: found no tools in {TOOLS_REGISTRY} — the "
            "registry format changed and this parser needs updating; refusing "
            "to publish a discovery file with an empty tool list"
        )

    seen = set()
    for name, _ in tools:
        if name in seen:
            sys.exit(f"generate-discovery: tool {name!r} is declared twice")
        seen.add(name)

    return sorted(tools)


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

    for name, desc in read_mcp_tools():
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


def main():
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


if __name__ == "__main__":
    main()
