#!/usr/bin/env python3
"""
Verify that the list of skills in plugins/corezoid/skills/ matches the lists
in CLAUDE.md (Architecture section), README.md (skills table), and POWER.md
(Kiro Power skill list), and that every SKILL.md has a frontmatter Claude Code
can read: an opening `---` on the first line, a closing `---`, `name` equal to
the directory name, and a non-empty `description` of at most
MAX_DESCRIPTION_CHARS characters.

Exits 0 on match, 1 on mismatch with a diff printed to stderr.

Usage:
    python3 scripts/check-skills-sync.py
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SKILLS_DIR = os.path.join(ROOT, "plugins", "corezoid", "skills")
CLAUDE_MD = os.path.join(ROOT, "CLAUDE.md")
README_MD = os.path.join(ROOT, "README.md")
POWER_MD = os.path.join(ROOT, "POWER.md")

# Skill-name regex: matches kebab-case identifiers (corezoid, corezoid-*, or
# marketplace-publish-validation) either inside backticks or as a bare
# directory reference terminated by `/` in a code block. Both forms appear in
# the checked documentation files, so we accept both.
SKILL_TOKEN = re.compile(
    r"(?:`|(?<=\s)|(?<=^))(corezoid(?:-[a-z0-9]+)*|marketplace-publish-validation)(?:`|/)",
    re.MULTILINE,
)


def fs_skills():
    """Directory names under plugins/corezoid/skills/ that contain a SKILL.md."""
    result = set()
    for entry in os.listdir(SKILLS_DIR):
        if os.path.isfile(os.path.join(SKILLS_DIR, entry, "SKILL.md")):
            result.add(entry)
    return result


# Skill descriptions are loaded into every session that has the plugin
# installed, so they are kept short. Raise this only with a reason.
MAX_DESCRIPTION_CHARS = 200


def parse_description(frontmatter):
    """Return the description value, folding a `>`/`|` block into one line."""
    lines = frontmatter.split("\n")
    for i, line in enumerate(lines):
        m = re.match(r"description:\s*(.*)$", line)
        if not m:
            continue
        value = m.group(1).strip()
        if value in (">", "|", ">-", "|-"):
            block = []
            for nxt in lines[i + 1:]:
                if nxt and not nxt[0].isspace():
                    break
                block.append(nxt.strip())
            return " ".join(part for part in block if part)
        return value.strip("\"'")
    return None


def frontmatter_errors():
    """Return error lines for SKILL.md files whose frontmatter is unusable."""
    errors = []
    for entry in sorted(fs_skills()):
        path = os.path.join(SKILLS_DIR, entry, "SKILL.md")
        with open(path, encoding="utf-8") as f:
            content = f.read()
        label = f"plugins/corezoid/skills/{entry}/SKILL.md"
        m = re.match(r"---\n(.*?)\n---\n", content, re.DOTALL)
        if not m:
            errors.append(
                f"ERROR: {label}: frontmatter must start with `---` on the first "
                "line (no leading spaces) and close with `---`"
            )
            continue
        frontmatter = m.group(1)
        name = re.search(r"^name:\s*(.+?)\s*$", frontmatter, re.MULTILINE)
        if not name or name.group(1) != entry:
            found = name.group(1) if name else "missing"
            errors.append(f"ERROR: {label}: name is {found!r}, expected {entry!r}")
        description = parse_description(frontmatter)
        if not description:
            errors.append(f"ERROR: {label}: description is missing or empty")
        elif len(description) > MAX_DESCRIPTION_CHARS:
            errors.append(
                f"ERROR: {label}: description is {len(description)} characters, "
                f"limit is {MAX_DESCRIPTION_CHARS} (it loads into every session)"
            )
    return errors


# Known kebab-case identifiers that match the skill regex but are NOT skills.
# The plugin's own package name is the most common false positive; add more
# here if new project-level slugs collide with the skill naming convention.
NOT_SKILLS = {
    "corezoid-ai-plugin",
}


def extract_skills(path):
    """Return the set of skill names mentioned in a markdown file."""
    with open(path, encoding="utf-8") as f:
        content = f.read()
    return {name for name in SKILL_TOKEN.findall(content) if name not in NOT_SKILLS}


def diff_report(label, expected, actual):
    """Return a list of error lines, or [] on match."""
    missing = expected - actual
    extra = actual - expected
    if not missing and not extra:
        return []
    lines = [f"ERROR: {label} skill list is out of sync with plugins/corezoid/skills/"]
    if missing:
        lines.append(f"  missing in {label} (present on disk, not documented):")
        for name in sorted(missing):
            lines.append(f"    - {name}")
    if extra:
        lines.append(f"  extra in {label} (documented, no directory on disk):")
        for name in sorted(extra):
            lines.append(f"    - {name}")
    return lines


def main():
    disk = fs_skills()
    claude = extract_skills(CLAUDE_MD)
    readme = extract_skills(README_MD)
    power = extract_skills(POWER_MD)

    errors = []
    errors += diff_report("CLAUDE.md", disk, claude)
    errors += diff_report("README.md", disk, readme)
    errors += diff_report("POWER.md", disk, power)
    errors += frontmatter_errors()

    if errors:
        for line in errors:
            print(line, file=sys.stderr)
        print(
            "\nFix: reference each skill directory with backticks in the docs "
            "(e.g. `corezoid-new-skill`) and correct the SKILL.md frontmatter.",
            file=sys.stderr,
        )
        sys.exit(1)

    print(
        f"OK: {len(disk)} skills consistent across filesystem, "
        "CLAUDE.md, README.md, POWER.md; frontmatter valid"
    )


if __name__ == "__main__":
    main()
