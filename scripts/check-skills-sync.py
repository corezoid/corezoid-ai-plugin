#!/usr/bin/env python3
"""
Verify that the list of skills in plugins/corezoid/skills/ matches the lists
in CLAUDE.md (Architecture section) and README.md (skills table).

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

# Skill-name regex: matches kebab-case identifiers (corezoid, corezoid-*, or
# marketplace-publish-validation) either inside backticks or as a bare
# directory reference terminated by `/` in a code block. Both forms appear in
# CLAUDE.md and README.md, so we accept both.
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

    errors = []
    errors += diff_report("CLAUDE.md", disk, claude)
    errors += diff_report("README.md", disk, readme)

    if errors:
        for line in errors:
            print(line, file=sys.stderr)
        print(
            "\nFix: update the affected file(s) so each skill directory is "
            "referenced with backticks (e.g. `corezoid-new-skill`).",
            file=sys.stderr,
        )
        sys.exit(1)

    print(f"OK: {len(disk)} skills consistent across filesystem, CLAUDE.md, README.md")


if __name__ == "__main__":
    main()
