// Package catalog is the built-in directory of personas amq-squad understands.
// It's intentionally small and Go-native so a new persona is one code change
// plus a rebuild, not a config file users have to discover.
package catalog

import (
	"fmt"
	"strconv"
	"strings"
)

// Role is one persona entry in the catalog. Field semantics:
//
//	ID               short slug, also used as default handle
//	Label            human title shown in listings and role.md
//	PreferredBinary  "claude" or "codex", user can override at team init time
//	Profile          short market-listing copy shown in interactive setup
//	Description      one-line summary seeded into role.md
//	Skills           slash commands worth invoking in this role's priming
//	DefaultPeers     persona IDs this archetype usually talks to
type Role struct {
	ID              string
	Label           string
	PreferredBinary string
	Profile         string
	Description     string
	Skills          []string
	DefaultPeers    []string
}

var roles = []Role{
	{
		ID:              "cpo",
		Label:           "CPO",
		PreferredBinary: "codex",
		Profile:         "Product direction, scope pressure, user value.",
		Description:     "Owns product strategy, vision, and prioritization. Pushes back on scope and sharpens the why before the what.",
		Skills:          []string{"/product-strategy"},
		DefaultPeers:    []string{"cto", "pm", "designer"},
	},
	{
		ID:              "cto",
		Label:           "CTO",
		PreferredBinary: "codex",
		Profile:         "Architecture, tradeoffs, final technical review.",
		Description:     "Owns technical direction, architecture, and engineering tradeoffs. Signs off on the shape of the system.",
		DefaultPeers:    []string{"cpo", "senior-dev", "fullstack", "frontend-dev", "backend-dev", "mobile-dev", "junior-dev", "qa"},
	},
	{
		ID:              "senior-dev",
		Label:           "Senior Developer",
		PreferredBinary: "codex",
		Profile:         "Takes harder code paths and reviews junior work.",
		Description:     "Owns complex implementation, code review, and technical mentorship. Turns architecture into maintainable changes.",
		DefaultPeers:    []string{"cto", "junior-dev", "fullstack", "frontend-dev", "backend-dev", "mobile-dev", "qa"},
	},
	{
		ID:              "fullstack",
		Label:           "Fullstack Developer",
		PreferredBinary: "claude",
		Profile:         "End-to-end feature builder across UI and backend.",
		Description:     "Implements features end to end across frontend and backend. Writes code that gets merged.",
		DefaultPeers:    []string{"cto", "senior-dev", "frontend-dev", "backend-dev", "qa", "pm", "designer"},
	},
	{
		ID:              "frontend-dev",
		Label:           "Frontend Developer",
		PreferredBinary: "claude",
		Profile:         "Product UI, components, state, browser polish.",
		Description:     "Builds and refines the browser product surface. Focuses on components, state, accessibility, and front-end quality.",
		DefaultPeers:    []string{"designer", "pm", "fullstack", "backend-dev", "qa", "cto"},
	},
	{
		ID:              "backend-dev",
		Label:           "Backend Developer",
		PreferredBinary: "codex",
		Profile:         "APIs, data flow, services, integrations.",
		Description:     "Builds backend behavior, APIs, persistence, and service integrations. Keeps data flow and operational boundaries clear.",
		DefaultPeers:    []string{"cto", "fullstack", "frontend-dev", "qa", "pm"},
	},
	{
		ID:              "mobile-dev",
		Label:           "Mobile Developer",
		PreferredBinary: "claude",
		Profile:         "Native and mobile app flows, device polish.",
		Description:     "Builds mobile app flows and platform-specific UX. Focuses on device behavior, responsiveness, and release-ready interaction.",
		DefaultPeers:    []string{"designer", "pm", "backend-dev", "qa", "cto"},
	},
	{
		ID:              "junior-dev",
		Label:           "Junior Developer",
		PreferredBinary: "codex",
		Profile:         "Fast on scoped tasks, needs review before merge.",
		Description:     "Moves quickly on well-scoped implementation tasks. Needs senior or CTO review before changes are considered ready.",
		DefaultPeers:    []string{"senior-dev", "cto", "qa", "fullstack"},
	},
	{
		ID:              "qa",
		Label:           "QA Manager",
		PreferredBinary: "claude",
		Profile:         "Regression thinking, release risk, test coverage.",
		Description:     "Owns test strategy, regression coverage, and release gating. Turns intent into verifiable checks.",
		DefaultPeers:    []string{"junior-dev", "fullstack", "frontend-dev", "backend-dev", "mobile-dev", "senior-dev", "cto", "pm"},
	},
	{
		ID:              "pm",
		Label:           "Project Manager / Product Owner",
		PreferredBinary: "claude",
		Profile:         "Keeps work ordered, unblocked, and shippable.",
		Description:     "Translates product strategy into ordered work. Tracks scope, unblocks, and keeps the team aligned on what ships next.",
		DefaultPeers:    []string{"cpo", "fullstack", "frontend-dev", "mobile-dev", "junior-dev", "qa", "designer"},
	},
	{
		ID:              "designer",
		Label:           "Product Designer",
		PreferredBinary: "claude",
		Profile:         "Product flows, visual shape, UI polish.",
		Description:     "Designs the product surface. Produces UI components, flows, and visual assets, leaning on /frontend-design and /canvas-design.",
		Skills:          []string{"/frontend-design", "/canvas-design"},
		DefaultPeers:    []string{"cpo", "fullstack", "pm"},
	},
}

// All returns a copy of the catalog in display order.
func All() []Role {
	out := make([]Role, len(roles))
	copy(out, roles)
	return out
}

// Lookup returns the role with the given ID, or nil if unknown.
func Lookup(id string) *Role {
	for i := range roles {
		if roles[i].ID == id {
			r := roles[i]
			return &r
		}
	}
	return nil
}

// IDs returns the set of known role IDs in catalog order.
func IDs() []string {
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = r.ID
	}
	return out
}

// ResolveSelection converts the user-facing persona picker syntax into role
// IDs. It accepts catalog IDs, 1-based market numbers, and "all".
func ResolveSelection(line string) ([]string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("no selection provided")
	}
	if strings.EqualFold(line, "all") {
		return IDs(), nil
	}
	picked := strings.Split(line, ",")
	out := make([]string, 0, len(picked))
	for _, p := range picked {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if p == "all" {
			out = append(out, IDs()...)
			continue
		}
		if n, err := strconv.Atoi(p); err == nil {
			if n < 1 || n > len(roles) {
				return nil, fmt.Errorf("persona number %d is out of range", n)
			}
			out = append(out, roles[n-1].ID)
			continue
		}
		if Lookup(p) == nil {
			return nil, fmt.Errorf("unknown persona/role %q. Known personas: %s", p, strings.Join(IDs(), ", "))
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no selection provided")
	}
	return out, nil
}
