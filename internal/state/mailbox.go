package state

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Maildir subdirectory names. tmp (in-flight) is intentionally NOT scanned.
const (
	inboxDir = "inbox"
	dirNew   = "new" // unread
	dirCur   = "cur" // read
)

// scanMailbox walks an agent's inbox/new and inbox/cur, parsing every message
// file. new=UNREAD, cur=READ; tmp is ignored. Torn/partial/invalid files are
// skipped and recorded as Warnings (never fatal, never panic). A schema
// mismatch surfaces the message but adds a degraded-read Warning.
//
// owner is the handle whose inbox this is; it is stamped onto every Message so
// downstream unread-by computation needs no second walk.
func scanMailbox(agentDir, owner string, now func() time.Time) ([]Message, []Warning) {
	var msgs []Message
	var warns []Warning

	for _, sub := range []struct {
		dir   string
		state MailboxState
	}{
		{dirNew, MailboxNew},
		{dirCur, MailboxCur},
	} {
		dirPath := filepath.Join(agentDir, inboxDir, sub.dir)
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			// A missing new/cur subdir is normal (agent never received mail in
			// that state); only a real read error is worth a warning.
			if !os.IsNotExist(err) {
				warns = append(warns, Warning{Path: dirPath, Reason: "read inbox dir: " + err.Error()})
			}
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(dirPath, e.Name())
			m, ok, perr := parseMessageFile(path, owner, sub.state, now)
			if !ok {
				reason := "skipped malformed message"
				if perr != nil {
					reason = "skipped: " + perr.Error()
				}
				warns = append(warns, Warning{Path: path, Reason: reason})
				continue
			}
			if !m.SchemaOK {
				warns = append(warns, Warning{
					Path:   path,
					Reason: "schema mismatch (degraded read)",
				})
			}
			msgs = append(msgs, m)
		}
	}

	sort.SliceStable(msgs, func(i, j int) bool {
		if !msgs[i].Created.Equal(msgs[j].Created) {
			return msgs[i].Created.Before(msgs[j].Created)
		}
		return msgs[i].ID < msgs[j].ID
	})
	return msgs, warns
}
