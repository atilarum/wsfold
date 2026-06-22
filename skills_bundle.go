package skillbundle

import "embed"

// AgentSkills contains the public WSFold skills bundled into released binaries.
//
//go:embed plugins/wsfold/skills
var AgentSkills embed.FS
