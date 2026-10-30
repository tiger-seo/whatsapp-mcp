package store

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// Page selects a slice of a listing: Limit 1 to 200 rows from Offset 0 to 100,000.
type Page struct{ Limit, Offset int }

// ChatSort is the order of a chat listing.
type ChatSort int

const (
	ByLastActivity ChatSort = iota // newest activity first, chats without activity last
	ByName                         // by name ascending, unnamed chats last
)

// chatOrder holds the exact ORDER BY expressions that chats_last_activity and chats_name are built on.
var chatOrder = map[ChatSort]string{
	ByLastActivity: "last_message_time IS NULL, last_message_time DESC, jid",
	ByName:         "name_quality = 0, name_fold, jid",
}

// MessageView is a stored message as the tools show it.
type MessageView struct {
	ID, ChatJID, ChatName              string
	SenderJID, SenderPhone, SenderName string
	Time                               time.Time
	FromMe                             bool
	Text, Caption, MediaType           string
}

// ChatView is a chat as the tools show it.
type ChatView struct {
	JID, Name    string
	IsGroup      bool
	LastActivity *time.Time   // nil = none
	Latest       *MessageView // nil when not requested or the chat has no messages
}

// Contact is one person from the device store, under the phone-number JID when it is known.
type Contact struct{ JID, Phone, Name string }

// MessageFilter narrows a message listing. Every field is optional.
type MessageFilter struct {
	After, Before time.Time // zero = unset; both strict
	ChatJID       string
	Sender        string // a contact; "" = unset
	Query         string // matches text or caption
}

// Context asks for up to Before earlier and After later messages of the same chat, 0 to 20 each.
type Context struct{ Before, After int }

// Hit is a listed message with its context, both sides oldest first.
type Hit struct {
	Message       MessageView
	Before, After []MessageView
}

// contactIDs is a contact under both identities; either may be empty, not both.
type contactIDs struct{ phone, phoneJID, lidJID string }

// querier is what a read transaction and a bare connection have in common.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// snapshot runs fn on one reader connection inside one read transaction, so a listing sees one state of the file
// even while the writer commits.
func (s *Store) snapshot(ctx context.Context, name string, fn func(ctx context.Context, q querier) error) error {
	return s.read(ctx, name, func(ctx context.Context, conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var q querier = tx
		if s.hooks.wrapReader != nil {
			q = s.hooks.wrapReader(tx)
		}
		return fn(ctx, q)
	})
}

func validatePage(p Page) error {
	if p.Limit < 1 || p.Limit > maxListSize {
		return invalid("limit", "must be 1 to 200")
	}
	if p.Offset < 0 || p.Offset > maxOffset {
		return invalid("offset", "must be 0 to 100000")
	}
	return nil
}

func validateContext(c Context) error {
	if c.Before < 0 || c.Before > maxContextSide {
		return invalid("context_before", "must be 0 to 20")
	}
	if c.After < 0 || c.After > maxContextSide {
		return invalid("context_after", "must be 0 to 20")
	}
	return nil
}

// resultBudget adds up the bytes of the rows of one result.
type resultBudget struct{ used int }

func (b *resultBudget) add(n int) error {
	if b.used += n; b.used > maxResultBytes {
		return limitErr("result bytes")
	}
	return nil
}

func (b *resultBudget) addMessage(m MessageView) error {
	return b.add(len(m.ID) + len(m.ChatJID) + len(m.ChatName) + len(m.SenderJID) + len(m.SenderPhone) +
		len(m.SenderName) + len(m.Text) + len(m.Caption) + len(m.MediaType) + 64)
}

func (b *resultBudget) addMessages(ms []MessageView) error {
	for _, m := range ms {
		if err := b.addMessage(m); err != nil {
			return err
		}
	}
	return nil
}

// contactName is a device-store contact's name in the order full, first, push, business; NULL when it has none.
const contactName = `coalesce(nullif(full_name, ''), nullif(first_name, ''), nullif(push_name, ''), nullif(business_name, ''))`

// currentDevice is the JID of the single device row; NULL, and so no contact, when there is none.
const currentDevice = `(SELECT jid FROM whatsmeow_device LIMIT 1)`

