// Tests for the OpenCode skills loader. Runs under `bun test`.
//
// Deliberately avoids importing index.ts so we don't need @opencode-ai/plugin
// or zod installed to exercise the skill-discovery logic.

import { describe, expect, test } from "bun:test"
import { mkdirSync, writeFileSync, rmSync, existsSync } from "node:fs"
import { join } from "node:path"
import { tmpdir } from "node:os"
import {
  parseFrontmatter,
  substituteRoot,
  skillToolName,
  discoverSkills,
  resolvePluginRoot,
} from "./skills-loader.ts"

describe("parseFrontmatter", () => {
  test("parses simple inline fields", () => {
    const raw = "---\nname: foo\ndescription: bar baz\n---\n\n# body\n"
    const got = parseFrontmatter(raw)
    expect(got?.frontmatter.name).toBe("foo")
    expect(got?.frontmatter.description).toBe("bar baz")
    expect(got?.body).toBe("# body\n")
  })

  test("parses folded multi-line description with '>'", () => {
    const raw = [
      "---",
      "name: corezoid-create",
      "description: >",
      "  first line",
      "  second line",
      "---",
      "",
      "body",
    ].join("\n")
    const got = parseFrontmatter(raw)
    expect(got?.frontmatter.name).toBe("corezoid-create")
    expect(got?.frontmatter.description).toBe("first line second line")
  })

  test("returns null when frontmatter is missing", () => {
    expect(parseFrontmatter("# just a heading")).toBeNull()
  })

  test("returns null when required fields are absent", () => {
    const raw = "---\ndescription: no name\n---\nbody"
    expect(parseFrontmatter(raw)).toBeNull()
  })
})

describe("substituteRoot", () => {
  test("replaces braced token", () => {
    expect(substituteRoot("see ${CLAUDE_PLUGIN_ROOT}/docs/x.md", "/p")).toBe(
      "see /p/docs/x.md",
    )
  })

  test("replaces unbraced token", () => {
    expect(substituteRoot("see $CLAUDE_PLUGIN_ROOT/docs/x.md", "/p")).toBe(
      "see /p/docs/x.md",
    )
  })

  test("replaces all occurrences", () => {
    const body = "${CLAUDE_PLUGIN_ROOT}/a ${CLAUDE_PLUGIN_ROOT}/b $CLAUDE_PLUGIN_ROOT/c"
    expect(substituteRoot(body, "/p")).toBe("/p/a /p/b /p/c")
  })
})

describe("skillToolName", () => {
  test("prefixes with corezoid_ and converts hyphens to underscores", () => {
    expect(skillToolName("corezoid-create")).toBe("corezoid_corezoid_create")
    expect(skillToolName("state-diagram-edit")).toBe("corezoid_state_diagram_edit")
  })
})

describe("discoverSkills — synthetic fixture", () => {
  const root = join(tmpdir(), `corezoid-opencode-test-${Date.now()}`)

  function seed(name: string, contents: string) {
    const dir = join(root, "skills", name)
    mkdirSync(dir, { recursive: true })
    writeFileSync(join(dir, "SKILL.md"), contents)
  }

  function cleanup() {
    if (existsSync(root)) rmSync(root, { recursive: true, force: true })
  }

  test("registers each SKILL.md as a tool", async () => {
    cleanup()
    seed("alpha", "---\nname: alpha\ndescription: alpha skill\n---\nA body ${CLAUDE_PLUGIN_ROOT}/x\n")
    seed("beta-gamma", "---\nname: beta-gamma\ndescription: bg skill\n---\nB body\n")
    // Skill dir with no SKILL.md is skipped.
    mkdirSync(join(root, "skills", "empty"), { recursive: true })

    const skills = await discoverSkills(root)
    expect(skills).toHaveLength(2)

    const alpha = skills.find((s) => s.toolName === "corezoid_alpha")
    const beta = skills.find((s) => s.toolName === "corezoid_beta_gamma")
    expect(alpha).toBeDefined()
    expect(beta).toBeDefined()
    expect(alpha!.description).toBe("alpha skill")
    expect(alpha!.body).toContain(`${root}/x`)
    expect(alpha!.body).not.toContain("${CLAUDE_PLUGIN_ROOT}")

    cleanup()
  })

  test("returns empty list when skills dir is missing", async () => {
    cleanup()
    mkdirSync(root, { recursive: true })
    const skills = await discoverSkills(root)
    expect(skills).toHaveLength(0)
    cleanup()
  })
})

describe("discoverSkills — against real corezoid plugin", () => {
  test("finds all real SKILL.md files with valid frontmatter", async () => {
    const pluginRoot = resolvePluginRoot(import.meta.url)
    const skills = await discoverSkills(pluginRoot)

    // Sanity: at least the well-known core skills must load.
    expect(skills.length).toBeGreaterThanOrEqual(10)

    const names = skills.map((s) => s.toolName)
    expect(names).toContain("corezoid_corezoid")
    expect(names).toContain("corezoid_corezoid_create")
    expect(names).toContain("corezoid_corezoid_edit")

    // Names all follow corezoid_<snake> pattern.
    for (const n of names) {
      expect(n).toMatch(/^corezoid_[a-z0-9_]+$/)
    }

    // Descriptions all meet Anthropic Agent Skills Spec minimum (≥20 chars).
    for (const s of skills) {
      expect(s.description.length).toBeGreaterThanOrEqual(20)
    }

    // No skill body still contains the raw token — substitution happened.
    for (const s of skills) {
      expect(s.body).not.toContain("${CLAUDE_PLUGIN_ROOT}")
      expect(s.body).not.toContain("$CLAUDE_PLUGIN_ROOT")
    }
  })
})
