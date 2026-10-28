package store

import (
	"errors"
	"strings"
	"testing"
)

func TestErrorMapping(t *testing.T) {
	var ve *ValidationError
	err := invalid("text", "too long")
	if !errors.Is(err, ErrInvalid) || !errors.As(err, &ve) || ve.Field != "text" {
		t.Errorf("validation error does not match: %v", err)
	}
	nf := error(&NotFoundError{ChatJID: "1@s.whatsapp.net", ID: "X"})
	if !errors.Is(nf, ErrNotFound) {
		t.Error("NotFoundError is not ErrNotFound")
	}
	for _, c := range []struct {
		err  error
		want error
	}{
		{&SchemaError{Schema: "messages", Observed: 7, Supported: 5, sentinel: ErrSchemaTooNew}, ErrSchemaTooNew},
		{&SchemaError{Schema: "device", sentinel: ErrSchemaInvalid}, ErrSchemaInvalid},
		{unsafeStorage("/x/whatsapp.db", "unsafe_artifact", "bad", ""), ErrUnsafeStorage},
		{notDatabase("/x/whatsapp.db"), ErrNotDatabase},
		{ioError("disk_full"), ErrIO},
		{limitErr("memory"), ErrResourceLimit},
		{&DiskLowError{Free: 1, Reserve: 2}, ErrDiskLow},
	} {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%T does not match %v", c.err, c.want)
		}
	}
}

func TestMapDriverErrorKeepsCodesNotText(t *testing.T) {
	// A foreign error with secret text maps to a generic I/O error that does not repeat it.
	err := mapDriverError(errors.New("SECRET-BODY-7f3a near 'INSERT INTO'"))
	if !errors.Is(err, ErrIO) || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("mapDriverError = %v, want a generic ErrIO", err)
	}
	if got := mapDriverError(ErrBusy); got != ErrBusy {
		t.Errorf("a classified error changed: %v", got)
	}
	if mapDriverError(nil) != nil {
		t.Error("nil mapped to non-nil")
	}
}

func TestErrorTextHasNoInputs(t *testing.T) {
	e := invalid("chat_jid", "malformed JID")
	if strings.Contains(e.Error(), "@") {
		t.Errorf("error text %q repeats input", e)
	}
	d := &DiskLowError{Free: 10, Reserve: 20}
	if !strings.Contains(d.Error(), "10") {
		t.Errorf("DiskLowError text = %q", d)
	}
}
