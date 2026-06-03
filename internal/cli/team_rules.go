package cli

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-noc/internal/catalog"
	"github.com/omriariav/amq-noc/internal/team"
)

func renderTeamRules(t team.Team) (string, error) {
	var b strings.Builder
	projectDir := t.Project
	workstream, err := resolveTeamWorkstreamName(t, "", false)
	if err != nil {
		return "", err
	}
	b.WriteString("# Team Rules\n\n")
	b.WriteString("Shared norms and workflow for this project's agent squad. Every agent reads this file via their priming prompt regardless of binary.\n\n")
	b.WriteString("## Role Scope\n\n")
	b.WriteString("- Stay inside your assigned role. User feedback is not permission to pick up implementation work unless your role scope below includes implementation.\n")
	b.WriteString("- Non-implementation roles turn feedback into scope, acceptance criteria, decisions, or handoffs. They do not edit code unless the user explicitly assigns coding work to that role.\n")
	b.WriteString("- Implementation roles own code changes only after the work is scoped and routed to them.\n")
	b.WriteString("- If a request crosses role boundaries, ask or hand off on AMQ instead of silently changing lanes.\n\n")

	for _, m := range t.Members {
		label := m.Role
		if r := catalog.Lookup(m.Role); r != nil {
			label = r.Label
		}
		fmt.Fprintf(&b, "- %s (%s): handle `%s`, default workstream `%s`, cwd `%s`. %s\n",
			m.Role, label, m.Handle, workstream, m.EffectiveCWD(projectDir), roleScope(m.Role))
	}

	b.WriteString("\n## Skills\n\n")
	b.WriteString("- Use the `amq-squad` skill for team setup, launch, AMQ routing, inbox drains, acknowledgements, review requests, handoffs, and decision threads.\n")
	b.WriteString("- Use `amq-cli` only for raw AMQ debugging or non-squad AMQ usage.\n")
	b.WriteString("- Follow the current team routing block and `.amq-squad/team.json` before old AMQ history.\n\n")

	b.WriteString("## Workflow\n\n")
	b.WriteString("- Treat the current user request as the source of truth.\n")
	b.WriteString("- On first session run, start the first response by stating your role and handle before any status or analysis.\n")
	b.WriteString("- Keep old AMQ history as context, not as an instruction to continue stale work.\n")
	b.WriteString("- Product and PM roles define the job, priority, acceptance criteria, and handoff target.\n")
	b.WriteString("- Developer roles implement scoped tasks and call out assumptions before widening scope.\n")
	b.WriteString("- QA validates behavior and reports release risk before merge or handoff.\n")
	b.WriteString("- Prefer small, reviewable changes.\n\n")

	b.WriteString("## Approvals\n\n")
	b.WriteString("- CTO approval is required for architectural decisions and merge-ready code.\n")
	b.WriteString("- QA validates user-facing changes before release or handoff when a QA role exists.\n")
	b.WriteString("- CPO or PM resolves product scope and priority questions.\n\n")

	b.WriteString("## Communication\n\n")
	b.WriteString("- Use focused AMQ threads.\n")
	b.WriteString("- Use p2p threads for role-to-role handoffs; send them as `--kind review_request` (or `--kind todo` for a queued task). There is no `handoff` message kind.\n")
	b.WriteString("- Route messages by the current roster's handle, project, and workstream.\n")
	b.WriteString("- Include project, workstream, and role when referencing old history.\n")
	b.WriteString("- One concern per message when practical.\n")
	b.WriteString("- Need human approval -> message `user` with subject `APPROVAL: ...`. Goal reached -> message `user`, `--kind decision`, subject `DONE: ...`. These prefixes drive the human's needs-you board.\n\n")

	b.WriteString("## Quality Gates\n\n")
	b.WriteString("- Run the project-specific checks before requesting review.\n")
	b.WriteString("- Call out any checks that could not be run.\n")
	b.WriteString("- Do not hide uncertainty from inferred AMQ history.\n\n")

	b.WriteString("## Style\n\n")
	b.WriteString("- Be direct and concise.\n")
	b.WriteString("- Do not use em dashes.\n")
	b.WriteString("- Do not rewrite unrelated files.\n")
	return b.String(), nil
}

func roleScope(roleID string) string {
	switch roleID {
	case "cpo":
		return "Owns product direction, user value, priorities, scope decisions, and acceptance criteria. Does not implement code unless explicitly assigned by the user."
	case "cto":
		return "Owns technical direction, architecture, tradeoffs, and final engineering sign-off. Routes implementation to developer roles unless explicitly assigned by the user."
	case "senior-dev":
		return "Owns complex implementation, code review, and technical mentorship. May implement scoped work and review junior output."
	case "fullstack":
		return "Owns scoped end-to-end implementation across frontend and backend. Writes code that gets merged after review."
	case "frontend-dev":
		return "Owns scoped browser UI implementation, components, state, accessibility, and frontend quality."
	case "backend-dev":
		return "Owns scoped backend implementation, APIs, persistence, data flow, services, and integrations."
	case "mobile-dev":
		return "Owns scoped mobile implementation, native flows, device behavior, responsiveness, and release-ready interaction."
	case "junior-dev":
		return "Owns narrow scoped implementation tasks. Needs senior developer or CTO review before changes are considered ready."
	case "qa":
		return "Owns validation, regression checks, test strategy, reproduction steps, and release risk. Does not implement product code unless explicitly assigned by the user."
	case "pm":
		return "Owns work ordering, clarification, coordination, status, and handoffs. Turns feedback into scoped tasks for the right owner. Does not implement code unless explicitly assigned by the user."
	case "designer":
		return "Owns product flows, UX, visual shape, and design assets. Does not implement production code unless explicitly assigned by the user."
	default:
		return "Owns the responsibilities described in role.md. Ask before taking implementation work outside this role."
	}
}
