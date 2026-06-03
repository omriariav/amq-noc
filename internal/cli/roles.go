package cli

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/omriariav/amq-noc/internal/catalog"
)

type rolesEnvelopeData struct {
	Roles []roleEnvelope `json:"roles"`
}

type roleEnvelope struct {
	Number          int      `json:"number"`
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	PreferredBinary string   `json:"preferred_binary"`
	Profile         string   `json:"profile"`
	Description     string   `json:"description"`
	Skills          []string `json:"skills,omitempty"`
	DefaultPeers    []string `json:"default_peers,omitempty"`
}

func runRoles(args []string) error {
	fs := flag.NewFlagSet("roles", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope instead of the table")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad roles - list built-in team roles

Usage:
  amq-squad roles [--json]

Prints the role market used by 'amq-squad new team --roles ...' and
'amq-squad team init --roles ...'. Use the NUM column for numbered selections,
the ROLE column for ID selections, or all to create every built-in role.

Examples:
  amq-squad roles
  amq-squad roles --json
  amq-squad new team --roles 2,9
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("roles takes no positional arguments")
	}
	data := rolesData()
	if *jsonOut {
		return printJSONEnvelope("roles", data)
	}
	return printRolesTable(data)
}

func rolesData() rolesEnvelopeData {
	all := catalog.All()
	out := rolesEnvelopeData{Roles: make([]roleEnvelope, 0, len(all))}
	for i, r := range all {
		out.Roles = append(out.Roles, roleEnvelope{
			Number:          i + 1,
			ID:              r.ID,
			Label:           r.Label,
			PreferredBinary: r.PreferredBinary,
			Profile:         r.Profile,
			Description:     r.Description,
			Skills:          append([]string(nil), r.Skills...),
			DefaultPeers:    append([]string(nil), r.DefaultPeers...),
		})
	}
	return out
}

func printRolesTable(data rolesEnvelopeData) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NUM\tROLE\tDEFAULT CLI\tPROFILE"); err != nil {
		return err
	}
	for _, r := range data.Roles {
		if _, err := fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", r.Number, r.ID, r.PreferredBinary, r.Profile); err != nil {
			return err
		}
	}
	return w.Flush()
}
