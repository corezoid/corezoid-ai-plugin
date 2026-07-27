// OpenCode host adapter for the Corezoid plugin.
//
// Registers every SKILL.md under ../skills/ as an OpenCode tool named
// `corezoid_<skill_name>`, with `${CLAUDE_PLUGIN_ROOT}` resolved in-memory
// to this plugin's on-disk location. The model receives the same skill
// contents that Claude Code / Codex would inject natively — OpenCode has
// no native concept of Anthropic Agent Skills.
//
// The MCP server (Go binary launched by ../mcp-server/run.sh) is configured
// separately via opencode.json — see plugins/corezoid/.mcp.opencode.json
// and scripts/install-opencode.sh.

import { type Plugin, tool } from "@opencode-ai/plugin"
import { discoverSkills, resolvePluginRoot } from "./skills-loader.ts"

export const CorezoidOpenCodePlugin: Plugin = async () => {
  const pluginRoot = resolvePluginRoot(import.meta.url)
  const skills = await discoverSkills(pluginRoot)

  const tools: Record<string, ReturnType<typeof tool>> = {}
  for (const skill of skills) {
    const body = skill.body
    tools[skill.toolName] = tool({
      description: skill.description,
      args: {},
      async execute() {
        return body
      },
    })
  }

  return { tool: tools }
}

export default CorezoidOpenCodePlugin
