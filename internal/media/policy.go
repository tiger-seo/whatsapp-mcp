package media

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"go.mau.fi/whatsmeow/types"
)

// AllowAny is the allow-list entry that permits every recipient. It is valid only as the sole entry.
const AllowAny = "*"

var (
	// ErrNotPermitted refuses a recipient that the allow-list does not name.
	ErrNotPermitted = errors.New("the recipient is not permitted")
	// ErrAliases means the local LID mapping could not be read, so nothing is sent to a guessed address.
	ErrAliases = errors.New("the local contact mapping could not be read")
)

// Aliases is the local LID map, in both directions. Both calls only read.
type Aliases interface {
	PhoneForLID(ctx context.Context, lidJID string) (string, bool, error)
	LIDsForPhone(ctx context.Context, phoneJID string) ([]string, error)
}

// NormalizeAllowList normalizes the entries of the recipient allow-list. An empty list stays empty, which
// permits nothing. An error names the position of the bad entry, never its value.
func NormalizeAllowList(entries []string) ([]string, error) {
	if len(entries) == 1 && entries[0] == AllowAny {
		return []string{AllowAny}, nil
	}
	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		if entry == AllowAny {
			return nil, errors.New(`"*" is accepted only as the sole entry`)
		}
		r, err := ParseRecipient(entry)
		if err != nil {
			return nil, fmt.Errorf("entry %d %w", i+1, err)
		}
		if !slices.Contains(out, r.String()) {
			out = append(out, r.String())
		}
	}
	return out, nil
}

// Policy is the owner's recipient allow-list. The zero value permits nothing.
type Policy struct {
	any     bool
	allowed map[string]bool
}

// NewPolicy builds the policy from the configured entries.
func NewPolicy(entries []string) (Policy, error) {
	normalized, err := NormalizeAllowList(entries)
	if err != nil {
		return Policy{}, err
	}
	p := Policy{allowed: make(map[string]bool, len(normalized))}
	for _, entry := range normalized {
		if entry == AllowAny {
			p.any = true
			continue
		}
		p.allowed[entry] = true
	}
	return p, nil
}

// Enabled reports whether any send can be permitted.
func (p Policy) Enabled() bool { return p.any || len(p.allowed) > 0 }

// Resolve decides whether r may be sent to and returns the address the send goes to: a LID with a known phone
// mapping goes to the phone address. A LID and the phone address it maps to count as the same recipient in both
// directions; an alias the local map does not hold is never guessed.
func (p Policy) Resolve(ctx context.Context, aliases Aliases, r Recipient) (Recipient, error) {
	if !p.Enabled() {
		return Recipient{}, ErrNotPermitted
	}
	given, target := r.String(), r
	switch r.JID.Server {
	case types.HiddenUserServer:
		phone, ok, err := aliases.PhoneForLID(ctx, given)
		if err != nil {
			return Recipient{}, errors.Join(ErrAliases, err)
		}
		if ok {
			mapped, err := ParseRecipient(phone)
			if err != nil {
				return Recipient{}, errors.Join(ErrAliases, err)
			}
			target = mapped
		}
	case types.DefaultUserServer:
		if p.any || p.allowed[given] {
			return r, nil
		}
		lids, err := aliases.LIDsForPhone(ctx, given)
		if err != nil {
			return Recipient{}, errors.Join(ErrAliases, err)
		}
		for _, lid := range lids {
			if p.allowed[lid] {
				return r, nil
			}
		}
	}
	if p.any || p.allowed[given] || p.allowed[target.String()] {
		return target, nil
	}
	return Recipient{}, ErrNotPermitted
}
