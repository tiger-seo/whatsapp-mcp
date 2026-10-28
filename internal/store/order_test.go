package store

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
)

// orderOp is one write of the fixture: a message or a chat update.
type orderOp struct {
	msg    *Message
	update *ChatUpdate
}

// orderFixture is 50 chats and 2,000 messages with duplicates of identical content, name offers of every quality
// at several times, and chats that have no messages.
func orderFixture() []orderOp {
	rng := rand.New(rand.NewSource(1))
	var ops []orderOp
	chats := make([]string, 50)
	for i := range chats {
		switch {
		case i%5 == 0:
			chats[i] = fmt.Sprintf("12036300000000%04d@g.us", i)
		case i%5 == 1:
			chats[i] = fmt.Sprintf("1000000%05d@lid", i)
		default:
			chats[i] = fmt.Sprintf("4915112%05d@s.whatsapp.net", i)
		}
	}
	var persons []string
	for i, c := range chats[:40] {
		if i%5 != 0 {
			persons = append(persons, c)
		}
	}
	for i := 0; i < 2000; i++ {
		chat := chats[rng.Intn(40)] // the last ten chats never get a message
		m := msg(chat, fmt.Sprintf("m%d", i%1500), alice, at(i%700), withText(fmt.Sprintf("body %d", i%1500)))
		// A duplicate (same chat and ID) must carry identical content; derive everything from the key.
		keyed := rand.New(rand.NewSource(int64(i % 1500)))
		m.ChatJID = chats[keyed.Intn(40)]
		m.SenderJID = persons[keyed.Intn(len(persons))]
		m.Time = at(keyed.Intn(700))
		ops = append(ops, orderOp{msg: &m})
	}
	for i := 0; i < 400; i++ {
		u := ChatUpdate{JID: chats[rng.Intn(50)], LastActivity: at(rng.Intn(900))}
		if rng.Intn(4) != 0 {
			u.Name = fmt.Sprintf("name %d", rng.Intn(30))
			u.NameQuality = NameQuality(1 + rng.Intn(3))
			u.NameTime = at(rng.Intn(100))
		}
		ops = append(ops, orderOp{update: &u})
	}
	return ops
}

// applyShuffled writes the operations in a seeded random order, grouped into batches of random sizes.
func applyShuffled(t *testing.T, st *Store, ops []orderOp, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	ops = append([]orderOp(nil), ops...)
	rng.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] })
	ctx := context.Background()
	for len(ops) > 0 {
		n := min(len(ops), 1+rng.Intn(300))
		var msgs []Message
		var ups []ChatUpdate
		for _, op := range ops[:n] {
			if op.msg != nil {
				msgs = append(msgs, *op.msg)
			} else {
				ups = append(ups, *op.update)
			}
		}
		ops = ops[n:]
		if len(msgs) > 0 {
			if err := st.StoreMessages(ctx, msgs); err != nil {
				t.Fatalf("StoreMessages: %v", err)
			}
		}
		if len(ups) > 0 {
			if err := st.UpdateChats(ctx, ups); err != nil {
				t.Fatalf("UpdateChats: %v", err)
			}
		}
	}
}

// TestStoredStateDoesNotDependOnOrder stores one fixture in 20 random orders and batch splits, each into a fresh
// store, and requires identical tables every time.
func TestStoredStateDoesNotDependOnOrder(t *testing.T) {
	ops := orderFixture()
	var want string
	for seed := int64(1); seed <= 20; seed++ {
		st, _ := openTest(t, newDataDir(t))
		applyShuffled(t, st, ops, seed)
		got := tableDump(t, st)
		if seed == 1 {
			want = got
			if countRows(t, st, "messages") != 1500 || countRows(t, st, "chats") != 50 {
				t.Fatalf("fixture shape: %d messages, %d chats", countRows(t, st, "messages"), countRows(t, st, "chats"))
			}
			continue
		}
		if got != want {
			t.Fatalf("order %d produced a different state", seed)
		}
		st.Close(context.Background())
	}
}
