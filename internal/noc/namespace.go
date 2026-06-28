package noc

import (
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-noc/internal/team"
)

const rootNamespaceSession = "_root"

// NamespaceRef mirrors amq-squad v2.9.0's profile/session namespace identity.
// It gives clients a stable key and the concrete storage paths without making
// them reconstruct profile-specific AMQ, brief, or task locations.
type NamespaceRef struct {
	TeamHome   string         `json:"team_home,omitempty"`
	Profile    string         `json:"profile"`
	Session    string         `json:"session"`
	ID         string         `json:"id"`
	Display    string         `json:"display"`
	AMQSession string         `json:"amq_session"`
	AMQRoot    string         `json:"amq_root,omitempty"`
	Paths      NamespacePaths `json:"paths,omitempty"`
}

type NamespacePaths struct {
	ProfileConfig string `json:"profile_config,omitempty"`
	AMQRoot       string `json:"amq_root,omitempty"`
	Brief         string `json:"brief,omitempty"`
	Tasks         string `json:"tasks,omitempty"`
	LaunchRecord  string `json:"launch_record,omitempty"`
}

type GoalBinding struct {
	Mode         string `json:"mode,omitempty"`
	NativeGoal   bool   `json:"native_goal"`
	Verified     bool   `json:"verified"`
	Source       string `json:"source,omitempty"`
	Detail       string `json:"detail,omitempty"`
	BriefPath    string `json:"brief_path,omitempty"`
	TasksPath    string `json:"tasks_path,omitempty"`
	NativeSource string `json:"native_source,omitempty"`
	Command      string `json:"command,omitempty"`
}

func ResolveNamespace(teamHome, profile, session string) NamespaceRef {
	profile = normalizeProfile(profile)
	session = strings.TrimSpace(session)
	displaySession := session
	if displaySession == "" {
		displaySession = "<root>"
	}
	ref := NamespaceRef{
		TeamHome:   strings.TrimSpace(teamHome),
		Profile:    profile,
		Session:    session,
		ID:         namespaceID(profile, session),
		Display:    profile + "/" + displaySession,
		AMQSession: session,
	}
	if ref.TeamHome == "" {
		return ref
	}
	ref.Paths.ProfileConfig = team.ProfilePath(ref.TeamHome, profile)
	if session != "" {
		ref.AMQRoot = namespaceAMQRoot(ref.TeamHome, profile, session)
		ref.Paths.AMQRoot = ref.AMQRoot
		ref.Paths.Brief = namespaceBriefPath(ref.TeamHome, profile, session)
		ref.Paths.Tasks = namespaceTasksPath(ref.TeamHome, profile, session)
	}
	return ref
}

func NamespaceFallbackGoalBinding(ns NamespaceRef) GoalBinding {
	return GoalBinding{
		Mode:       "amq_task_brief",
		NativeGoal: false,
		Verified:   false,
		Source:     "amq-task-brief",
		Detail:     "This runtime does not set a native /goal value; the visible lead is bound by the durable AMQ task, active brief, and task store for the namespace.",
		BriefPath:  ns.Paths.Brief,
		TasksPath:  ns.Paths.Tasks,
	}
}

func namespaceID(profile, session string) string {
	profile = normalizeProfile(profile)
	session = strings.TrimSpace(session)
	if session == "" {
		session = rootNamespaceSession
	}
	return profile + "/" + session
}

func normalizeProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return team.DefaultProfile
	}
	return profile
}

func namespaceAMQRoot(teamHome, profile, session string) string {
	base := filepath.Join(teamHome, AgentMailDirName)
	if normalizeProfile(profile) != team.DefaultProfile {
		base = filepath.Join(base, normalizeProfile(profile))
	}
	return filepath.Join(base, session)
}

func namespaceBriefPath(teamHome, profile, session string) string {
	base := filepath.Join(teamHome, SquadDirName, "briefs")
	if normalizeProfile(profile) != team.DefaultProfile {
		base = filepath.Join(base, normalizeProfile(profile))
	}
	return filepath.Join(base, session+".md")
}

func namespaceTasksPath(teamHome, profile, session string) string {
	base := filepath.Join(teamHome, SquadDirName, "tasks")
	if normalizeProfile(profile) != team.DefaultProfile {
		base = filepath.Join(base, normalizeProfile(profile))
	}
	return filepath.Join(base, session)
}
