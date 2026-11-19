package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

type DiagnosticEvent uint8

type DiagnosticTool uint8
type DiagnosticCategory uint8
type HumanFailure uint8
type TerminalFailure uint8

const (
	HumanFailureUnknown HumanFailure = iota
	HumanLockHeld
	HumanStartupFailure
	HumanStorageFailure
	HumanUsageFailure
	HumanLogLevelFailure
	HumanDataDirFailure
	HumanRunMarkerFailure
	HumanNotDatabase
)

const (
	TerminalUnknown TerminalFailure = iota
	TerminalDevice
	TerminalIngest
)

const (
	DiagnosticToolUnknown DiagnosticTool = iota
	DiagnosticListChats
	DiagnosticGetChat
	DiagnosticSearchContacts
	DiagnosticDirectChat
	DiagnosticContactChats
)

const (
	DiagnosticCategoryUnknown DiagnosticCategory = iota
	DiagnosticInvalidInput
	DiagnosticNotFound
	DiagnosticBusy
	DiagnosticUnavailable
	DiagnosticResourceLimit
	DiagnosticInternal
)

const (
	DiagnosticUnknown DiagnosticEvent = iota
	DiagnosticHistoryIncomplete
	DiagnosticReceiveOverload
	DiagnosticDenied
	DiagnosticFailed
	DiagnosticTerminal
	DiagnosticHumanFailure
	DiagnosticDeliveryFailed
	DiagnosticIngestFailed
	DiagnosticMessagesDropped
	DiagnosticQuietStopFailed
	DiagnosticOwnerDrainFailed
	DiagnosticRunGuardRecoveryRequired
	DiagnosticRunGuardClearFailed
	DiagnosticRevocationCleanupFailed
	DiagnosticRepairIncomplete
	DiagnosticRepairBeginFailed
	DiagnosticRepairClearRejected
	DiagnosticRepairReplayFailed
	DiagnosticLIDMergeFailed
	DiagnosticVersionRefreshFailed
	DiagnosticReconnectDenied
	DiagnosticConnectTimeout
	DiagnosticRegistrationDecodeFailed
	DiagnosticRegistrationEncodeFailed
	diagnosticEventCount
)

var diagnosticNames = [...]string{"warning", "history_incomplete", "receive_overload", "denied", "failed", "terminal_fault", "human_failure", "diagnostic_delivery_failed", "ingest_failed", "messages_dropped", "quiet_stop_failed", "owner_drain_failed", "run_guard_recovery_required", "run_guard_clear_failed", "revocation_cleanup_failed", "repair_incomplete", "repair_begin_failed", "repair_clear_rejected", "repair_replay_failed", "lid_merge_failed", "version_refresh_failed", "reconnect_denied", "connect_timeout", "registration_decode_failed", "registration_encode_failed"}

type diagnosticRecord struct {
	data     []byte
	event    DiagnosticEvent
	deadline time.Time
}

type diagnosticAck struct {
	err error
	at  time.Time
}

type diagnosticCell struct {
	count   uint64
	pending uint64
	last    time.Time
}

// Diagnostics never waits for the inherited writer, including during Finish.
type Diagnostics struct {
	mu                           sync.Mutex
	ordinary                     []diagnosticRecord
	mandatory                    []diagnosticRecord
	current                      *diagnosticRecord
	cells                        [diagnosticEventCount]diagnosticCell
	dropped                      uint64
	failed, sinkFailed, finished bool
	wake                         chan struct{}
	failure                      chan struct{}
	changed                      chan struct{}
	completed                    chan diagnosticAck
	level                        slog.LevelVar
	draining                     bool
	drained                      chan struct{}
}