// contactNameOf is the name of the current device's contact whose their_jid is the given expression.
func contactNameOf(theirJID string) string {
	return `(SELECT ` + contactName + ` FROM whatsmeow_contacts WHERE our_jid = ` + currentDevice +
		` AND their_jid = ` + theirJID + `)`
}

// counterpartJID is the other identity of m's sender through the LID map. The map's key is searched, never
// computed, and only one side is looked up for one sender.
const counterpartJID = `CASE WHEN m.sender_phone <> '' THEN ` +
	`(SELECT lid || '@lid' FROM whatsmeow_lid_map WHERE pn = m.sender_phone) ` +
	`ELSE (SELECT pn || '@s.whatsapp.net' FROM whatsmeow_lid_map WHERE lid = substr(m.sender_jid, 1, length(m.sender_jid) - 4)) END`

// messageSelect reads a message with its chat name and sender name.
var messageSelect = `SELECT m.id, m.chat_jid, c.name, m.sender_jid, m.sender_phone, ` +
	`CASE WHEN m.from_me = 1 THEN '' ELSE coalesce(` +
	contactNameOf(`m.sender_jid`) + `, ` +
	contactNameOf(`(`+counterpartJID+`)`) + `, ` +
	`(SELECT name FROM chats WHERE jid = m.sender_jid AND name_quality >= 2), '') END, ` +
	`m.time, m.from_me, m.text, m.caption, m.media_type ` +
	`FROM messages m JOIN chats c ON c.jid = m.chat_jid `

type rowScanner interface{ Scan(dest ...any) error }

func scanMessage(r rowScanner) (MessageView, error) {
	var v MessageView
	var ms int64
	var fromMe int
	if err := r.Scan(&v.ID, &v.ChatJID, &v.ChatName, &v.SenderJID, &v.SenderPhone, &v.SenderName,
		&ms, &fromMe, &v.Text, &v.Caption, &v.MediaType); err != nil {
		return MessageView{}, err
	}
	v.Time, v.FromMe = time.UnixMilli(ms).UTC(), fromMe == 1
	return v, nil
}

// messageKey is the position of a message in every listing.
type messageKey struct {
	time    int64
	chatJID string
	id      string
}

// newerThan reports whether k comes before o in a newest-first listing.
func (k messageKey) newerThan(o messageKey) bool {
	if k.time != o.time {
		return k.time > o.time
	}
	if k.chatJID != o.chatJID {
		return k.chatJID > o.chatJID
	}
	return k.id > o.id
}

func getMessage(ctx context.Context, q querier, chatJID, id string) (MessageView, error) {
	v, err := scanMessage(q.QueryRowContext(ctx, messageSelect+`WHERE m.chat_jid = ? AND m.id = ?`, chatJID, id))
	if err == sql.ErrNoRows {
		return MessageView{}, &NotFoundError{ChatJID: chatJID, ID: id}
	}
	return v, err
}

