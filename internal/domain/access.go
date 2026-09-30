package domain

import "time"

// ShareAction is semantic: callback strings and slash names are not rights.
type ShareAction string

const (
	ShareOutput      ShareAction = "output"
	ShareScreen      ShareAction = "screen"
	ShareHistory     ShareAction = "history"
	ShareOverview    ShareAction = "overview"
	SharePreferences ShareAction = "preferences"
	SharePrompt      ShareAction = "prompt"
	ShareAttachment  ShareAction = "attachment"
	ShareDialog      ShareAction = "dialog"
	ShareKeys        ShareAction = "keys"
	ShareForward     ShareAction = "forward"
	ShareGit         ShareAction = "git"
	ShareClose       ShareAction = "close"
	ShareFocus       ShareAction = "focus"
)

type AccessReason string

const (
	AccessAllowed     AccessReason = "allowed"
	AccessInvalid     AccessReason = "invalid"
	AccessInactive    AccessReason = "inactive"
	AccessExpired     AccessReason = "expired"
	AccessActor       AccessReason = "wrong-actor"
	AccessDestination AccessReason = "wrong-destination"
	AccessRevision    AccessReason = "stale-revision"
	AccessSession     AccessReason = "changed-session"
	AccessCapability  AccessReason = "missing-capability"
)

// ShareOrigin travels unchanged through asynchronous jobs. Resolve it from
// authenticated update fields and the policy snapshot, never callback claims.
type ShareOrigin struct {
	ActorID   int64
	Address   TopicAddress
	MessageID int
	GrantID   string
	Revision  uint64
	Key       Key
	ProcessID string
}

type AccessDecision struct {
	Allowed       bool
	Reason        AccessReason
	HistoryCursor uint64
}

func AuthorizeShare(g ShareGrant, m MirrorBinding, o ShareOrigin, current Key, action ShareAction, now time.Time) AccessDecision {
	deny := func(reason AccessReason) AccessDecision { return AccessDecision{Reason: reason} }
	if g.ID == "" || g.Revision == 0 || g.RecipientID <= 0 || (g.Role != ShareRead && g.Role != ShareControl) {
		return deny(AccessInvalid)
	}
	if g.State != GrantActive {
		return deny(AccessInactive)
	}
	if !g.ExpiresAt.IsZero() && !now.Before(g.ExpiresAt) {
		return deny(AccessExpired)
	}
	if o.ActorID != g.RecipientID {
		return deny(AccessActor)
	}
	if m.GrantID != g.ID || m.Creating || m.Address.ChatID <= 0 || m.Address.ThreadID <= 0 || o.Address != m.Address {
		return deny(AccessDestination)
	}
	if o.GrantID != g.ID || o.Revision != g.Revision {
		return deny(AccessRevision)
	}
	if g.Key.PaneID == "" || g.Key.TerminalID == "" || o.Key != g.Key || current != g.Key {
		return deny(AccessSession)
	}
	if g.Key.SessionDigest == "" && (g.ProcessID == "" || g.ProcessID != o.ProcessID) {
		return deny(AccessSession)
	}
	if !SharePermits(g, action) {
		return deny(AccessCapability)
	}
	return AccessDecision{Allowed: true, Reason: AccessAllowed, HistoryCursor: g.HistoryCursor}
}

func SharePermits(g ShareGrant, action ShareAction) bool {
	if g.Role != ShareRead && g.Role != ShareControl {
		return false
	}
	switch action {
	case ShareOutput, ShareScreen, ShareHistory, ShareOverview, SharePreferences:
		return true
	case SharePrompt, ShareAttachment, ShareDialog, ShareKeys, ShareForward:
		return g.Role == ShareControl
	case ShareGit:
		return g.Role == ShareControl || g.RepositoryRead
	case ShareClose:
		return g.Role == ShareControl && g.CloseAgent
	case ShareFocus:
		return g.Role == ShareControl && g.LocalFocus
	}
	return false
}

// ShareCommandAction rejects owner commands and unknown slash commands.
func ShareCommandAction(c Command) (ShareAction, bool) {
	switch c.Kind {
	case CmdScreen:
		if c.All {
			return ShareHistory, true
		}
		return ShareScreen, true
	case CmdStatus, CmdHelp:
		return ShareOverview, true
	case CmdPrompt:
		return SharePrompt, true
	case CmdKeys, CmdStop, CmdInterrupt:
		return ShareKeys, true
	case CmdForward:
		return ShareForward, true
	case CmdGit:
		return ShareGit, true
	case CmdClose:
		return ShareClose, true
	case CmdFocus:
		return ShareFocus, true
	}
	return "", false
}
