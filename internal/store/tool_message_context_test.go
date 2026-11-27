package store

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func TestMessageContextLookup(t *testing.T) {
	f := newToolMessageFixture(t)
	accept := func(MessageView) (bool, error) { return true, nil }
	got, err := f.reader.GetMessageContext(t.Context(), "", "direct", Context{}, accept)
	if err != nil || got.Message.ChatJID != f.phone || got.Before == nil || got.After == nil || len(got.Before)+len(got.After) != 0 {
		t.Fatalf("unique: %+v %v", got, err)
	}
	for _, chat := range []string{"", groupOne} {
		_, err := f.reader.GetMessageContext(t.Context(), chat, "direct-missing", Context{}, accept)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
	}
	if _, err := f.reader.GetMessageContext(t.Context(), groupOne, "direct", Context{}, accept); !errors.Is(err, ErrNotFound) {
		t.Fatalf("exact lookup fell back: %v", err)
	}
	for i := 0; i < 101; i++ {
		chat := fmt.Sprintf("%d@g.us", 100000+i)
		mustStore(t, f.store, msg(chat, "shared", f.phone, at(10)))
		if i != 1 && i != 99 && i != 100 {
			continue
		}
		got, err := f.reader.GetMessageContext(t.Context(), "", "shared", Context{}, accept)
		if !reflect.DeepEqual(got, MessageContextResult{}) {
			t.Fatalf("ambiguous lookup returned data: %+v", got)
		}
		if i == 100 {
			var limit *ResourceLimitError
			var ambiguous *AmbiguousIDError
			if !errors.As(err, &limit) || limit.Limit != "matching chats" || errors.As(err, &ambiguous) {
				t.Fatalf("101 chats: %v", err)
			}
			continue
		}
		var ambiguous *AmbiguousIDError
		if !errors.As(err, &ambiguous) || len(ambiguous.ChatJIDs) != i+1 || !slices.IsSorted(ambiguous.ChatJIDs) {
			t.Fatalf("%d chats: %v", i+1, err)
		}
		for j, chat := range ambiguous.ChatJIDs {
			if chat != fmt.Sprintf("%d@g.us", 100000+j) {
				t.Fatalf("missing or unexpected chat: %v", ambiguous.ChatJIDs)
			}
		}
	}
}

func TestMessageContextNeighbours(t *testing.T) {
	f := newToolMessageFixture(t)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		mustStore(t, f.store, msg(f.phone, id, f.phone, at(10)))
	}
	accept := func(MessageView) (bool, error) { return true, nil }
	ids := func(rows []MessageView) []string {
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.ID)
		}
		return out
	}
	for _, tc := range []struct {
		id            string
		counts        Context
		before, after []string
	}{
		{"c", Context{Before: 1, After: 2}, []string{"b"}, []string{"d", "e"}},
		{"c", Context{}, []string{}, []string{}},
		{"a", Context{Before: 2, After: 1}, []string{"direct"}, []string{"b"}},
		{"e", Context{Before: 1, After: 2}, []string{"d"}, []string{}},
	} {
		got, err := f.reader.GetMessageContext(t.Context(), f.phone, tc.id, tc.counts, accept)
		if err != nil || !reflect.DeepEqual(ids(got.Before), tc.before) || !reflect.DeepEqual(ids(got.After), tc.after) || got.BeforeReduced || got.AfterReduced {
			t.Errorf("%s %+v: %+v %v", tc.id, tc.counts, got, err)
		}
	}
}
