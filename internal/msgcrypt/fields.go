package msgcrypt

import "strconv"

// Associated data (AAD) for every sealed column other than messages.body_ciphertext (whose AAD is
// "conversation:<id>", internal/honeypot). Each AAD names the table/column and the row's subject, so a
// ciphertext copied into another row or column fails to open. The app (internal/honeypot) and
// cmd/backfill-encrypt must use exactly these; changing one makes existing rows undecryptable.
//
// Conversation identifiers are bound to the honeypot account rather than the conversation id: the
// blind index and ciphertext are written in the same INSERT that creates the row (the unique index needs
// the blind index at insert time), before the id exists. A Telegram peer ref is only valid for the
// account that saw it, so the account is the natural scope.

// DomainPeerRef is the blind-index domain of conversations.scammer_peer_ref_bidx.
const DomainPeerRef = "peer_ref"

func AADConvPeerRef(accountID int64) []byte    { return []byte("conv_peer_ref:acct:" + itoa(accountID)) }
func AADConvHandle(accountID int64) []byte     { return []byte("conv_handle:acct:" + itoa(accountID)) }
func AADPersonaDemoIDs(personaID int64) []byte { return []byte("persona_demo_ids:" + itoa(personaID)) }
func AADConvSummary(conversationID int64) []byte {
	return []byte("conv_summary:" + itoa(conversationID))
}
func AADPersonaSummary(personaID int64) []byte { return []byte("persona_summary:" + itoa(personaID)) }
func AADOverviewSummary() []byte               { return []byte("overview_summary") }

// PeerRefIndex is the blind index stored in conversations.scammer_peer_ref_bidx.
func (b *Box) PeerRefIndex(peerRef string) []byte {
	return b.BlindIndex(DomainPeerRef, []byte(peerRef))
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