func NewDiagnostics(w io.Writer, level slog.Level) *Diagnostics {
	d := &Diagnostics{wake: make(chan struct{}, 1), failure: make(chan struct{}), changed: make(chan struct{}, 1), completed: make(chan diagnosticAck, 1), drained: make(chan struct{})}
	d.SetLevel(level)
	jobs := make(chan []byte, 1)
	completed, changed := d.completed, d.changed
	go func() {
		for p := range jobs {
			var result error
			for len(p) != 0 {
				n, err := w.Write(p)
				if n < 0 || n > len(p) {
					err = io.ErrShortWrite
					n = 0
				}
				p = p[n:]
				if err != nil {
					result = err
					break
				}
				if n == 0 {
					result = io.ErrShortWrite
					break
				}
			}
			completed <- diagnosticAck{err: result, at: time.Now()}
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}()
	go d.run(jobs)
	return d
}

func (d *Diagnostics) SetLevel(level slog.Level) { d.level.Set(level) }

func (d *Diagnostics) notify() {
	select {
	case d.changed <- struct{}{}:
	default:
	}
}
func (d *Diagnostics) failLocked() {
	if !d.failed {
		d.failed = true
		if d.failure != nil {
			close(d.failure)
		}
		cell := &d.cells[DiagnosticDeliveryFailed]
		cell.count++
		cell.last = time.Now().UTC()
		select {
		case d.wake <- struct{}{}:
		default:
		}
	}
}

func (d *Diagnostics) Report(event DiagnosticEvent) bool {
	return d.report(event, "")
}

func (d *Diagnostics) report(event DiagnosticEvent, metadata string) bool {
	if event <= DiagnosticUnknown || event == DiagnosticDeliveryFailed || event >= diagnosticEventCount {
		event = DiagnosticUnknown
	}
	now := time.Now().UTC()
	outcome := "failed"
	if event == DiagnosticDenied {
		outcome = "denied"
	}
	if event == DiagnosticHistoryIncomplete || event == DiagnosticReceiveOverload || event == DiagnosticMessagesDropped || event == DiagnosticIngestFailed {
		metadata += " messages_may_be_missing=true"
	}
	record := diagnosticRecord{event: event, deadline: now.Add(5 * time.Second), data: []byte(fmt.Sprintf("time=%s event=%s outcome=%s%s\n", now.Format(time.RFC3339Nano), diagnosticNames[event], outcome, metadata))}
	d.mu.Lock()
	cell := &d.cells[event]
	cell.count++
	cell.last = now
	cell.pending++
	if d.finished || d.sinkFailed || len(d.mandatory) == 16 || len(record.data) > 1024 {
		d.failLocked()
		d.mu.Unlock()
		d.notify()
		return false
	}
	d.mandatory = append(d.mandatory, record)
	d.mu.Unlock()
	d.notify()
	return true
}

func (d *Diagnostics) ReportAudit(event DiagnosticEvent, tool DiagnosticTool, category DiagnosticCategory) bool {
	if event != DiagnosticDenied && event != DiagnosticFailed {
		event = DiagnosticFailed
	}
	tools := [...]string{"unknown", "list_chats", "get_chat", "search_contacts", "get_direct_chat_by_contact", "get_contact_chats"}
	categories := [...]string{"unknown", "invalid_input", "not_found", "busy", "unavailable", "resource_limit", "internal"}
	if int(tool) >= len(tools) {
		tool = DiagnosticToolUnknown
	}
	if int(category) >= len(categories) {
		category = DiagnosticCategoryUnknown
	}
	return d.report(event, " tool="+tools[tool]+" category="+categories[category])
}

// GuardDenied admits only fixed guard categories and never waits for the sink.
func (d *Diagnostics) GuardDenied(denial GuardDenial) bool {
	return d.GuardDeniedForTool(denial, DiagnosticToolUnknown)
}

// GuardDeniedForTool accepts only a bounded tool identity, never a client name.
func (d *Diagnostics) GuardDeniedForTool(denial GuardDenial, tool DiagnosticTool) bool {
	event, category := DiagnosticDenied, DiagnosticCategoryUnknown
	switch denial {
	case GuardInvalidParams:
		category = DiagnosticInvalidInput
	case GuardBusy:
		category = DiagnosticBusy
	case GuardUnsupported:
		category = DiagnosticUnavailable
	case GuardSDKToolFailure:
		event, category = DiagnosticFailed, DiagnosticInternal
	default:
		event = DiagnosticFailed
	}
	return d.ReportAudit(event, tool, category)
}

func (d *Diagnostics) ReportHuman(kind HumanFailure, lockPID int) bool {
	reasons := [...]string{"unknown", "lock_held", "startup_failure", "storage_failure", "invalid_arguments", "invalid_configuration", "data_directory", "unsafe_run", "not_database"}
	if int(kind) >= len(reasons) {
		kind = HumanFailureUnknown
	}
	metadata := " reason=" + reasons[kind]
	if kind == HumanLockHeld && lockPID > 0 {
		metadata += " pid=" + strconv.Itoa(lockPID)
	}
	var hint string
	switch kind {
	case HumanLockHeld:
		hint = "stop the lock holder or use a different data directory"
	case HumanStorageFailure:
		hint = "message storage failed; messages may be missing. Fix the cause and start again."
	case HumanUsageFailure:
		hint = "check stdio arguments with whatsapp-mcp stdio --help"
	case HumanLogLevelFailure:
		hint = "invalid configuration: --log-level must be debug, info, warn or error"
	case HumanDataDirFailure:
		hint = "check --data-dir and its permissions"
	case HumanRunMarkerFailure:
		hint = "offline recovery required; use pair to begin recovery"
	case HumanNotDatabase:
		hint = "whatsapp.db is not a usable SQLite database; keep it for offline recovery or use a different data directory"
	}
	if hint != "" {
		metadata += " hint=" + strconv.Quote(hint)
	}
	return d.report(DiagnosticHumanFailure, metadata)
}

func (d *Diagnostics) ReportNewerSchema(device bool, observed, supported int) bool {
	schema := "message schema version"
	if device {
		schema = "device schema compatible from version"
	}
	hint := fmt.Sprintf("%s %d is newer than this binary supports (%d); use a newer whatsapp-mcp", schema, observed, supported)
	return d.report(DiagnosticHumanFailure, " reason=schema_too_new hint="+strconv.Quote(hint))
}

// ReportPrivateDirectory accepts only operator-local path information and a
// permission remedy generated by the private-directory checker.
func (d *Diagnostics) ReportPrivateDirectory(path, fix string) bool {
	if len(path)+len(fix) <= 650 && !strings.ContainsAny(path, "%!") && strings.IndexFunc(path, unicode.IsControl) < 0 &&
		(strings.HasPrefix(fix, "run: chmod 700 ") || strings.HasPrefix(fix, "run: icacls ")) {
		meta := " reason=data_directory path=" + strconv.QuoteToASCII(path) + " hint=" + strconv.QuoteToASCII(fix)
		if len(meta) <= 900 {
			return d.report(DiagnosticHumanFailure, meta)
		}
	}
	hint := "replace '<data-directory>' with the shell-quoted --data-dir path: chmod 700 '<data-directory>'"
	if strings.HasPrefix(fix, "run: icacls ") {
		hint = `In cmd.exe replace <data-directory>, <user-SID> and each <untrusted-SID>: icacls "<data-directory>" /inheritance:r /grant:r "*<user-SID>:(OI)(CI)F" /grant:r "*S-1-5-18:(OI)(CI)F" /grant:r "*S-1-5-32-544:(OI)(CI)F" /remove:g *<untrusted-SID>. Repeat /remove:g for each untrusted grant; for each untrusted deny run icacls "<data-directory>" /remove:d *<untrusted-SID>. Inspect the ACL.`
	}
	return d.report(DiagnosticHumanFailure, " reason=data_directory hint="+strconv.Quote(hint))
}

func (d *Diagnostics) ReportTerminal(kind TerminalFailure) bool {
	components := [...]string{"unknown", "device", "ingest"}
	if int(kind) >= len(components) {
		kind = TerminalUnknown
	}
	return d.report(DiagnosticTerminal, " component="+components[kind]+" tool=not_applicable category="+components[kind]+"_failure")
}

func (d *Diagnostics) ReportTransportFailure() bool {
	return d.report(DiagnosticFailed, " component=mcp tool=not_applicable category=transport_failure")
}

func (d *Diagnostics) ordinaryRecord(r slog.Record, attrs []string) {
	message := "ordinary diagnostic"
	switch r.Message {
	case "message rejected", "message received", "ingest stopped", "identity mapping changed; earlier merges are kept",
		"startup identity reconciliation finished by the session", "pairing requested", "pairing ended",
		"connection confirmed", "logged out by the server", "quiet stop passed", "runtime sample",
		"lock acquired", "identity reconciliation", "shutting down", "first stop signal received; continuing orderly shutdown", "stopped",
		"config", "stopped. The saved session can be reused.":
		message = r.Message
	}
	line := "time=" + r.Time.UTC().Format(time.RFC3339Nano) + " level=" + r.Level.String() + " msg=" + strconv.QuoteToASCII(message)
	if len(line) > 4095 {
		line = "level=" + r.Level.String() + " msg=truncated"
	}
	for _, attr := range attrs {
		if len(line)+1+len(attr) > 4095 {
			break
		}
		line += " " + attr
	}
	data := []byte(line + "\n")
	d.mu.Lock()
	if d.finished || d.draining || d.sinkFailed || len(d.ordinary) == 16 {
		d.dropped++
	} else {
		d.ordinary = append(d.ordinary, diagnosticRecord{data: data})
	}
	d.mu.Unlock()
	d.notify()
}

func (d *Diagnostics) acknowledgeLocked(err error, at time.Time) {
	if !d.current.deadline.IsZero() && !at.Before(d.current.deadline) {
		d.failLocked()
	}
	if err != nil {
		d.sinkFailed = true
		if !d.current.deadline.IsZero() || len(d.mandatory) > 0 {
			d.failLocked()
		}
	} else if !d.current.deadline.IsZero() {
		d.cells[d.current.event].pending--
	}
	d.current = nil
}

func (d *Diagnostics) consumeCompletionLocked() {
	if d.current == nil {
		return
	}
	select {
	case result := <-d.completed:
		d.acknowledgeLocked(result.err, result.at)
	default:
	}
}

func (d *Diagnostics) run(jobs chan<- []byte) {
	defer close(jobs)
	if d.drained != nil {
		defer close(d.drained)
	}
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		d.mu.Lock()
		d.consumeCompletionLocked()
		if d.finished {
			d.mu.Unlock()
			return
		}
		if d.draining && (d.failed || (len(d.mandatory) == 0 && (d.current == nil || d.current.deadline.IsZero()))) {
			d.finished = true
			d.mu.Unlock()
			return
		}
		if d.current == nil && !d.sinkFailed {
			if len(d.mandatory) > 0 {
				r := d.mandatory[0]
				d.mandatory[0] = diagnosticRecord{}
				d.mandatory = d.mandatory[1:]
				d.current = &r
			} else if !d.draining && len(d.ordinary) > 0 {
				r := d.ordinary[0]
				d.ordinary[0] = diagnosticRecord{}
				d.ordinary = d.ordinary[1:]
				d.current = &r
			}
			if d.current != nil {
				jobs <- d.current.data
			}
		}
		var earliest time.Time
		if d.current != nil && !d.current.deadline.IsZero() {
			earliest = d.current.deadline
		}
		for _, r := range d.mandatory {
			if earliest.IsZero() || r.deadline.Before(earliest) {
				earliest = r.deadline
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		var timeout <-chan time.Time
		if !earliest.IsZero() && !d.failed {
			timer.Reset(time.Until(earliest))
			timeout = timer.C
		}
		d.mu.Unlock()
		select {
		case <-d.changed:
		case <-timeout:
			d.mu.Lock()
			d.consumeCompletionLocked()
			now := time.Now()
			if d.current != nil && !d.current.deadline.IsZero() && !now.Before(d.current.deadline) {
				d.failLocked()
			}
			for _, r := range d.mandatory {
				if !now.Before(r.deadline) {
					d.failLocked()
					break
				}
			}
			d.mu.Unlock()
		}
	}
}

func (d *Diagnostics) Failed() bool          { d.mu.Lock(); defer d.mu.Unlock(); return d.failed }
func (d *Diagnostics) Wake() <-chan struct{} { return d.wake }

// Finalize allows only the existing mandatory deadlines after command cleanup.
// It never joins the sink pump or waits for an ordinary record.
func (d *Diagnostics) Finalize(started func(pending bool)) bool {
	d.mu.Lock()
	d.consumeCompletionLocked()
	pending := !d.failed && (len(d.mandatory) != 0 || (d.current != nil && !d.current.deadline.IsZero()))
	d.draining = true
	d.ordinary = nil
	d.mu.Unlock()
	d.notify()
	if started != nil {
		started(pending)
	}
	<-d.drained
	return !d.Failed()
}

func (d *Diagnostics) Finish() bool {
	d.mu.Lock()
	d.consumeCompletionLocked()
	for i := range d.cells {
		if d.cells[i].pending != 0 {
			d.failLocked()
			break
		}
	}
	d.finished = true
	ok := !d.failed
	d.mu.Unlock()
	d.notify()
	return ok
}

type diagnosticHandler struct {
	d     *Diagnostics
	event DiagnosticEvent
	group string
	attrs []string
}

func (d *Diagnostics) Handler() slog.Handler { return diagnosticHandler{d: d} }
func (h diagnosticHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn || level >= h.d.level.Level()
}
func diagnosticEventAttr(a slog.Attr, fallback DiagnosticEvent) DiagnosticEvent {
	if a.Key == "event" && a.Value.Kind() == slog.KindString {
		for event := DiagnosticHistoryIncomplete; event < diagnosticEventCount; event++ {
			if (event <= DiagnosticReceiveOverload || event >= DiagnosticIngestFailed) && a.Value.String() == diagnosticNames[event] {
				return event
			}
		}
	}
	return fallback
}

func diagnosticAttrs(dst []string, group string, attrs []slog.Attr, depth int) []string {
	if depth > 4 {
		return dst
	}
	for _, a := range attrs {
		if len(dst) >= 32 {
			break
		}
		if a.Value.Kind() == slog.KindGroup {
			dst = diagnosticAttrs(dst, "group", a.Value.Group(), depth+1)
			continue
		}
		key := a.Key
		var value string
		switch key {
		case "merged", "failed", "skipped":
			if a.Value.Kind() != slog.KindInt64 {
				continue
			}
			value = strconv.FormatInt(a.Value.Int64(), 10)
		case "status":
			if a.Value.Kind() != slog.KindString {
				continue
			}
			switch a.Value.String() {
			case "completed", "incomplete", "budget_exhausted":
				value = a.Value.String()
			default:
				continue
			}
		case "event":
			if a.Value.Kind() != slog.KindString {
				continue
			}
			switch a.Value.String() {
			case "message_rejected", "message_received", "ingest_stopped", "lid_reassigned", "lid_reconcile", "pairing_requested", "pairing_ended", "connection_confirmed", "logged_out", "quiet_stop_passed", "runtime_sample", "owner_running":
				value = a.Value.String()
			default:
				continue
			}
		default:
			continue
		}
		if group != "" {
			key = "group." + key
		}
		dst = append(dst, key+"="+value)
	}
	return dst
}

func (h diagnosticHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		event := h.event
		r.Attrs(func(a slog.Attr) bool {
			event = diagnosticEventAttr(a, event)
			return true
		})
		h.d.Report(event)
	} else if r.Level >= h.d.level.Level() {
		attrs := append([]string(nil), h.attrs...)
		r.Attrs(func(a slog.Attr) bool {
			attrs = diagnosticAttrs(attrs, h.group, []slog.Attr{a}, 0)
			return len(attrs) < 32
		})
		h.d.ordinaryRecord(r, attrs)
	}
	return nil
}
func (h diagnosticHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append([]string(nil), h.attrs...)
	h.attrs = diagnosticAttrs(h.attrs, h.group, attrs, 0)
	for _, a := range attrs {
		h.event = diagnosticEventAttr(a, h.event)
	}
	return h
}
func (h diagnosticHandler) WithGroup(name string) slog.Handler {
	if name == "" || len(h.group) >= 256 {
		return h
	}
	h.group = "group"
	return h
}