// latestMessage is the newest message of a chat, or nil.
func latestMessage(ctx context.Context, q querier, chatJID string) (*MessageView, error) {
	v, err := scanMessage(q.QueryRowContext(ctx,
		messageSelect+`WHERE m.chat_jid = ? ORDER BY m.time DESC, m.id DESC LIMIT 1`, chatJID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func scanChats(rows *sql.Rows) ([]ChatView, error) {
	defer rows.Close()
	var out []ChatView
	for rows.Next() {
		var c ChatView
		var last sql.NullInt64
		if err := rows.Scan(&c.JID, &c.Name, &last); err != nil {
			return nil, err
		}
		c.IsGroup = strings.HasSuffix(c.JID, "@"+serverGroup)
		if last.Valid {
			t := time.UnixMilli(last.Int64).UTC()
			c.LastActivity = &t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// finishChats counts the chats against the budget and, when asked, fills Latest of each.
func finishChats(ctx context.Context, q querier, chats []ChatView, withLatest bool) error {
	budget := &resultBudget{}
	for i := range chats {
		if err := budget.add(len(chats[i].JID) + len(chats[i].Name) + 64); err != nil {
			return err
		}
		if !withLatest {
			continue
		}
		latest, err := latestMessage(ctx, q, chats[i].JID)
		if err != nil {
			return err
		}
		if latest != nil {
			if err := budget.addMessage(*latest); err != nil {
				return err
			}
		}
		chats[i].Latest = latest
	}
	return nil
}

// ListChats lists chats whose name or JID contains query, in the given order.
func (s *Store) ListChats(ctx context.Context, query string, sort ChatSort, withLatest bool, p Page) ([]ChatView, error) {
	if err := validatePage(p); err != nil {
		return nil, s.report(newOpID(), err)
	}
	order, ok := chatOrder[sort]
	if !ok {
		return nil, s.report(newOpID(), invalid("sort", "not one of last activity, name"))
	}
	if err := validText("query", query, maxSearchBytes); err != nil {
		return nil, s.report(newOpID(), err)
	}
	text := `SELECT jid, name, last_message_time FROM chats `
	var args []any
	if f := fold(query); f != "" {
		text += `WHERE instr(name_fold, ?) > 0 OR instr(jid, ?) > 0 `
		args = append(args, f, f)
	}
	text += `ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	args = append(args, p.Limit, p.Offset)

	var out []ChatView
	err := s.snapshot(ctx, "ListChats", func(ctx context.Context, q querier) error {
		rows, err := q.QueryContext(ctx, text, args...)
		if err != nil {
			return err
		}
		if out, err = scanChats(rows); err != nil {
			return err
		}
		return finishChats(ctx, q, out, withLatest)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func getChat(ctx context.Context, q querier, jid string) (ChatView, error) {
	rows, err := q.QueryContext(ctx, `SELECT jid, name, last_message_time FROM chats WHERE jid = ?`, jid)
	if err != nil {
		return ChatView{}, err
	}
	chats, err := scanChats(rows)
	if err != nil {
		return ChatView{}, err
	}
	if len(chats) == 0 {
		return ChatView{}, &NotFoundError{ChatJID: jid}
	}
	if err := finishChats(ctx, q, chats, true); err != nil {
		return ChatView{}, err
	}
	return chats[0], nil
}

// GetChat returns a chat with its latest message.
func (s *Store) GetChat(ctx context.Context, jid string) (ChatView, error) {
	parsed, err := parseChatJID("chat_jid", jid)
	if err != nil {
		return ChatView{}, s.report(newOpID(), err)
	}
	var out ChatView
	err = s.snapshot(ctx, "GetChat", func(ctx context.Context, q querier) (err error) {
		out, err = getChat(ctx, q, parsed.String())
		return err
	})
	return out, err
}

// DirectChat returns the one-to-one chat with a contact: the phone chat, else the @lid chat.
func (s *Store) DirectChat(ctx context.Context, contact string) (ChatView, error) {
	var out ChatView
	err := s.snapshot(ctx, "DirectChat", func(ctx context.Context, q querier) error {
		ids, err := resolveContact(ctx, q, contact)
		if err != nil {
			return err
		}
		for _, jid := range []string{ids.phoneJID, ids.lidJID} {
			if jid == "" {
				continue
			}
			c, err := getChat(ctx, q, jid)
			if err == nil {
				out = c
				return nil
			}
			if _, missing := err.(*NotFoundError); !missing {
				return err
			}
		}
		return &NotFoundError{ChatJID: firstNonEmpty(ids.phoneJID, ids.lidJID)}
	})
	return out, err
}

// ContactChats lists the chats of a contact: its direct chats and every group it wrote in, under either identity.
// The page's JIDs are selected first, so the walk of chats_last_activity is covering.
func (s *Store) ContactChats(ctx context.Context, contact string, p Page) ([]ChatView, error) {
	if err := validatePage(p); err != nil {
		return nil, s.report(newOpID(), err)
	}
	var out []ChatView
	err := s.snapshot(ctx, "ContactChats", func(ctx context.Context, q querier) error {
		ids, err := resolveContact(ctx, q, contact)
		if err != nil {
			return err
		}
		var direct, probes []string
		var directArgs, probeArgs []any
		if ids.phoneJID != "" {
			direct, directArgs = append(direct, "?"), append(directArgs, ids.phoneJID)
			probes = append(probes, `EXISTS (SELECT 1 FROM messages WHERE chat_jid = chats.jid AND sender_phone = ?)`)
			probeArgs = append(probeArgs, ids.phone)
		}
		if ids.lidJID != "" {
			direct, directArgs = append(direct, "?"), append(directArgs, ids.lidJID)
			probes = append(probes, `EXISTS (SELECT 1 FROM messages WHERE chat_jid = chats.jid AND sender_jid = ?)`)
			probeArgs = append(probeArgs, ids.lidJID)
		}
		keySQL := `SELECT jid FROM chats WHERE jid IN (` + strings.Join(direct, ", ") + `) ` +
			`OR (jid LIKE '%@g.us' AND (` + strings.Join(probes, ` OR `) + `)) ` +
			`ORDER BY ` + chatOrder[ByLastActivity] + ` LIMIT ? OFFSET ?`
		args := append(append(directArgs, probeArgs...), p.Limit, p.Offset)
		rows, err := q.QueryContext(ctx, keySQL, args...)
		if err != nil {
			return err
		}
		var jids []string
		for rows.Next() {
			var jid string
			if err := rows.Scan(&jid); err != nil {
				rows.Close()
				return err
			}
			jids = append(jids, jid)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, jid := range jids {
			c, err := getChat(ctx, q, jid)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		budget := &resultBudget{}
		for _, c := range out {
			if err := budget.add(len(c.JID) + len(c.Name) + 64); err != nil {
				return err
			}
			if c.Latest != nil {
				if err := budget.addMessage(*c.Latest); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// resolveContact reads a contact argument and fills its other identity from the current device's LID map.
// Without a device or a mapping the contact keeps the one identity it was given.
func resolveContact(ctx context.Context, q querier, s string) (contactIDs, error) {
	phone, lid, err := parseContact(s)
	if err != nil {
		return contactIDs{}, err
	}
	var other sql.NullString
	if phone != "" {
		err = q.QueryRowContext(ctx,
			`SELECT lid FROM whatsmeow_lid_map WHERE pn = ? AND EXISTS (SELECT 1 FROM whatsmeow_device)`, phone).Scan(&other)
		lid = other.String
	} else {
		err = q.QueryRowContext(ctx,
			`SELECT pn FROM whatsmeow_lid_map WHERE lid = ? AND EXISTS (SELECT 1 FROM whatsmeow_device)`, lid).Scan(&other)
		phone = other.String
	}
	if err != nil && err != sql.ErrNoRows {
		return contactIDs{}, err
	}
	var ids contactIDs
	if phone != "" {
		ids.phone, ids.phoneJID = phone, phone+"@"+serverUser
	}
	if lid != "" {
		ids.lidJID = lid + "@" + serverLID
	}
	return ids, nil
}

// ownIdentity is the user parts of the current device's JID and LID, without a device part. Either may be empty.
func ownIdentity(ctx context.Context, q querier) (phone, lid string, err error) {
	var jid, lidJID sql.NullString
	err = q.QueryRowContext(ctx, `SELECT jid, lid FROM whatsmeow_device LIMIT 1`).Scan(&jid, &lidJID)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if j, perr := types.ParseJID(jid.String); perr == nil && j.Server == serverUser {
		phone = j.User
	}
	if j, perr := types.ParseJID(lidJID.String); perr == nil && j.Server == serverLID {
		lid = j.User
	}
	return phone, lid, nil
}

// senderBranch is the sender condition for one identity of a contact. A contact with both identities has two,
// joined by UNION ALL and never by OR, so each is a search on its own index.
type senderBranch struct{ column, value string }

func (ids contactIDs) branches() []senderBranch {
	var out []senderBranch
	if ids.phone != "" {
		out = append(out, senderBranch{"sender_phone", ids.phone})
	}
	if ids.lidJID != "" {
		out = append(out, senderBranch{"sender_jid", ids.lidJID})
	}
	return out
}

// messageKeys selects the keys of one page of a filtered listing, newest first.
func messageKeys(ctx context.Context, q querier, f MessageFilter, ids *contactIDs, p Page) ([]messageKey, error) {
	var conds []string
	var condArgs []any
	if !f.After.IsZero() {
		conds, condArgs = append(conds, "time > ?"), append(condArgs, f.After.UnixMilli())
	}
	if !f.Before.IsZero() {
		conds, condArgs = append(conds, "time < ?"), append(condArgs, f.Before.UnixMilli())
	}
	if f.ChatJID != "" {
		conds, condArgs = append(conds, "chat_jid = ?"), append(condArgs, f.ChatJID)
	}
	if t := fold(f.Query); t != "" {
		conds = append(conds, "(instr(text_fold, ?) > 0 OR instr(caption_fold, ?) > 0)")
		condArgs = append(condArgs, t, t)
	}

	var parts []string
	var args []any
	build := func(extra string, extraArg any) {
		c, a := conds, condArgs
		if extra != "" {
			c = append([]string{extra}, conds...)
			a = append([]any{extraArg}, condArgs...)
		}
		text := "SELECT time, chat_jid, id FROM messages"
		if len(c) > 0 {
			text += " WHERE " + strings.Join(c, " AND ")
		}
		parts, args = append(parts, text), append(args, a...)
	}
	if ids == nil {
		build("", nil)
	} else {
		for _, b := range ids.branches() {
			build(b.column+" = ?", b.value)
		}
	}
	text := strings.Join(parts, " UNION ALL ") + " ORDER BY time DESC, chat_jid DESC, id DESC LIMIT ? OFFSET ?"
	rows, err := q.QueryContext(ctx, text, append(args, p.Limit, p.Offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []messageKey
	for rows.Next() {
		var k messageKey
		if err := rows.Scan(&k.time, &k.chatJID, &k.id); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// contextOf reads the messages around a message of the same chat, both sides oldest first. The message's position
// is bound as a row value, so the index seeks straight to it.
func contextOf(ctx context.Context, q querier, k messageKey, c Context) (before, after []MessageView, err error) {
	read := func(op, order string, n int) ([]MessageView, error) {
		if n == 0 {
			return nil, nil
		}
		rows, err := q.QueryContext(ctx, messageSelect+
			`WHERE m.chat_jid = ? AND (m.time, m.id) `+op+` (?, ?) ORDER BY `+order+` LIMIT ?`,
			k.chatJID, k.time, k.id, n)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []MessageView
		for rows.Next() {
			v, err := scanMessage(rows)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, rows.Err()
	}
	if before, err = read("<", "m.time DESC, m.id DESC", c.Before); err != nil {
		return nil, nil, err
	}
	for i, j := 0, len(before)-1; i < j; i, j = i+1, j-1 {
		before[i], before[j] = before[j], before[i]
	}
	after, err = read(">", "m.time, m.id", c.After)
	return before, after, err
}

// hitFor reads a message and its context against the budget.
func hitFor(ctx context.Context, q querier, k messageKey, c Context, b *resultBudget) (Hit, error) {
	m, err := getMessage(ctx, q, k.chatJID, k.id)
	if err != nil {
		return Hit{}, err
	}
	before, after, err := contextOf(ctx, q, k, c)
	if err != nil {
		return Hit{}, err
	}
	for _, group := range [][]MessageView{{m}, before, after} {
		if err := b.addMessages(group); err != nil {
			return Hit{}, err
		}
	}
	return Hit{Message: m, Before: before, After: after}, nil
}

// ListMessages lists messages newest first, by (time, chat, ID), with the requested context around each.
func (s *Store) ListMessages(ctx context.Context, f MessageFilter, c Context, p Page) ([]Hit, error) {
	if err := validatePage(p); err != nil {
		return nil, s.report(newOpID(), err)
	}
	if err := validateContext(c); err != nil {
		return nil, s.report(newOpID(), err)
	}
	if err := validText("query", f.Query, maxSearchBytes); err != nil {
		return nil, s.report(newOpID(), err)
	}
	for _, b := range []struct {
		field string
		t     time.Time
	}{{"after", f.After}, {"before", f.Before}} {
		if b.t.IsZero() {
			continue
		}
		if err := validateTime(b.field, b.t); err != nil {
			return nil, s.report(newOpID(), err)
		}
	}
	if f.ChatJID != "" {
		chat, err := parseChatJID("chat_jid", f.ChatJID)
		if err != nil {
			return nil, s.report(newOpID(), err)
		}
		f.ChatJID = chat.String()
	}
	if !f.After.IsZero() && !f.Before.IsZero() && !f.After.Before(f.Before) {
		return nil, nil
	}

	var out []Hit
	err := s.snapshot(ctx, "ListMessages", func(ctx context.Context, q querier) error {
		var ids *contactIDs
		if f.Sender != "" {
			resolved, err := resolveContact(ctx, q, f.Sender)
			if err != nil {
				return err
			}
			ids = &resolved
		}
		keys, err := messageKeys(ctx, q, f, ids, p)
		if err != nil {
			return err
		}
		if len(keys)*(c.Before+c.After) > maxContextRows {
			return limitErr("context rows")
		}
		budget := &resultBudget{}
		for _, k := range keys {
			h, err := hitFor(ctx, q, k, c, budget)
			if err != nil {
				return err
			}
			out = append(out, h)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MessageContext returns one message with the messages around it in its chat.
func (s *Store) MessageContext(ctx context.Context, chatJID, id string, c Context) (Hit, error) {
	chat, err := parseChatJID("chat_jid", chatJID)
	if err != nil {
		return Hit{}, s.report(newOpID(), err)
	}
	if err := validateID("id", id); err != nil {
		return Hit{}, s.report(newOpID(), err)
	}
	if err := validateContext(c); err != nil {
		return Hit{}, s.report(newOpID(), err)
	}
	var out Hit
	err = s.snapshot(ctx, "MessageContext", func(ctx context.Context, q querier) error {
		var ms int64
		err := q.QueryRowContext(ctx, `SELECT time FROM messages WHERE chat_jid = ? AND id = ?`, chat.String(), id).Scan(&ms)
		if err == sql.ErrNoRows {
			return &NotFoundError{ChatJID: chat.String(), ID: id}
		}
		if err != nil {
			return err
		}
		out, err = hitFor(ctx, q, messageKey{time: ms, chatJID: chat.String(), id: id}, c, &resultBudget{})
		return err
	})
	return out, err
}

// LastInteraction returns the newest message of a contact: in its direct chats (either side) or sent by it in
// any chat, or nil. The newest of up to four single-index lookups is chosen here, never one query over an OR.
func (s *Store) LastInteraction(ctx context.Context, contact string) (*MessageView, error) {
	var out *MessageView
	err := s.snapshot(ctx, "LastInteraction", func(ctx context.Context, q querier) error {
		ids, err := resolveContact(ctx, q, contact)
		if err != nil {
			return err
		}
		ownPhone, ownLID, err := ownIdentity(ctx, q)
		if err != nil {
			return err
		}
		// An empty own value never equals anything, so an unmapped contact is not taken for the account.
		own := (ids.phone != "" && ids.phone == ownPhone) ||
			(ids.lidJID != "" && ownLID != "" && ids.lidJID == ownLID+"@"+serverLID)
		if own {
			// Both of the account's own chats count, whichever way it was named.
			if ownPhone != "" {
				ids.phone, ids.phoneJID = ownPhone, ownPhone+"@"+serverUser
			}
			if ownLID != "" {
				ids.lidJID = ownLID + "@" + serverLID
			}
		}

		var best *messageKey
		consider := func(query string, args ...any) error {
			var k messageKey
			err := q.QueryRowContext(ctx, query, args...).Scan(&k.time, &k.chatJID, &k.id)
			if err == sql.ErrNoRows {
				return nil
			}
			if err != nil {
				return err
			}
			if best == nil || k.newerThan(*best) {
				best = &k
			}
			return nil
		}
		for _, jid := range []string{ids.phoneJID, ids.lidJID} {
			if jid == "" {
				continue
			}
			if err := consider(`SELECT time, chat_jid, id FROM messages WHERE chat_jid = ? `+
				`ORDER BY time DESC, id DESC LIMIT 1`, jid); err != nil {
				return err
			}
		}
		if !own {
			// from_me = 0 costs nothing for another contact and keeps the result right when the own number is unknown.
			for _, b := range ids.branches() {
				if err := consider(`SELECT time, chat_jid, id FROM messages WHERE `+b.column+` = ? AND from_me = 0 `+
					`ORDER BY time DESC, chat_jid DESC, id DESC LIMIT 1`, b.value); err != nil {
					return err
				}
			}
		}
		if best == nil {
			return nil
		}
		v, err := getMessage(ctx, q, best.chatJID, best.id)
		if err != nil {
			return err
		}
		out = &v
		return nil
	})
	return out, err
}

// GetMessage returns one message by chat and ID.
func (s *Store) GetMessage(ctx context.Context, chatJID, id string) (MessageView, error) {
	chat, err := parseChatJID("chat_jid", chatJID)
	if err != nil {
		return MessageView{}, s.report(newOpID(), err)
	}
	if err := validateID("id", id); err != nil {
		return MessageView{}, s.report(newOpID(), err)
	}
	var out MessageView
	err = s.snapshot(ctx, "GetMessage", func(ctx context.Context, q querier) (err error) {
		out, err = getMessage(ctx, q, chat.String(), id)
		return err
	})
	return out, err
}

// GetMessageByID returns the message with an ID that is stored in exactly one chat. An ID in several chats is an
// *AmbiguousIDError listing them sorted, and in more than 100 an ErrResourceLimit, without reading further.
func (s *Store) GetMessageByID(ctx context.Context, id string) (MessageView, error) {
	if err := validateID("id", id); err != nil {
		return MessageView{}, s.report(newOpID(), err)
	}
	var out MessageView
	err := s.snapshot(ctx, "GetMessageByID", func(ctx context.Context, q querier) error {
		rows, err := q.QueryContext(ctx, `SELECT chat_jid FROM messages WHERE id = ? LIMIT ?`, id, maxAmbiguousIDs+1)
		if err != nil {
			return err
		}
		var chats []string
		for rows.Next() {
			var jid string
			if err := rows.Scan(&jid); err != nil {
				rows.Close()
				return err
			}
			chats = append(chats, jid)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		switch {
		case len(chats) == 0:
			return &NotFoundError{ID: id}
		case len(chats) > maxAmbiguousIDs:
			return limitErr("ambiguous ids")
		case len(chats) > 1:
			slices.Sort(chats)
			return &AmbiguousIDError{ID: id, ChatJIDs: chats}
		}
		out, err = getMessage(ctx, q, chats[0], id)
		return err
	})
	return out, err
}

// MediaInfo returns a media message with what a download of its media needs. A message without media is
// ErrNotMedia.
func (s *Store) MediaInfo(ctx context.Context, chatJID, id string) (MessageView, MediaMeta, error) {
	chat, err := parseChatJID("chat_jid", chatJID)
	if err != nil {
		return MessageView{}, MediaMeta{}, s.report(newOpID(), err)
	}
	if err := validateID("id", id); err != nil {
		return MessageView{}, MediaMeta{}, s.report(newOpID(), err)
	}
	var view MessageView
	var meta MediaMeta
	err = s.snapshot(ctx, "MediaInfo", func(ctx context.Context, q querier) error {
		var err error
		if view, err = getMessage(ctx, q, chat.String(), id); err != nil {
			return err
		}
		if view.MediaType == "" {
			return ErrNotMedia
		}
		var url, path, mime, name sql.NullString
		var length sql.NullInt64
		err = q.QueryRowContext(ctx,
			`SELECT media_url, media_direct_path, media_key, file_sha256, file_enc_sha256, file_length, mime_type, file_name `+
				`FROM messages WHERE chat_jid = ? AND id = ?`, chat.String(), id).
			Scan(&url, &path, &meta.MediaKey, &meta.FileSHA256, &meta.FileEncSHA256, &length, &mime, &name)
		if err != nil {
			return err
		}
		meta.URL, meta.DirectPath, meta.MIMEType, meta.FileName = url.String, path.String, mime.String, name.String
		if length.Valid {
			meta.FileLength = &length.Int64
		}
		return nil
	})
	if err != nil {
		return MessageView{}, MediaMeta{}, err
	}
	return view, meta, nil
}
