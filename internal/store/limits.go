package store

import "time"

// The approved storage profile. Every size is in bytes, with KiB = 1024.
const (
	kib = 1 << 10
	mib = 1 << 20
	gib = 1 << 30

	maxJIDBytes      = 256
	maxIDBytes       = 256
	maxNameBytes     = 1 * kib
	maxTextBytes     = 256 * kib
	maxCaptionBytes  = 256 * kib
	maxSearchBytes   = 1 * kib
	maxURLBytes      = 4 * kib
	maxPathBytes     = 4 * kib
	maxMIMEBytes     = 255
	maxFileNameBytes = 1 * kib
	mediaHashBytes   = 32
	maxFileLength    = 4 * gib

	maxBatchMessages = 1000
	maxBatchBytes    = 16 * mib
	maxChatUpdates   = 1000

	maxListSize     = 200
	maxOffset       = 100_000
	maxContextSide  = 20
	maxContextRows  = 2000
	maxResultBytes  = 32 * mib
	maxAmbiguousIDs = 100

	writeConns    = 1
	readConns     = 4
	maxActive     = 16
	maxWaiting    = 64
	deepSlots     = 2
	deepPageEnd   = 10_000
	maxHeapBytes  = 256 * mib
	diskReserve   = 256 * mib
	diskResume    = 512 * mib
	walLimitBytes = 64 * mib
	busyTimeoutMS = 5000

	startupTimeout = 10 * time.Minute
)

// The durations are variables only so that tests can shorten them; production code never assigns them.
var (
	admissionWait   = 5 * time.Second
	operationLimit  = 10 * time.Second
	closeGrace      = 2 * time.Second
	closeCancelWait = 1 * time.Second
	diskCacheAge    = 30 * time.Second
	limitEventEvery = 10 * time.Second
)

var (
	minTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	maxTime = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)
