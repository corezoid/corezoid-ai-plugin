// Pure skill-discovery + frontmatter logic. Split out from index.ts so tests
// can import it without pulling in the @opencode-ai/plugin peer dependency.

import { readFile, readdir } from "node:fs/promises"
import { existsSync, realpathSync } from "node:fs"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const TOKEN_BRACED = "${CLAUDE_PLUGIN_ROOT}"
const TOKEN_UNBRACED = "$CLAUDE_PLUGIN_ROOT"

export interface SkillFrontmatter {
  name: string
  description: string
}

export interface Skill {
  toolName: string
  description: string
  body: string
}

export function resolvePluginRoot(moduleUrl: string): string {
  // In npm-installed layout the file lives at <pkg>/index.ts (or .js after build);
  // in-repo it lives at plugins/corezoid/opencode-plugin/index.ts.
  // Either way, one dir up is the corezoid plugin root (siblings: skills/, mcp-server/, docs/...).
  const here = dirname(fileURLToPath(moduleUrl))
  const candidate = resolve(here, "..")
  return realpathSync(candidate)
}

export function parseFrontmatter(
  raw: string,
): { frontmatter: SkillFrontmatter; body: string } | null {
  if (!raw.startsWith("---")) return null
  const end = raw.indexOf("\n---", 3)
  if (end < 0) return null

  const yaml = raw.slice(3, end).trim()
  const body = raw.slice(end + 4).replace(/^\r?\n/, "")

  const fm: Partial<SkillFrontmatter> = {}
  let key: keyof SkillFrontmatter | null = null
  let accum: string[] = []

  const flush = () => {
    if (key) {
      fm[key] = accum.join(" ").replace(/\s+/g, " ").trim()
    }
    key = null
    accum = []
  }

  for (const line of yaml.split("\n")) {
    const m = line.match(/^([A-Za-z_-]+):\s*(.*)$/)
    if (m) {
      flush()
      const [, k, rest] = m
      if (k === "name" || k === "description") {
        key = k
        if (rest && rest.trim() !== ">" && rest.trim() !== "|") {
          accum.push(rest.trim())
        }
      }
    } else if (key) {
      accum.push(line.trim())
    }
  }
  flush()

  if (!fm.name || !fm.description) return null
  return { frontmatter: fm as SkillFrontmatter, body }
}

export function substituteRoot(body: string, pluginRoot: string): string {
  return body.split(TOKEN_BRACED).join(pluginRoot).split(TOKEN_UNBRACED).join(pluginRoot)
}

export function skillToolName(skillDirName: string): string {
  return `corezoid_${skillDirName.replace(/-/g, "_")}`
}

export async function discoverSkills(pluginRoot: string): Promise<Skill[]> {
  const skillsDir = join(pluginRoot, "skills")
  if (!existsSync(skillsDir)) return []

  const entries = await readdir(skillsDir, { withFileTypes: true })
  const skills: Skill[] = []

  for (const entry of entries) {
    if (!entry.isDirectory()) continue
    const skillPath = join(skillsDir, entry.name, "SKILL.md")
    if (!existsSync(skillPath)) continue

    const raw = await readFile(skillPath, "utf8")
    const parsed = parseFrontmatter(raw)
    if (!parsed) continue

    skills.push({
      toolName: skillToolName(entry.name),
      description: parsed.frontmatter.description,
      body: substituteRoot(parsed.body, pluginRoot),
    })
  }

  skills.sort((a, b) => a.toolName.localeCompare(b.toolName))
  return skills
}
